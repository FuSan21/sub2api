//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestForkUsageShareRepository(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	suffix := time.Now().UnixNano()

	reset7d := time.Now().Add(72 * time.Hour).Truncate(time.Second)
	reset5h := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	sampled := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)

	group := mustCreateGroup(t, client, &service.Group{Name: fmt.Sprintf("fork-share-%d", suffix)})
	account := mustCreateAccount(t, client, &service.Account{
		Name: fmt.Sprintf("fork-share-acc-%d", suffix),
		Extra: map[string]any{
			"session_window_utilization":   0.25,
			"passive_usage_7d_utilization": 0.52,
			"passive_usage_7d_reset":       reset7d.Unix(),
			"passive_usage_sampled_at":     sampled.Format(time.RFC3339),
		},
		SessionWindowEnd: &reset5h,
	})
	require.NoError(t, repo.AddToGroup(ctx, account.ID, group.ID, 1))
	u1 := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("share1-%d@example.com", suffix)})
	u2 := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("share2-%d@example.com", suffix)})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: u1.ID, GroupID: &group.ID, Key: fmt.Sprintf("sk-share-%d", suffix)})

	for i, l := range []struct {
		userID int64
		cost   float64
		at     time.Time
	}{
		{u1.ID, 3, time.Now().Add(-30 * time.Minute)},
		{u2.ID, 1, time.Now().Add(-20 * time.Minute)},
		{u1.ID, 5, time.Now().Add(-10 * 24 * time.Hour)}, // outside the window
	} {
		_, err := client.UsageLog.Create().SetUserID(l.userID).SetAPIKeyID(key.ID).SetAccountID(account.ID).
			SetRequestID(fmt.Sprintf("share-%d-%d", suffix, i)).SetModel("claude-opus").SetGroupID(group.ID).
			SetTotalCost(l.cost).SetCreatedAt(l.at).Save(ctx)
		require.NoError(t, err)
	}

	states, err := repo.ListUsageShareAccountStates(ctx)
	require.NoError(t, err)
	var st *service.UsageShareAccountState
	for i := range states {
		if states[i].ID == account.ID {
			st = &states[i]
		}
	}
	require.NotNil(t, st)
	require.InDelta(t, 0.52, *st.Util[service.UsageWindow7d], 1e-9)
	require.InDelta(t, 0.25, *st.Util[service.UsageWindow5h], 1e-9)
	require.True(t, reset7d.Equal(*st.Reset[service.UsageWindow7d]))
	require.True(t, reset5h.Equal(*st.Reset[service.UsageWindow5h]))
	require.True(t, sampled.Equal(*st.SampledAt))

	costs, err := repo.UsageCostByUser(ctx, account.ID, reset7d.Add(-7*24*time.Hour), time.Now())
	require.NoError(t, err)
	require.Len(t, costs, 2)

	// Includes a NULL (outside) attribution.
	uid := u1.ID
	sample := service.UsageSample{AccountID: account.ID, Window: service.UsageWindow7d, Utilization: 0.52, ResetAt: reset7d, SampledAt: sampled}
	require.NoError(t, repo.RecordUsageSegment(ctx, sample, reset7d.Add(-7*24*time.Hour),
		[]service.UsageAttribution{{UserID: &uid, Delta: 0.4}, {Delta: 0.12}}, true))
	// A sample with no attributions still records the sample.
	sample.Utilization, sample.SampledAt = 0.50, sampled.Add(time.Second)
	require.NoError(t, repo.RecordUsageSegment(ctx, sample, sampled, nil, false))

	latest, err := repo.LatestUsageSamples(ctx)
	require.NoError(t, err)
	var found bool
	for _, s := range latest {
		if s.AccountID == account.ID && s.Window == service.UsageWindow7d {
			found = true
			require.InDelta(t, 0.50, s.Utilization, 1e-9)
		}
	}
	require.True(t, found)

	sums, err := repo.ListCurrentUsageAttributions(ctx, time.Now())
	require.NoError(t, err)
	var userSum, outside float64
	for _, s := range sums {
		if s.AccountID != account.ID {
			continue
		}
		if s.UserID == nil {
			outside += s.Delta
		} else if *s.UserID == u1.ID {
			userSum += s.Delta
		}
	}
	require.InDelta(t, 0.4, userSum, 1e-9)
	require.InDelta(t, 0.12, outside, 1e-9)

	for _, c := range costs {
		require.NotNil(t, c.GroupID)
		require.Equal(t, group.ID, *c.GroupID)
	}
	gid := group.ID
	require.NoError(t, repo.RecordUsageSegment(ctx, service.UsageSample{AccountID: account.ID, Window: service.UsageWindow5h,
		Utilization: 0.25, ResetAt: reset5h, SampledAt: sampled}, sampled.Add(-time.Hour),
		[]service.UsageAttribution{{UserID: &uid, GroupID: &gid, Delta: 0.25}}, false))
	sums, err = repo.ListCurrentUsageAttributions(ctx, time.Now())
	require.NoError(t, err)
	var grouped float64
	for _, s := range sums {
		if s.AccountID == account.ID && s.GroupID != nil && *s.GroupID == group.ID {
			grouped += s.Delta
		}
	}
	require.InDelta(t, 0.25, grouped, 1e-9)

	// Global settings: seeded by the migration, then replaced.
	settings, err := repo.GetUsageQuotaSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, 24, settings.Spare7dWithinHours)
	settings.Spare5hMinPct = 30
	require.NoError(t, repo.SaveUsageQuotaSettings(ctx, settings))
	settings, err = repo.GetUsageQuotaSettings(ctx)
	require.NoError(t, err)
	require.InDelta(t, 30, settings.Spare5hMinPct, 1e-9)

	// Account capacity: detected tier, admin override, re-detect.
	tier := service.UsageTierMax5x
	require.NoError(t, repo.SaveDetectedTier(ctx, account.ID, &tier, nil, time.Now()))
	require.NoError(t, repo.SaveAccountWeights(ctx, account.ID, nil, ptrFloat(4), false))
	caps, err := repo.ListAccountCapacities(ctx)
	require.NoError(t, err)
	c := caps[account.ID]
	require.Equal(t, service.UsageTierMax5x, *c.DetectedTier)
	require.NotNil(t, c.DetectedAt)
	require.Nil(t, c.Weight5h)
	require.InDelta(t, 4, *c.Weight7d, 1e-9)
	require.NoError(t, repo.SaveAccountWeights(ctx, account.ID, nil, nil, true))
	caps, err = repo.ListAccountCapacities(ctx)
	require.NoError(t, err)
	require.Nil(t, caps[account.ID].DetectedAt, "re-detect clears the detection time")
	require.Equal(t, service.UsageTierMax5x, *caps[account.ID].DetectedTier, "keeps the tier until re-detected")
	require.ErrorIs(t, repo.SaveAccountWeights(ctx, -1, nil, nil, false), service.ErrUsageQuotaTargetNotFound)

	groupAccounts, err := repo.ListUsageShareGroupAccounts(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{account.ID}, groupAccounts[group.ID])

	// Quotas: none -> group default -> subscription row -> cleared back to default.
	sub := mustCreateSubscription(t, client, &service.UserSubscription{UserID: u1.ID, GroupID: group.ID})
	quotaOf := func() service.UsageQuotaSubscription {
		subs, err := repo.ListQuotaSubscriptions(ctx)
		require.NoError(t, err)
		for _, s := range subs {
			if s.ID == sub.ID {
				return s
			}
		}
		t.Fatalf("subscription %d not listed", sub.ID)
		return service.UsageQuotaSubscription{}
	}
	got := quotaOf()
	require.Equal(t, service.UsageQuotaSourceNone, got.Source)
	require.Nil(t, got.Quota.Quota7dX)
	require.Equal(t, u1.Email, got.Email)
	require.Equal(t, group.Name, got.GroupName)

	require.NoError(t, repo.SaveGroupQuotaDefault(ctx, group.ID, &service.UsageQuota{Quota7dX: ptrFloat(0.5)}))
	def, err := repo.GetGroupQuotaDefault(ctx, group.ID)
	require.NoError(t, err)
	require.InDelta(t, 0.5, *def.Quota7dX, 1e-9)
	got = quotaOf()
	require.Equal(t, service.UsageQuotaSourceGroup, got.Source)
	require.InDelta(t, 0.5, *got.Quota.Quota7dX, 1e-9)

	require.NoError(t, repo.SaveSubscriptionQuota(ctx, sub.ID, &service.UsageQuota{Quota5hX: ptrFloat(2)}))
	got = quotaOf()
	require.Equal(t, service.UsageQuotaSourceAdmin, got.Source)
	require.InDelta(t, 2, *got.Quota.Quota5hX, 1e-9)
	require.Nil(t, got.Quota.Quota7dX, "the row replaces the default in both windows")
	require.ErrorIs(t, repo.SaveSubscriptionQuota(ctx, -1, &service.UsageQuota{}), service.ErrUsageQuotaTargetNotFound)

	// Plan quota copied on purchase; an older order never overwrites a newer one.
	plan, err := client.SubscriptionPlan.Create().SetGroupID(group.ID).SetName(fmt.Sprintf("plan-%d", suffix)).SetPrice(20).Save(ctx)
	require.NoError(t, err)
	applied, err := repo.ApplyPlanQuota(ctx, plan.ID, 100, u1.ID, group.ID)
	require.NoError(t, err)
	require.False(t, applied, "plan without a quota changes nothing")
	require.NoError(t, repo.SavePlanQuota(ctx, plan.ID, &service.UsageQuota{Quota5hX: ptrFloat(1), Quota7dX: ptrFloat(1)}))
	plans, err := repo.ListPlanQuotas(ctx)
	require.NoError(t, err)
	require.InDelta(t, 1, *plans[plan.ID].Quota7dX, 1e-9)
	applied, err = repo.ApplyPlanQuota(ctx, plan.ID, 100, u1.ID, group.ID)
	require.NoError(t, err)
	require.True(t, applied)
	got = quotaOf()
	require.Equal(t, service.UsageQuotaSourcePlan, got.Source)
	require.Equal(t, plan.ID, *got.PlanID)
	require.InDelta(t, 1, *got.Quota.Quota7dX, 1e-9)
	applied, err = repo.ApplyPlanQuota(ctx, plan.ID, 99, u1.ID, group.ID)
	require.NoError(t, err)
	require.False(t, applied, "older order")
	require.NoError(t, repo.SaveSubscriptionQuota(ctx, sub.ID, &service.UsageQuota{Quota7dX: ptrFloat(3)}))
	applied, err = repo.ApplyPlanQuota(ctx, plan.ID, 100, u1.ID, group.ID)
	require.NoError(t, err)
	require.False(t, applied, "a retried order keeps the admin edit")
	applied, err = repo.ApplyPlanQuota(ctx, plan.ID, 101, u1.ID, group.ID)
	require.NoError(t, err)
	require.True(t, applied, "a newer purchase wins")

	require.NoError(t, repo.SaveSubscriptionQuota(ctx, sub.ID, nil))
	require.Equal(t, service.UsageQuotaSourceGroup, quotaOf().Source)
	require.NoError(t, repo.SavePlanQuota(ctx, plan.ID, nil))
	require.NoError(t, repo.SaveGroupQuotaDefault(ctx, group.ID, nil))
	require.Equal(t, service.UsageQuotaSourceNone, quotaOf().Source)

	require.NoError(t, repo.PruneUsageShare(ctx, time.Now().Add(-9*24*time.Hour)))
}

func ptrFloat(v float64) *float64 { return &v }
