package service

// [fork] Usage share status for end users (dashboard, API-key endpoint used by
// the Claude Code status line) and the admin Users page.

import (
	"context"
	"math"
	"sort"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// UsageShareKeyGroup links a user to a group they hold an active API key in.
type UsageShareKeyGroup struct {
	UserID    int64
	GroupID   int64
	GroupName string
}

// UsageShareKeyGroupStore is implemented by the account repository.
type UsageShareKeyGroupStore interface {
	// ListUsageShareKeyGroups returns (user, group) pairs from non-deleted API keys;
	// userID nil = all users.
	ListUsageShareKeyGroups(ctx context.Context, userID *int64) ([]UsageShareKeyGroup, error)
}

// UsageShareStatus is one user's consumption of one group's account pool.
type UsageShareStatus struct {
	UserID         int64      `json:"user_id"`
	GroupID        int64      `json:"group_id"`
	GroupName      string     `json:"group_name"`
	Enforced       bool       `json:"enforced"`
	Accounts       int        `json:"accounts"`
	Used5hPct      float64    `json:"used_5h_pct"`
	Used7dPct      float64    `json:"used_7d_pct"`
	Limit5hPct     *float64   `json:"limit_5h_pct"`
	Limit7dPct     *float64   `json:"limit_7d_pct"`
	Remaining5hPct *float64   `json:"remaining_5h_pct"`
	Remaining7dPct *float64   `json:"remaining_7d_pct"`
	Blocked        bool       `json:"blocked"`
	FreesAt        *time.Time `json:"frees_at,omitempty"`
}

var errUsageShareUnavailable = infraerrors.ServiceUnavailable("USAGE_SHARE_UNAVAILABLE", "usage share is not available")

// ForkUsageShareStatuses returns statuses for the given user (nil = all users),
// one per group the user has an API key in.
func ForkUsageShareStatuses(ctx context.Context, userID *int64) ([]UsageShareStatus, error) {
	e := activeUsageShare.Load()
	if e == nil {
		return nil, errUsageShareUnavailable
	}
	kgStore, ok := e.store.(UsageShareKeyGroupStore)
	if !ok {
		return nil, errUsageShareUnavailable
	}
	pairs, err := kgStore.ListUsageShareKeyGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	return e.statuses(ctx, pairs)
}

// ForkUsageShareStatusForGroup returns the status of one user in one group.
func ForkUsageShareStatusForGroup(ctx context.Context, userID, groupID int64, groupName string) (*UsageShareStatus, error) {
	e := activeUsageShare.Load()
	if e == nil {
		return nil, errUsageShareUnavailable
	}
	out, err := e.statuses(ctx, []UsageShareKeyGroup{{UserID: userID, GroupID: groupID, GroupName: groupName}})
	if err != nil {
		return nil, err
	}
	return &out[0], nil
}

func (e *usageShareEngine) statuses(ctx context.Context, pairs []UsageShareKeyGroup) ([]UsageShareStatus, error) {
	if len(pairs) == 0 {
		return []UsageShareStatus{}, nil
	}
	all, err := e.store.ListUsageShareSettings(ctx)
	if err != nil {
		return nil, err
	}
	byGroup := make(map[int64]UsageShareSettings, len(all))
	for _, s := range all {
		byGroup[s.GroupID] = s
	}
	settings := make([]UsageShareSettings, 0)
	seen := map[int64]bool{}
	for _, p := range pairs {
		if seen[p.GroupID] {
			continue
		}
		seen[p.GroupID] = true
		s, ok := byGroup[p.GroupID]
		if !ok {
			s = defaultUsageShareSettings(p.GroupID)
		}
		settings = append(settings, s)
	}

	e.mu.Lock()
	snap, err := e.buildSnapshot(ctx, settings)
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	now := e.now()
	out := make([]UsageShareStatus, 0, len(pairs))
	for _, p := range pairs {
		g := snap.groups[p.GroupID]
		st := UsageShareStatus{UserID: p.UserID, GroupID: p.GroupID, GroupName: p.GroupName}
		if g == nil {
			out = append(out, st)
			continue
		}
		st.Enforced = g.settings.Enabled
		st.Accounts = len(g.accountIDs)
		if u := g.users[p.UserID]; u != nil {
			st.Used5hPct, st.Used7dPct = u.pct[UsageWindow5h], u.pct[UsageWindow7d]
		}
		st.Limit5hPct = g.settings.limitFor(p.UserID, UsageWindow5h)
		st.Limit7dPct = g.settings.limitFor(p.UserID, UsageWindow7d)
		st.Remaining5hPct = remainingPct(st.Limit5hPct, st.Used5hPct)
		st.Remaining7dPct = remainingPct(st.Limit7dPct, st.Used7dPct)
		if over := g.exceeded(p.UserID); len(over) > 0 {
			if st.Enforced {
				st.Blocked = true
				for _, accID := range g.accountIDs {
					if g.accountHasSpare(snap.accounts[accID], over, now) {
						st.Blocked = false
						break
					}
				}
			}
			var until time.Time
			for _, w := range over {
				if t := g.freesAt(p.UserID, w, now); t.After(until) {
					until = t
				}
			}
			if until.After(now) {
				st.FreesAt = &until
			}
		}
		out = append(out, st)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].UserID != out[j].UserID {
			return out[i].UserID < out[j].UserID
		}
		return out[i].GroupID < out[j].GroupID
	})
	return out, nil
}

func remainingPct(limit *float64, used float64) *float64 {
	if limit == nil {
		return nil
	}
	r := math.Max(0, *limit-used)
	return &r
}
