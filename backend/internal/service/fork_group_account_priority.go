package service

// [fork] Per-group account priority overrides.
//
// Upstream schedules accounts by accounts.priority only, so an account has the
// same rank in every group. The fork lets each group override that rank per
// account (table fork_group_account_priorities).
//
// Upstream simple mode also ignores groups when scheduling (every key can use
// every account of the platform). The fork restricts grouped keys to the
// accounts bound to their group in simple mode too; ungrouped keys keep the
// upstream behaviour.
//
// Both are applied to the account copies returned by
// SchedulerSnapshotService.ListSchedulableAccounts, so every platform
// scheduler sees them without further changes.

import (
	"context"
	"log/slog"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const groupAccountPriorityCacheTTL = 10 * time.Second

// GroupAccountPriorityStore is implemented by the account repository.
type GroupAccountPriorityStore interface {
	ListGroupAccountPriorityOverrides(ctx context.Context, groupID int64) (map[int64]int, error)
	ListGroupAccountPriorities(ctx context.Context, groupID int64) ([]GroupAccountPriority, error)
	ReplaceGroupAccountPriorityOverrides(ctx context.Context, groupID int64, overrides map[int64]int) error
}

// GroupAccountPriority describes one account bound to a group.
type GroupAccountPriority struct {
	AccountID         int64  `json:"account_id"`
	Name              string `json:"name"`
	Platform          string `json:"platform"`
	Type              string `json:"type"`
	Status            string `json:"status"`
	Schedulable       bool   `json:"schedulable"`
	AccountPriority   int    `json:"account_priority"`
	OverridePriority  *int   `json:"override_priority"`
	EffectivePriority int    `json:"effective_priority"`
}

// GroupAccountPriorityUpdate sets (Priority != nil) or clears an override.
type GroupAccountPriorityUpdate struct {
	AccountID int64 `json:"account_id"`
	Priority  *int  `json:"priority"`
}

type groupAccountPriorityCacheEntry struct {
	overrides map[int64]int
	loadedAt  time.Time
}

type groupAccountPriorityCache struct {
	mu      sync.RWMutex
	entries map[int64]groupAccountPriorityCacheEntry
}

var groupAccountPriorities = &groupAccountPriorityCache{entries: make(map[int64]groupAccountPriorityCacheEntry)}

func (c *groupAccountPriorityCache) get(ctx context.Context, store GroupAccountPriorityStore, groupID int64) map[int64]int {
	c.mu.RLock()
	entry, ok := c.entries[groupID]
	c.mu.RUnlock()
	if ok && time.Since(entry.loadedAt) < groupAccountPriorityCacheTTL {
		return entry.overrides
	}

	overrides, err := store.ListGroupAccountPriorityOverrides(ctx, groupID)
	if err != nil {
		slog.Warn("fork_group_account_priority_load_failed", "group_id", groupID, "error", err)
		if ok {
			return entry.overrides
		}
		return nil
	}
	c.mu.Lock()
	c.entries[groupID] = groupAccountPriorityCacheEntry{overrides: overrides, loadedAt: time.Now()}
	c.mu.Unlock()
	return overrides
}

func (c *groupAccountPriorityCache) invalidate(groupID int64) {
	c.mu.Lock()
	delete(c.entries, groupID)
	c.mu.Unlock()
}

// applyForkGroupScheduling scopes accounts to the group (simple mode) and
// applies the group's priority overrides. accounts must be a private copy.
func (s *SchedulerSnapshotService) applyForkGroupScheduling(ctx context.Context, groupID *int64, accounts []Account) []Account {
	if groupID == nil || *groupID <= 0 || len(accounts) == 0 {
		return accounts
	}
	if s.isRunModeSimple() {
		scoped := accounts[:0]
		for _, account := range accounts {
			if accountInGroup(&account, *groupID) {
				scoped = append(scoped, account)
			}
		}
		accounts = scoped
	}
	store, ok := s.accountRepo.(GroupAccountPriorityStore)
	if !ok || len(accounts) == 0 {
		return accounts
	}
	overrides := groupAccountPriorities.get(ctx, store, *groupID)
	for i := range accounts {
		if priority, ok := overrides[accounts[i].ID]; ok {
			accounts[i].Priority = priority
		}
	}
	return accounts
}

func accountInGroup(account *Account, groupID int64) bool {
	for _, ag := range account.AccountGroups {
		if ag.GroupID == groupID {
			return true
		}
	}
	for _, id := range account.GroupIDs {
		if id == groupID {
			return true
		}
	}
	return false
}

func (s *adminServiceImpl) groupAccountPriorityStore() (GroupAccountPriorityStore, error) {
	store, ok := s.accountRepo.(GroupAccountPriorityStore)
	if !ok {
		return nil, infraerrors.InternalServer("GROUP_ACCOUNT_PRIORITY_UNSUPPORTED", "group account priority store unavailable")
	}
	return store, nil
}

func (s *adminServiceImpl) GetGroupAccountPriorities(ctx context.Context, groupID int64) ([]GroupAccountPriority, error) {
	if _, err := s.groupRepo.GetByIDLite(ctx, groupID); err != nil {
		return nil, err
	}
	store, err := s.groupAccountPriorityStore()
	if err != nil {
		return nil, err
	}
	return store.ListGroupAccountPriorities(ctx, groupID)
}

// SetGroupAccountPriorities replaces all overrides of the group: listed accounts
// with a non-nil priority get an override, everything else falls back to the
// account's own priority.
func (s *adminServiceImpl) SetGroupAccountPriorities(ctx context.Context, groupID int64, updates []GroupAccountPriorityUpdate) ([]GroupAccountPriority, error) {
	if _, err := s.groupRepo.GetByIDLite(ctx, groupID); err != nil {
		return nil, err
	}
	store, err := s.groupAccountPriorityStore()
	if err != nil {
		return nil, err
	}
	overrides := make(map[int64]int, len(updates))
	for _, u := range updates {
		if u.AccountID <= 0 {
			return nil, infraerrors.BadRequest("INVALID_ACCOUNT_ID", "account_id must be positive")
		}
		if u.Priority == nil {
			continue
		}
		if *u.Priority < 0 {
			return nil, infraerrors.BadRequest("INVALID_PRIORITY", "priority must be >= 0")
		}
		overrides[u.AccountID] = *u.Priority
	}
	if err := store.ReplaceGroupAccountPriorityOverrides(ctx, groupID, overrides); err != nil {
		return nil, err
	}
	groupAccountPriorities.invalidate(groupID)
	return store.ListGroupAccountPriorities(ctx, groupID)
}
