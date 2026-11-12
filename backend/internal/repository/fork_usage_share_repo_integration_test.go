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
			SetRequestID(fmt.Sprintf("share-%d-%d", suffix, i)).SetModel("claude-opus").
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

	settings := service.UsageShareSettings{
		GroupID: group.ID, Enabled: true, Default7dPct: ptrFloat(20),
		Spare7dMinPct: 40, Spare7dWithinHours: 24, Spare5hMinPct: 40, Spare5hWithinMinutes: 60,
		Limits: []service.UsageShareUserLimit{{UserID: u1.ID, Limit7dPct: ptrFloat(50)}, {UserID: u2.ID}},
	}
	require.NoError(t, repo.SaveUsageShareSettings(ctx, settings))
	settings.Limits = []service.UsageShareUserLimit{{UserID: u2.ID, Limit5hPct: ptrFloat(10)}}
	require.NoError(t, repo.SaveUsageShareSettings(ctx, settings)) // replaces u1's override
	all, err := repo.ListUsageShareSettings(ctx)
	require.NoError(t, err)
	var got *service.UsageShareSettings
	for i := range all {
		if all[i].GroupID == group.ID {
			got = &all[i]
		}
	}
	require.NotNil(t, got)
	require.True(t, got.Enabled)
	require.InDelta(t, 20, *got.Default7dPct, 1e-9)
	require.Nil(t, got.Default5hPct)
	require.Len(t, got.Limits, 1)
	require.Equal(t, u2.ID, got.Limits[0].UserID)
	require.InDelta(t, 10, *got.Limits[0].Limit5hPct, 1e-9)

	groupAccounts, err := repo.ListUsageShareGroupAccounts(ctx, []int64{group.ID})
	require.NoError(t, err)
	require.Equal(t, []int64{account.ID}, groupAccounts[group.ID])

	users, err := repo.ListUsageShareUsers(ctx, group.ID, []int64{u2.ID})
	require.NoError(t, err)
	require.Len(t, users, 2) // u1 via api key, u2 via extra ids

	require.NoError(t, repo.PruneUsageShare(ctx, time.Now().Add(-9*24*time.Hour)))
}

func ptrFloat(v float64) *float64 { return &v }
