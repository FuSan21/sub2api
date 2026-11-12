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
