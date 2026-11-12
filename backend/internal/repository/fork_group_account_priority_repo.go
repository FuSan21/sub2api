package repository

// [fork] Per-group account priority overrides (fork_group_account_priorities).
// Methods hang off *accountRepository and are discovered via type assertion
// (service.GroupAccountPriorityStore) so no upstream interface has to change.

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

var _ service.GroupAccountPriorityStore = (*accountRepository)(nil)

func (r *accountRepository) ListGroupAccountPriorityOverrides(ctx context.Context, groupID int64) (map[int64]int, error) {
	if r.sql == nil {
		return nil, errors.New("sql executor unavailable")
	}
	rows, err := r.sql.QueryContext(ctx,
		`SELECT account_id, priority FROM fork_group_account_priorities WHERE group_id = $1`, groupID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int64]int)
	for rows.Next() {
		var accountID int64
		var priority int
		if err := rows.Scan(&accountID, &priority); err != nil {
			return nil, err
		}
		out[accountID] = priority
	}
	return out, rows.Err()
}

func (r *accountRepository) ListGroupAccountPriorities(ctx context.Context, groupID int64) ([]service.GroupAccountPriority, error) {
	if r.sql == nil {
		return nil, errors.New("sql executor unavailable")
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT a.id, a.name, a.platform, a.type, a.status, a.schedulable, a.priority, o.priority
		FROM account_groups ag
		JOIN accounts a ON a.id = ag.account_id AND a.deleted_at IS NULL
		LEFT JOIN fork_group_account_priorities o ON o.group_id = ag.group_id AND o.account_id = a.id
		WHERE ag.group_id = $1
		ORDER BY COALESCE(o.priority, a.priority) ASC, a.id ASC
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.GroupAccountPriority, 0)
	for rows.Next() {
		var item service.GroupAccountPriority
		var override *int
		if err := rows.Scan(&item.AccountID, &item.Name, &item.Platform, &item.Type, &item.Status,
			&item.Schedulable, &item.AccountPriority, &override); err != nil {
			return nil, err
		}
		item.OverridePriority = override
		item.EffectivePriority = item.AccountPriority
		if override != nil {
			item.EffectivePriority = *override
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ReplaceGroupAccountPriorityOverrides makes the group's overrides exactly match
// the given map. Accounts not bound to the group are ignored.
func (r *accountRepository) ReplaceGroupAccountPriorityOverrides(ctx context.Context, groupID int64, overrides map[int64]int) error {
	if r.sql == nil {
		return errors.New("sql executor unavailable")
	}
	accountIDs := make([]int64, 0, len(overrides))
	priorities := make([]int64, 0, len(overrides))
	for accountID, priority := range overrides {
		accountIDs = append(accountIDs, accountID)
		priorities = append(priorities, int64(priority))
	}
	_, err := r.sql.ExecContext(ctx, `
		WITH removed AS (
			DELETE FROM fork_group_account_priorities
			WHERE group_id = $1 AND NOT (account_id = ANY($2::bigint[]))
		)
		INSERT INTO fork_group_account_priorities (group_id, account_id, priority, updated_at)
		SELECT $1, v.account_id, v.priority, NOW()
		FROM unnest($2::bigint[], $3::bigint[]) AS v(account_id, priority)
		JOIN account_groups ag ON ag.group_id = $1 AND ag.account_id = v.account_id
		ON CONFLICT (group_id, account_id) DO UPDATE
			SET priority = EXCLUDED.priority, updated_at = NOW()
	`, groupID, pq.Array(accountIDs), pq.Array(priorities))
	return err
}
