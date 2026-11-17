package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type forkPriorityRepoStub struct {
	AccountRepository
	overrides map[int64]map[int64]int
	loads     int
}

func (r *forkPriorityRepoStub) ListGroupAccountPriorityOverrides(_ context.Context, groupID int64) (map[int64]int, error) {
	r.loads++
	return r.overrides[groupID], nil
}

func (r *forkPriorityRepoStub) ListGroupAccountPriorities(context.Context, int64) ([]GroupAccountPriority, error) {
	return nil, nil
}

func (r *forkPriorityRepoStub) ReplaceGroupAccountPriorityOverrides(context.Context, int64, map[int64]int) error {
	return nil
}

func forkTestAccounts() []Account {
	inGroups := func(id int64, priority int, groups ...int64) Account {
		a := Account{ID: id, Priority: priority}
		for _, g := range groups {
			a.AccountGroups = append(a.AccountGroups, AccountGroup{AccountID: id, GroupID: g})
		}
		return a
	}
	return []Account{inGroups(1, 1, 7, 8), inGroups(2, 50, 7), inGroups(3, 50, 8)}
}

func TestApplyForkGroupScheduling(t *testing.T) {
	repo := &forkPriorityRepoStub{overrides: map[int64]map[int64]int{8: {3: 0, 1: 10}}}
	groupAccountPriorities.invalidate(7)
	groupAccountPriorities.invalidate(8)

	simple := &SchedulerSnapshotService{accountRepo: repo, cfg: &config.Config{RunMode: config.RunModeSimple}}
	standard := &SchedulerSnapshotService{accountRepo: repo, cfg: &config.Config{RunMode: config.RunModeStandard}}
	g7, g8 := int64(7), int64(8)

	t.Run("simple mode scopes to group without overrides", func(t *testing.T) {
		got := simple.applyForkGroupScheduling(context.Background(), &g7, forkTestAccounts())
		require.Len(t, got, 2)
		require.Equal(t, int64(1), got[0].ID)
		require.Equal(t, 1, got[0].Priority)
		require.Equal(t, int64(2), got[1].ID)
		require.Equal(t, 50, got[1].Priority)
	})

	t.Run("simple mode scopes and applies overrides", func(t *testing.T) {
		got := simple.applyForkGroupScheduling(context.Background(), &g8, forkTestAccounts())
		require.Len(t, got, 2)
		require.Equal(t, int64(1), got[0].ID)
		require.Equal(t, 10, got[0].Priority)
		require.Equal(t, int64(3), got[1].ID)
		require.Equal(t, 0, got[1].Priority)
	})

	t.Run("standard mode only applies overrides", func(t *testing.T) {
		got := standard.applyForkGroupScheduling(context.Background(), &g8, forkTestAccounts())
		require.Len(t, got, 3)
		require.Equal(t, 10, got[0].Priority)
		require.Equal(t, 50, got[1].Priority)
		require.Equal(t, 0, got[2].Priority)
	})

	t.Run("ungrouped keys are untouched", func(t *testing.T) {
		got := simple.applyForkGroupScheduling(context.Background(), nil, forkTestAccounts())
		require.Len(t, got, 3)
		require.Equal(t, 1, got[0].Priority)
	})

	t.Run("overrides are cached per group", func(t *testing.T) {
		before := repo.loads
		simple.applyForkGroupScheduling(context.Background(), &g8, forkTestAccounts())
		require.Equal(t, before, repo.loads)
		groupAccountPriorities.invalidate(8)
		simple.applyForkGroupScheduling(context.Background(), &g8, forkTestAccounts())
		require.Equal(t, before+1, repo.loads)
	})
}

type forkHitCache struct {
	SchedulerCache
	shared []*Account
}

func (c *forkHitCache) GetSnapshot(context.Context, SchedulerBucket) ([]*Account, bool, error) {
	return c.shared, true, nil
}

type forkFallbackRepo struct {
	*forkPriorityRepoStub
	accounts []Account
}

func (r *forkFallbackRepo) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]Account, error) {
	return r.accounts, nil
}

func forkSchedulableTestAccounts() []Account {
	accounts := forkTestAccounts()
	for i := range accounts {
		accounts[i].Platform = PlatformOpenAI
		accounts[i].Status = StatusActive
		accounts[i].Schedulable = true
	}
	return accounts
}

// The fork hook must stay wired into ListSchedulableAccounts on both the cache
// hit and the DB fallback path, without mutating data shared between callers.
func TestListSchedulableAccountsAppliesForkGroupScheduling(t *testing.T) {
	g8 := int64(8)
	simpleCfg := func() *config.Config {
		return &config.Config{RunMode: config.RunModeSimple, Gateway: config.GatewayConfig{Scheduling: config.GatewaySchedulingConfig{DbFallbackEnabled: true}}}
	}
	requireScopedG8 := func(t *testing.T, got []Account) {
		t.Helper()
		require.Len(t, got, 2)
		require.Equal(t, int64(1), got[0].ID)
		require.Equal(t, 10, got[0].Priority)
		require.Equal(t, int64(3), got[1].ID)
		require.Equal(t, 0, got[1].Priority)
	}

	t.Run("cache hit", func(t *testing.T) {
		groupAccountPriorities.invalidate(8)
		repo := &forkPriorityRepoStub{overrides: map[int64]map[int64]int{8: {3: 0, 1: 10}}}
		accounts := forkSchedulableTestAccounts()
		shared := []*Account{&accounts[0], &accounts[1], &accounts[2]}
		svc := NewSchedulerSnapshotService(&forkHitCache{shared: shared}, nil, repo, nil, simpleCfg())

		got, _, err := svc.ListSchedulableAccounts(context.Background(), &g8, PlatformOpenAI, false)
		require.NoError(t, err)
		requireScopedG8(t, got)
		require.Equal(t, 1, shared[0].Priority, "cached accounts must not be mutated")

		all, _, err := svc.ListSchedulableAccounts(context.Background(), nil, PlatformOpenAI, false)
		require.NoError(t, err)
		require.Len(t, all, 3, "ungrouped keys keep upstream behaviour")
	})

	t.Run("db fallback", func(t *testing.T) {
		groupAccountPriorities.invalidate(8)
		repo := &forkFallbackRepo{
			forkPriorityRepoStub: &forkPriorityRepoStub{overrides: map[int64]map[int64]int{8: {3: 0, 1: 10}}},
			accounts:             forkSchedulableTestAccounts(),
		}
		svc := NewSchedulerSnapshotService(nil, nil, repo, nil, simpleCfg())

		got, _, err := svc.ListSchedulableAccounts(context.Background(), &g8, PlatformOpenAI, false)
		require.NoError(t, err)
		requireScopedG8(t, got)
		require.Equal(t, 1, repo.accounts[0].Priority, "repository accounts must not be mutated")
	})
}
