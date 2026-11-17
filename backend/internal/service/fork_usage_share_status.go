package service

// [fork] Usage share status for end users (dashboard, API-key endpoint used by
// the Claude Code status line) and the admin Users / Subscriptions pages, plus
// the admin group report.

import (
	"context"
	"math"
	"sort"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// UsageShareStatus is one user's usage in one group, in x (1x = one Pro account).
type UsageShareStatus struct {
	UserID         int64      `json:"user_id"`
	Email          string     `json:"email"`
	Username       string     `json:"username"`
	GroupID        int64      `json:"group_id"`
	GroupName      string     `json:"group_name"`
	SubscriptionID *int64     `json:"subscription_id"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	QuotaSource    string     `json:"quota_source"`
	Enforced       bool       `json:"enforced"`
	Accounts       int        `json:"accounts"`
	Capacity5hX    float64    `json:"capacity_5h_x"`
	Capacity7dX    float64    `json:"capacity_7d_x"`
	Used5hX        float64    `json:"used_5h_x"`
	Used7dX        float64    `json:"used_7d_x"`
	Quota5hX       *float64   `json:"quota_5h_x"`
	Quota7dX       *float64   `json:"quota_7d_x"`
	Remaining5hX   *float64   `json:"remaining_5h_x"`
	Remaining7dX   *float64   `json:"remaining_7d_x"`
	Blocked        bool       `json:"blocked"`
	FreesAt        *time.Time `json:"frees_at,omitempty"`
	// Soonest reset of an account the user has usage on, i.e. when their usage next drops.
	NextDrop5h *time.Time `json:"next_drop_5h,omitempty"`
	NextDrop7d *time.Time `json:"next_drop_7d,omitempty"`
}

var errUsageShareUnavailable = infraerrors.ServiceUnavailable("USAGE_SHARE_UNAVAILABLE", "usage share is not available")

func usageShareSnapshotForRead() (*usageShareEngine, *usageShareSnapshot, error) {
	e := activeUsageShare.Load()
	if e == nil {
		return nil, nil, errUsageShareUnavailable
	}
	snap := e.snapshot.Load()
	if snap == nil {
		return nil, nil, errUsageShareUnavailable
	}
	return e, snap, nil
}

// ForkUsageShareStatuses returns one status per live subscription of the given
// user (nil = all users).
func ForkUsageShareStatuses(_ context.Context, userID *int64) ([]UsageShareStatus, error) {
	e, snap, err := usageShareSnapshotForRead()
	if err != nil {
		return nil, err
	}
	now := e.now()
	out := make([]UsageShareStatus, 0)
	for _, v := range snap.subs {
		if userID != nil && v.sub.UserID != *userID {
			continue
		}
		out = append(out, snap.subStatus(v, now))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].UserID != out[j].UserID {
			return out[i].UserID < out[j].UserID
		}
		return out[i].GroupID < out[j].GroupID
	})
	return out, nil
}

// ForkUsageShareStatusForGroup returns the status of one user in one group. Without
// a live subscription the usage is still reported but nothing is enforced.
func ForkUsageShareStatusForGroup(_ context.Context, userID, groupID int64, groupName string) (*UsageShareStatus, error) {
	e, snap, err := usageShareSnapshotForRead()
	if err != nil {
		return nil, err
	}
	now := e.now()
	if v := snap.subFor(userID, groupID); v != nil {
		st := snap.subStatus(v, now)
		return &st, nil
	}
	st := UsageShareStatus{UserID: userID, GroupID: groupID, GroupName: groupName, QuotaSource: UsageQuotaSourceNone}
	snap.fillUsage(&st, snap.usage[usageKey{userID: userID, groupID: groupID}], now)
	return &st, nil
}

func (s *usageShareSnapshot) fillUsage(st *UsageShareStatus, u *usageView, now time.Time) {
	st.Accounts = len(s.groupAccounts[st.GroupID])
	st.Capacity5hX = s.capacity(st.GroupID, UsageWindow5h)
	st.Capacity7dX = s.capacity(st.GroupID, UsageWindow7d)
	if u != nil {
		st.Used5hX, st.Used7dX = u.used[UsageWindow5h], u.used[UsageWindow7d]
		st.NextDrop5h, st.NextDrop7d = u.nextDrop(UsageWindow5h, now), u.nextDrop(UsageWindow7d, now)
	}
}

func (s *usageShareSnapshot) subStatus(v *usageSubView, now time.Time) UsageShareStatus {
	id, expires := v.sub.ID, v.sub.ExpiresAt
	st := UsageShareStatus{
		UserID: v.sub.UserID, Email: v.sub.Email, Username: v.sub.Username,
		GroupID: v.sub.GroupID, GroupName: v.sub.GroupName,
		SubscriptionID: &id, ExpiresAt: &expires,
		QuotaSource: v.sub.Source, Enforced: v.sub.Quota.limited(),
		Quota5hX: v.sub.Quota.Quota5hX, Quota7dX: v.sub.Quota.Quota7dX,
	}
	s.fillUsage(&st, v.usage, now)
	st.Remaining5hX = remainingX(st.Quota5hX, st.Used5hX)
	st.Remaining7dX = remainingX(st.Quota7dX, st.Used7dX)
	if over := v.exceeded(); len(over) > 0 {
		st.Blocked = !s.groupHasSpare(v.sub.GroupID, over, now)
		if until := v.freesAtAll(over, now); until.After(now) {
			st.FreesAt = &until
		}
	}
	return st
}

func (u *usageView) nextDrop(window string, now time.Time) *time.Time {
	var next *time.Time
	for _, c := range u.contrib[window] {
		if c.x <= 0 || !c.resetAt.After(now) {
			continue
		}
		if next == nil || c.resetAt.Before(*next) {
			t := c.resetAt
			next = &t
		}
	}
	return next
}

func remainingX(quota *float64, used float64) *float64 {
	if quota == nil {
		return nil
	}
	r := math.Max(0, *quota-used)
	return &r
}

// --- admin group report ------------------------------------------------------

// UsageShareReportAccount shows one group account's current windows in x.
type UsageShareReportAccount struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Type        string     `json:"type"`
	Tier        *string    `json:"tier"`
	DetectedAt  *time.Time `json:"detected_at"`
	DetectError *string    `json:"detect_error"`
	Weight5h    float64    `json:"weight_5h"`
	Weight7d    float64    `json:"weight_7d"`
	Override5h  *float64   `json:"weight_5h_override"`
	Override7d  *float64   `json:"weight_7d_override"`
	Util5hPct   *float64   `json:"util_5h_pct"`
	Reset5h     *time.Time `json:"reset_5h"`
	Util7dPct   *float64   `json:"util_7d_pct"`
	Reset7d     *time.Time `json:"reset_7d"`
	// Usage in x by subscription of this group, by other sub2api usage (other
	// groups, users without a subscription) and from outside sub2api.
	BySubscription5hX map[int64]float64 `json:"by_subscription_5h_x"`
	BySubscription7dX map[int64]float64 `json:"by_subscription_7d_x"`
	Other5hX          float64           `json:"other_5h_x"`
	Other7dX          float64           `json:"other_7d_x"`
	Outside5hX        float64           `json:"outside_5h_x"`
	Outside7dX        float64           `json:"outside_7d_x"`
}

// UsageShareReport is the admin view of a group.
type UsageShareReport struct {
	GroupID       int64                     `json:"group_id"`
	Default       *UsageQuota               `json:"default"`
	Settings      UsageQuotaSettings        `json:"settings"`
	Capacity5hX   float64                   `json:"capacity_5h_x"`
	Capacity7dX   float64                   `json:"capacity_7d_x"`
	Allocated5hX  float64                   `json:"allocated_5h_x"`
	Allocated7dX  float64                   `json:"allocated_7d_x"`
	Unlimited5h   int                       `json:"unlimited_5h"`
	Unlimited7d   int                       `json:"unlimited_7d"`
	Accounts      []UsageShareReportAccount `json:"accounts"`
	Subscriptions []UsageShareStatus        `json:"subscriptions"`
}

func (e *usageShareEngine) report(ctx context.Context, groupID int64) (*UsageShareReport, error) {
	def, err := e.store.GetGroupQuotaDefault(ctx, groupID)
	if err != nil {
		return nil, err
	}
	snap := e.snapshot.Load()
	if snap == nil {
		if err := e.rebuild(ctx); err != nil {
			return nil, err
		}
		snap = e.snapshot.Load()
	}
	e.mu.Lock()
	types := make(map[int64]string, len(e.types))
	for k, v := range e.types {
		types[k] = v
	}
	e.mu.Unlock()

	now := e.now()
	rep := &UsageShareReport{
		GroupID: groupID, Default: def, Settings: snap.settings,
		Capacity5hX: snap.capacity(groupID, UsageWindow5h), Capacity7dX: snap.capacity(groupID, UsageWindow7d),
		Accounts: []UsageShareReportAccount{}, Subscriptions: []UsageShareStatus{},
	}
	subIDByUser := map[int64]int64{}
	for _, v := range snap.subs {
		if v.sub.GroupID != groupID {
			continue
		}
		subIDByUser[v.sub.UserID] = v.sub.ID
		rep.Subscriptions = append(rep.Subscriptions, snap.subStatus(v, now))
		if q := v.sub.Quota.Quota5hX; q != nil {
			rep.Allocated5hX += *q
		} else {
			rep.Unlimited5h++
		}
		if q := v.sub.Quota.Quota7dX; q != nil {
			rep.Allocated7dX += *q
		} else {
			rep.Unlimited7d++
		}
	}
	sort.SliceStable(rep.Subscriptions, func(i, j int) bool { return rep.Subscriptions[i].Used7dX > rep.Subscriptions[j].Used7dX })

	for _, accID := range snap.groupAccounts[groupID] {
		acc := snap.account(accID)
		ra := UsageShareReportAccount{
			ID: accID, Name: acc.name, Type: types[accID],
			Tier: acc.cap.DetectedTier, DetectedAt: acc.cap.DetectedAt, DetectError: acc.cap.DetectError,
			Weight5h: acc.weight[UsageWindow5h], Weight7d: acc.weight[UsageWindow7d],
			Override5h: acc.cap.Weight5h, Override7d: acc.cap.Weight7d,
			BySubscription5hX: map[int64]float64{}, BySubscription7dX: map[int64]float64{},
		}
		for _, w := range usageWindowKinds {
			reset, ok := acc.reset[w]
			if !ok {
				continue
			}
			util, r := 100*acc.util[w], reset
			if w == UsageWindow5h {
				ra.Util5hPct, ra.Reset5h = &util, &r
			} else {
				ra.Util7dPct, ra.Reset7d = &util, &r
			}
			for key, frac := range snap.current[accID][w] {
				x := frac * acc.weight[w]
				subID, mine := subIDByUser[key.userID]
				switch {
				case key.userID == 0 && w == UsageWindow5h:
					ra.Outside5hX += x
				case key.userID == 0:
					ra.Outside7dX += x
				case mine && key.groupID == groupID && w == UsageWindow5h:
					ra.BySubscription5hX[subID] += x
				case mine && key.groupID == groupID:
					ra.BySubscription7dX[subID] += x
				case w == UsageWindow5h:
					ra.Other5hX += x
				default:
					ra.Other7dX += x
				}
			}
		}
		rep.Accounts = append(rep.Accounts, ra)
	}
	return rep, nil
}
