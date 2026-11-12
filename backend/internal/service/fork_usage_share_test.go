package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func f64(v float64) *float64 { return &v }

func TestSplitUsageDelta(t *testing.T) {
	t.Run("single user gets the whole rise", func(t *testing.T) {
		got := splitUsageDelta(0.02, []UsageUserCost{{UserID: 4, Cost: 1.5, Requests: 3}})
		require.Len(t, got, 1)
		require.Equal(t, int64(4), *got[0].UserID)
		require.InDelta(t, 0.02, got[0].Delta, 1e-12)
	})
	t.Run("multiple users split by cost", func(t *testing.T) {
		got := splitUsageDelta(0.04, []UsageUserCost{{UserID: 1, Cost: 3}, {UserID: 2, Cost: 1}})
		require.Len(t, got, 2)
		require.InDelta(t, 0.03, got[0].Delta, 1e-12)
		require.InDelta(t, 0.01, got[1].Delta, 1e-12)
	})
	t.Run("zero cost falls back to request count", func(t *testing.T) {
		got := splitUsageDelta(0.03, []UsageUserCost{{UserID: 1, Requests: 2}, {UserID: 2, Requests: 1}})
		require.InDelta(t, 0.02, got[0].Delta, 1e-12)
		require.InDelta(t, 0.01, got[1].Delta, 1e-12)
	})
	t.Run("no traffic is outside usage", func(t *testing.T) {
		got := splitUsageDelta(0.05, nil)
		require.Len(t, got, 1)
		require.Nil(t, got[0].UserID)
		require.InDelta(t, 0.05, got[0].Delta, 1e-12)
	})
	t.Run("no rise attributes nothing", func(t *testing.T) {
		require.Empty(t, splitUsageDelta(0, []UsageUserCost{{UserID: 1, Cost: 1}}))
	})
}

func TestPlanUsageSegment(t *testing.T) {
	reset := time.Date(2026, 11, 16, 17, 0, 0, 0, time.UTC)
	t0 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(10 * time.Minute)

	t.Run("first observation backfills the window", func(t *testing.T) {
		seg := planUsageSegment(nil, UsageWindow7d, 0.52, reset, t1)
		require.True(t, seg.record)
		require.True(t, seg.estimated)
		require.InDelta(t, 0.52, seg.delta, 1e-12)
		require.Equal(t, reset.Add(-7*24*time.Hour), seg.start)
	})
	last := &UsageSample{Utilization: 0.52, ResetAt: reset, SampledAt: t0}
	t.Run("rise within window", func(t *testing.T) {
		seg := planUsageSegment(last, UsageWindow7d, 0.54, reset.Add(time.Second), t1)
		require.True(t, seg.record)
		require.InDelta(t, 0.02, seg.delta, 1e-9)
		require.Equal(t, t0, seg.start)
		require.Equal(t, reset, seg.resetAt, "keeps the canonical reset")
	})
	t.Run("unchanged is ignored", func(t *testing.T) {
		require.False(t, planUsageSegment(last, UsageWindow7d, 0.52, reset, t1).record)
	})
	t.Run("drop rebaselines without attributing", func(t *testing.T) {
		seg := planUsageSegment(last, UsageWindow7d, 0.40, reset, t1)
		require.True(t, seg.record)
		require.Zero(t, seg.delta)
	})
	t.Run("new window starts from zero", func(t *testing.T) {
		next := reset.Add(7 * 24 * time.Hour)
		seg := planUsageSegment(last, UsageWindow7d, 0.03, next, reset.Add(time.Hour))
		require.True(t, seg.record)
		require.InDelta(t, 0.03, seg.delta, 1e-12)
		require.Equal(t, reset, seg.start, "segment starts at the new window start")
		require.Equal(t, next, seg.resetAt)
	})
}

// usageShareStoreStub implements UsageShareStore in memory for snapshot tests.
type usageShareStoreStub struct {
	settings []UsageShareSettings
	groups   map[int64][]int64
	sums     []UsageAttributionSum
}

func (s *usageShareStoreStub) ListUsageShareAccountStates(context.Context) ([]UsageShareAccountState, error) {
	return nil, nil
}
func (s *usageShareStoreStub) LatestUsageSamples(context.Context) ([]UsageSample, error) {
	return nil, nil
}
func (s *usageShareStoreStub) UsageCostByUser(context.Context, int64, time.Time, time.Time) ([]UsageUserCost, error) {
	return nil, nil
}
func (s *usageShareStoreStub) RecordUsageSegment(context.Context, UsageSample, time.Time, []UsageAttribution, bool) error {
	return nil
}
func (s *usageShareStoreStub) ListCurrentUsageAttributions(context.Context, time.Time) ([]UsageAttributionSum, error) {
	return s.sums, nil
}
func (s *usageShareStoreStub) ListUsageShareSettings(context.Context) ([]UsageShareSettings, error) {
	return s.settings, nil
}
func (s *usageShareStoreStub) SaveUsageShareSettings(context.Context, UsageShareSettings) error {
	return nil
}
func (s *usageShareStoreStub) ListUsageShareGroupAccounts(context.Context, []int64) (map[int64][]int64, error) {
	return s.groups, nil
}
func (s *usageShareStoreStub) ListUsageShareUsers(context.Context, int64, []int64) ([]UsageShareUser, error) {
	return nil, nil
}
func (s *usageShareStoreStub) PruneUsageShare(context.Context, time.Time) error { return nil }

func TestUsageShareEnforcement(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	resetA := now.Add(3 * 24 * time.Hour) // account 1 resets in 3 days
	resetB := now.Add(10 * time.Hour)     // account 2 resets in 10 hours
	u := func(id int64) *int64 { return &id }

	store := &usageShareStoreStub{
		settings: []UsageShareSettings{{
			GroupID: 7, Enabled: true, Default7dPct: f64(20),
			Spare7dMinPct: 40, Spare7dWithinHours: 24, Spare5hMinPct: 40, Spare5hWithinMinutes: 60,
			Limits: []UsageShareUserLimit{{UserID: 1, Limit7dPct: f64(100)}},
		}},
		groups: map[int64][]int64{7: {1, 2}},
		sums: []UsageAttributionSum{
			{AccountID: 1, Window: UsageWindow7d, ResetAt: resetA, UserID: u(4), Delta: 0.30},
			{AccountID: 2, Window: UsageWindow7d, ResetAt: resetB, UserID: u(4), Delta: 0.20},
			{AccountID: 1, Window: UsageWindow7d, ResetAt: resetA, UserID: u(1), Delta: 0.20},
			{AccountID: 1, Window: UsageWindow7d, ResetAt: resetA, UserID: nil, Delta: 0.05},
			{AccountID: 2, Window: UsageWindow7d, ResetAt: resetB.Add(-7 * 24 * time.Hour), UserID: u(5), Delta: 0.9}, // old window
		},
	}
	e := &usageShareEngine{store: store, now: func() time.Time { return now }, names: map[int64]string{},
		last: map[string]*UsageSample{
			usageSampleKey(1, UsageWindow7d): {AccountID: 1, Window: UsageWindow7d, Utilization: 0.55, ResetAt: resetA, SampledAt: now},
			usageSampleKey(2, UsageWindow7d): {AccountID: 2, Window: UsageWindow7d, Utilization: 0.20, ResetAt: resetB, SampledAt: now},
		}}
	require.NoError(t, e.rebuildLocked(context.Background()))
	g := e.snapshot.Load().groups[7]

	// user 4: (0.30 + 0.20) / 2 accounts = 25% of pool
	require.InDelta(t, 25, g.users[4].pct[UsageWindow7d], 1e-9)
	require.InDelta(t, 10, g.users[1].pct[UsageWindow7d], 1e-9)
	require.Nil(t, g.users[5], "attributions of finished windows don't count")

	require.Equal(t, []string{UsageWindow7d}, g.exceeded(4))
	require.Empty(t, g.exceeded(1), "override raises the limit")
	require.Empty(t, g.exceeded(6), "unused users are under any positive limit")

	// account 2: 80% left and resets in 10h -> spare; account 1 resets in 3 days -> not spare
	acc1, acc2 := e.snapshot.Load().accounts[1], e.snapshot.Load().accounts[2]
	require.False(t, g.accountHasSpare(acc1, []string{UsageWindow7d}, now))
	require.True(t, g.accountHasSpare(acc2, []string{UsageWindow7d}, now))

	// freesAt: dropping account 2's 10% (resets first) leaves 15% < 20%
	require.Equal(t, resetB, g.freesAt(4, UsageWindow7d, now))

	activeUsageShare.Store(e)
	t.Cleanup(func() { activeUsageShare.Store(nil) })

	_, blocked := ForkUsageShareBlocked(7, 4)
	require.False(t, blocked, "account 2 has spare capacity")

	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(4))
	ctx = context.WithValue(ctx, ctxkey.Group, &Group{ID: 7})
	require.False(t, forkUsageShareAllowsAccount(ctx, &Account{ID: 1}))
	require.True(t, forkUsageShareAllowsAccount(ctx, &Account{ID: 2}))

	// Without spare capacity the user is blocked with a message.
	e.last[usageSampleKey(2, UsageWindow7d)].Utilization = 0.95
	require.NoError(t, e.rebuildLocked(context.Background()))
	msg, blocked := ForkUsageShareBlocked(7, 4)
	require.True(t, blocked)
	require.Contains(t, msg, "weekly 25.0% of 20.0%")

	// Other users and other groups are unaffected.
	_, blocked = ForkUsageShareBlocked(7, 1)
	require.False(t, blocked)
	_, blocked = ForkUsageShareBlocked(8, 4)
	require.False(t, blocked)

	// Stale snapshots fail open.
	e.now = func() time.Time { return now.Add(time.Hour) }
	_, blocked = ForkUsageShareBlocked(7, 4)
	require.False(t, blocked)
}
