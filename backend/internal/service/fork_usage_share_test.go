package service

import (
	"testing"
	"time"

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

