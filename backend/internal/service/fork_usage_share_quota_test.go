package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestSplitUsageDeltaKeepsGroup(t *testing.T) {
	g1, g2 := int64(7), int64(8)
	got := splitUsageDelta(0.04, []UsageUserCost{{UserID: 1, GroupID: &g1, Cost: 1}, {UserID: 1, GroupID: &g2, Cost: 1}, {UserID: 2, Cost: 2}})
	require.Len(t, got, 3)
	require.Equal(t, int64(7), *got[0].GroupID)
	require.Equal(t, int64(8), *got[1].GroupID)
	require.Nil(t, got[2].GroupID)
	require.InDelta(t, 0.02, got[2].Delta, 1e-12)
}

func TestNormalizeClaudeTier(t *testing.T) {
	tier := func(org, rate string, max, pro bool) string {
		var p claudeProfileTier
		p.Organization.OrganizationType, p.Organization.RateLimitTier = org, rate
		p.Account.HasClaudeMax, p.Account.HasClaudePro = max, pro
		return normalizeClaudeTier(p)
	}
	require.Equal(t, UsageTierMax20x, tier("claude_max", "default_claude_max_20x", true, false))
	require.Equal(t, UsageTierMax5x, tier("claude_max", "default_claude_max_5x", true, false))
	require.Equal(t, UsageTierPro, tier("claude_pro", "default_claude_ai", false, true))
	require.Equal(t, UsageTierPro, tier("", "", false, true))
	require.Equal(t, "claude_team/default_claude_ai", tier("claude_team", "default_claude_ai", false, false))
	require.Equal(t, "unknown", tier("", "", false, false))
}

func TestUsageAccountCapacityWeight(t *testing.T) {
	s := func(v string) *string { return &v }
	require.Equal(t, 1.0, UsageAccountCapacity{}.weight(UsageWindow5h), "unknown tier counts as one Pro")
	require.Equal(t, 20.0, UsageAccountCapacity{DetectedTier: s(UsageTierMax20x)}.weight(UsageWindow7d))
	require.Equal(t, 1.0, UsageAccountCapacity{DetectedTier: s("claude_team/x")}.weight(UsageWindow7d))
	c := UsageAccountCapacity{DetectedTier: s(UsageTierMax5x), Weight7d: f64(4)}
	require.Equal(t, 5.0, c.weight(UsageWindow5h))
	require.Equal(t, 4.0, c.weight(UsageWindow7d), "admin override wins")
}

// usageShareStoreStub implements UsageShareStore in memory for snapshot tests.
type usageShareStoreStub struct {
	caps   map[int64]UsageAccountCapacity
	groups map[int64][]int64
	subs   []UsageQuotaSubscription
	sums   []UsageAttributionSum
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
func (s *usageShareStoreStub) PruneUsageShare(context.Context, time.Time) error { return nil }
func (s *usageShareStoreStub) GetUsageQuotaSettings(context.Context) (UsageQuotaSettings, error) {
	return defaultUsageQuotaSettings(), nil
}
func (s *usageShareStoreStub) SaveUsageQuotaSettings(context.Context, UsageQuotaSettings) error {
	return nil
}
func (s *usageShareStoreStub) ListAccountCapacities(context.Context) (map[int64]UsageAccountCapacity, error) {
	return s.caps, nil
}
func (s *usageShareStoreStub) SaveAccountWeights(context.Context, int64, *float64, *float64, bool) error {
	return nil
}
func (s *usageShareStoreStub) SaveDetectedTier(context.Context, int64, *string, *string, time.Time) error {
	return nil
}
func (s *usageShareStoreStub) ListUsageShareGroupAccounts(context.Context) (map[int64][]int64, error) {
	return s.groups, nil
}
func (s *usageShareStoreStub) ListQuotaSubscriptions(context.Context) ([]UsageQuotaSubscription, error) {
	return s.subs, nil
}
func (s *usageShareStoreStub) GetGroupQuotaDefault(context.Context, int64) (*UsageQuota, error) {
	return nil, nil
}
func (s *usageShareStoreStub) SaveGroupQuotaDefault(context.Context, int64, *UsageQuota) error {
	return nil
}
func (s *usageShareStoreStub) SaveSubscriptionQuota(context.Context, int64, *UsageQuota) error {
	return nil
}
func (s *usageShareStoreStub) ListPlanQuotas(context.Context) (map[int64]UsageQuota, error) {
	return nil, nil
}
func (s *usageShareStoreStub) SavePlanQuota(context.Context, int64, *UsageQuota) error { return nil }
func (s *usageShareStoreStub) ApplyPlanQuota(context.Context, int64, int64, int64, int64) (bool, error) {
	return false, nil
}

func TestUsageQuotaEnforcement(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	resetA := now.Add(3 * 24 * time.Hour) // account 1 (Max 20x) resets in 3 days
	resetB := now.Add(10 * time.Hour)     // account 2 (Pro) resets in 10 hours
	id := func(v int64) *int64 { return &v }
	tier := func(v string) *string { return &v }
	const group = int64(7)

	store := &usageShareStoreStub{
		caps: map[int64]UsageAccountCapacity{
			1: {AccountID: 1, DetectedTier: tier(UsageTierMax20x)},
			2: {AccountID: 2, DetectedTier: tier(UsageTierPro)},
		},
		groups: map[int64][]int64{group: {1, 2}},
		subs: []UsageQuotaSubscription{
			{ID: 40, UserID: 4, GroupID: group, GroupName: "pool", Quota: UsageQuota{Quota7dX: f64(2)}, Source: UsageQuotaSourcePlan},
			{ID: 10, UserID: 1, GroupID: group, GroupName: "pool", Quota: UsageQuota{Quota7dX: f64(10)}, Source: UsageQuotaSourceAdmin},
		},
		sums: []UsageAttributionSum{
			// user 4: 10% of the Max 20x (2x) + 20% of the Pro (0.2x) = 2.2x
			{AccountID: 1, Window: UsageWindow7d, ResetAt: resetA, UserID: id(4), GroupID: id(group), Delta: 0.10},
			{AccountID: 2, Window: UsageWindow7d, ResetAt: resetB, UserID: id(4), GroupID: id(group), Delta: 0.20},
			// user 4 in another group doesn't count against this subscription
			{AccountID: 1, Window: UsageWindow7d, ResetAt: resetA, UserID: id(4), GroupID: id(9), Delta: 0.10},
			{AccountID: 1, Window: UsageWindow7d, ResetAt: resetA, UserID: id(1), GroupID: id(group), Delta: 0.20},
			{AccountID: 1, Window: UsageWindow7d, ResetAt: resetA, UserID: nil, Delta: 0.05},
			{AccountID: 2, Window: UsageWindow7d, ResetAt: resetB.Add(-7 * 24 * time.Hour), UserID: id(5), GroupID: id(group), Delta: 0.9}, // old window
		},
	}
	e := &usageShareEngine{store: store, now: func() time.Time { return now }, names: map[int64]string{}, types: map[int64]string{},
		last: map[string]*UsageSample{
			usageSampleKey(1, UsageWindow7d): {AccountID: 1, Window: UsageWindow7d, Utilization: 0.45, ResetAt: resetA, SampledAt: now},
			usageSampleKey(2, UsageWindow7d): {AccountID: 2, Window: UsageWindow7d, Utilization: 0.20, ResetAt: resetB, SampledAt: now},
		}}
	require.NoError(t, e.rebuildLocked(context.Background()))
	snap := e.snapshot.Load()

	sub4, sub1 := snap.subFor(4, group), snap.subFor(1, group)
	require.InDelta(t, 2.2, sub4.used(UsageWindow7d), 1e-9)
	require.InDelta(t, 4.0, sub1.used(UsageWindow7d), 1e-9)
	require.Nil(t, snap.subFor(5, group), "no subscription")
	require.InDelta(t, 21, snap.capacity(group, UsageWindow7d), 1e-9)

	require.Equal(t, []string{UsageWindow7d}, sub4.exceeded())
	require.Empty(t, sub1.exceeded())

	// account 2: 80% left and resets in 10h -> spare; account 1 resets in 3 days -> not
	require.False(t, snap.accountHasSpare(1, []string{UsageWindow7d}, now))
	require.True(t, snap.accountHasSpare(2, []string{UsageWindow7d}, now))

	// freesAt: dropping account 2's 0.2x (resets first) leaves 2.0x, not below 2; then account 1
	require.Equal(t, resetA, sub4.freesAt(UsageWindow7d, now))

	activeUsageShare.Store(e)
	t.Cleanup(func() { activeUsageShare.Store(nil) })

	_, blocked := ForkUsageShareBlocked(group, 4)
	require.False(t, blocked, "account 2 has spare capacity")

	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(4))
	ctx = context.WithValue(ctx, ctxkey.Group, &Group{ID: group})
	require.False(t, forkUsageShareAllowsAccount(ctx, &Account{ID: 1}))
	require.True(t, forkUsageShareAllowsAccount(ctx, &Account{ID: 2}))

	// Without spare capacity the subscription is blocked with a message.
	e.last[usageSampleKey(2, UsageWindow7d)].Utilization = 0.95
	require.NoError(t, e.rebuildLocked(context.Background()))
	msg, blocked := ForkUsageShareBlocked(group, 4)
	require.True(t, blocked)
	require.Contains(t, msg, "weekly 2.20x of 2.00x")

	// Other subscriptions, users without one, and other groups are unaffected.
	for _, c := range [][2]int64{{group, 1}, {group, 5}, {9, 4}} {
		_, blocked = ForkUsageShareBlocked(c[0], c[1])
		require.False(t, blocked)
	}

	st, err := ForkUsageShareStatusForGroup(context.Background(), 4, group, "pool")
	require.NoError(t, err)
	require.True(t, st.Enforced)
	require.Equal(t, int64(40), *st.SubscriptionID)
	require.Equal(t, UsageQuotaSourcePlan, st.QuotaSource)
	require.Equal(t, 2, st.Accounts)
	require.InDelta(t, 2.2, st.Used7dX, 1e-9)
	require.InDelta(t, 0, *st.Remaining7dX, 1e-9)
	require.Nil(t, st.Quota5hX)
	require.True(t, st.Blocked)
	require.Equal(t, resetB, *st.NextDrop7d, "soonest reset among accounts with usage")

	// Usage without a subscription is reported but not enforced.
	st, err = ForkUsageShareStatusForGroup(context.Background(), 4, 9, "other")
	require.NoError(t, err)
	require.Nil(t, st.SubscriptionID)
	require.False(t, st.Enforced)
	require.InDelta(t, 2.0, st.Used7dX, 1e-9)

	all, err := ForkUsageShareStatuses(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, all, 2)

	rep, err := e.report(context.Background(), group)
	require.NoError(t, err)
	require.InDelta(t, 12, rep.Allocated7dX, 1e-9)
	require.Equal(t, 2, rep.Unlimited5h)
	require.InDelta(t, 2.0, rep.Accounts[0].BySubscription7dX[40], 1e-9)
	require.InDelta(t, 2.0, rep.Accounts[0].Other7dX, 1e-9, "user 4 usage in group 9")
	require.InDelta(t, 1.0, rep.Accounts[0].Outside7dX, 1e-9)

	// Stale snapshots fail open.
	e.now = func() time.Time { return now.Add(time.Hour) }
	_, blocked = ForkUsageShareBlocked(group, 4)
	require.False(t, blocked)
}
