package repository

// [fork] Usage share storage (fork_usage_* tables). Discovered through
// service.UsageShareStore by type assertion; no upstream interface changes.

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

var _ service.UsageShareStore = (*accountRepository)(nil)

var errUsageShareNoSQL = errors.New("sql executor unavailable")

func (r *accountRepository) ListUsageShareAccountStates(ctx context.Context) ([]service.UsageShareAccountState, error) {
	if r.sql == nil {
		return nil, errUsageShareNoSQL
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id, name, status,
			extra->>'session_window_utilization', session_window_end,
			extra->>'passive_usage_7d_utilization', extra->>'passive_usage_7d_reset',
			extra->>'passive_usage_sampled_at'
		FROM accounts
		WHERE deleted_at IS NULL AND platform = 'anthropic' AND type IN ('oauth', 'setup-token')
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.UsageShareAccountState, 0)
	for rows.Next() {
		var (
			st                     service.UsageShareAccountState
			util5h, util7d, reset7 sql.NullString
			sampled                sql.NullString
			reset5h                sql.NullTime
		)
		if err := rows.Scan(&st.ID, &st.Name, &st.Status, &util5h, &reset5h, &util7d, &reset7, &sampled); err != nil {
			return nil, err
		}
		st.Util = map[string]*float64{
			service.UsageWindow5h: parseNullFloat(util5h),
			service.UsageWindow7d: parseNullFloat(util7d),
		}
		st.Reset = map[string]*time.Time{service.UsageWindow5h: nil, service.UsageWindow7d: nil}
		if reset5h.Valid {
			t := reset5h.Time
			st.Reset[service.UsageWindow5h] = &t
		}
		if reset7.Valid {
			if ts, err := strconv.ParseInt(reset7.String, 10, 64); err == nil && ts > 0 {
				if ts > 1e11 {
					ts /= 1000
				}
				t := time.Unix(ts, 0)
				st.Reset[service.UsageWindow7d] = &t
			}
		}
		if sampled.Valid {
			if t, err := time.Parse(time.RFC3339, sampled.String); err == nil {
				st.SampledAt = &t
			}
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func parseNullFloat(v sql.NullString) *float64 {
	if !v.Valid || v.String == "" {
		return nil
	}
	f, err := strconv.ParseFloat(v.String, 64)
	if err != nil {
		return nil
	}
	return &f
}

func (r *accountRepository) LatestUsageSamples(ctx context.Context) ([]service.UsageSample, error) {
	if r.sql == nil {
		return nil, errUsageShareNoSQL
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT DISTINCT ON (account_id, window_kind) account_id, window_kind, utilization::float8, reset_at, sampled_at
		FROM fork_usage_samples
		ORDER BY account_id, window_kind, id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.UsageSample, 0)
	for rows.Next() {
		var s service.UsageSample
		if err := rows.Scan(&s.AccountID, &s.Window, &s.Utilization, &s.ResetAt, &s.SampledAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *accountRepository) UsageCostByUser(ctx context.Context, accountID int64, from, to time.Time) ([]service.UsageUserCost, error) {
	if r.sql == nil {
		return nil, errUsageShareNoSQL
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT user_id, COALESCE(SUM(total_cost), 0)::float8, COUNT(*)
		FROM usage_logs
		WHERE account_id = $1 AND created_at > $2 AND created_at <= $3
		GROUP BY user_id
	`, accountID, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.UsageUserCost, 0)
	for rows.Next() {
		var c service.UsageUserCost
		if err := rows.Scan(&c.UserID, &c.Cost, &c.Requests); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RecordUsageSegment writes the new sample and its attributions atomically, so a
// crash can't attribute the same segment twice.
func (r *accountRepository) RecordUsageSegment(ctx context.Context, sample service.UsageSample, segmentStart time.Time, attributions []service.UsageAttribution, estimated bool) error {
	if r.sql == nil {
		return errUsageShareNoSQL
	}
	userIDs := make([]sql.NullInt64, 0, len(attributions))
	deltas := make([]float64, 0, len(attributions))
	for _, a := range attributions {
		if a.UserID != nil {
			userIDs = append(userIDs, sql.NullInt64{Int64: *a.UserID, Valid: true})
		} else {
			userIDs = append(userIDs, sql.NullInt64{})
		}
		deltas = append(deltas, a.Delta)
	}
	_, err := r.sql.ExecContext(ctx, `
		WITH sample AS (
			INSERT INTO fork_usage_samples (account_id, window_kind, utilization, reset_at, sampled_at)
			VALUES ($1, $2, $3, $4, $5)
		)
		INSERT INTO fork_usage_attributions (account_id, window_kind, reset_at, user_id, delta, segment_start, segment_end, estimated)
		SELECT $1, $2, $4, t.user_id, t.delta, $6, $5, $7
		FROM unnest($8::bigint[], $9::float8[]) AS t(user_id, delta)
	`, sample.AccountID, sample.Window, sample.Utilization, sample.ResetAt, sample.SampledAt,
		segmentStart, estimated, pq.Array(userIDs), pq.Array(deltas))
	return err
}

func (r *accountRepository) ListCurrentUsageAttributions(ctx context.Context, now time.Time) ([]service.UsageAttributionSum, error) {
	if r.sql == nil {
		return nil, errUsageShareNoSQL
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT account_id, window_kind, reset_at, user_id, SUM(delta)::float8
		FROM fork_usage_attributions
		WHERE reset_at > $1
		GROUP BY account_id, window_kind, reset_at, user_id
	`, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.UsageAttributionSum, 0)
	for rows.Next() {
		var s service.UsageAttributionSum
		var uid sql.NullInt64
		if err := rows.Scan(&s.AccountID, &s.Window, &s.ResetAt, &uid, &s.Delta); err != nil {
			return nil, err
		}
		if uid.Valid {
			v := uid.Int64
			s.UserID = &v
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *accountRepository) ListUsageShareSettings(ctx context.Context) ([]service.UsageShareSettings, error) {
	if r.sql == nil {
		return nil, errUsageShareNoSQL
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT group_id, enabled, default_7d_pct::float8, default_5h_pct::float8,
			spare_7d_min_pct::float8, spare_7d_within_hours, spare_5h_min_pct::float8, spare_5h_within_minutes
		FROM fork_usage_share_settings
	`)
	if err != nil {
		return nil, err
	}
	byGroup := map[int64]*service.UsageShareSettings{}
	out := make([]service.UsageShareSettings, 0)
	for rows.Next() {
		var s service.UsageShareSettings
		var d7, d5 sql.NullFloat64
		if err := rows.Scan(&s.GroupID, &s.Enabled, &d7, &d5, &s.Spare7dMinPct, &s.Spare7dWithinHours, &s.Spare5hMinPct, &s.Spare5hWithinMinutes); err != nil {
			_ = rows.Close()
			return nil, err
		}
		s.Default7dPct, s.Default5hPct = nullFloatPtr(d7), nullFloatPtr(d5)
		s.Limits = []service.UsageShareUserLimit{}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	for i := range out {
		byGroup[out[i].GroupID] = &out[i]
	}

	lrows, err := r.sql.QueryContext(ctx, `
		SELECT group_id, user_id, limit_7d_pct::float8, limit_5h_pct::float8 FROM fork_usage_share_limits
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lrows.Close() }()
	for lrows.Next() {
		var groupID int64
		var l service.UsageShareUserLimit
		var l7, l5 sql.NullFloat64
		if err := lrows.Scan(&groupID, &l.UserID, &l7, &l5); err != nil {
			return nil, err
		}
		l.Limit7dPct, l.Limit5hPct = nullFloatPtr(l7), nullFloatPtr(l5)
		if s := byGroup[groupID]; s != nil {
			s.Limits = append(s.Limits, l)
		}
	}
	return out, lrows.Err()
}

func nullFloatPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

func (r *accountRepository) SaveUsageShareSettings(ctx context.Context, s service.UsageShareSettings) error {
	if r.sql == nil {
		return errUsageShareNoSQL
	}
	if _, err := r.sql.ExecContext(ctx, `
		INSERT INTO fork_usage_share_settings (group_id, enabled, default_7d_pct, default_5h_pct,
			spare_7d_min_pct, spare_7d_within_hours, spare_5h_min_pct, spare_5h_within_minutes, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		ON CONFLICT (group_id) DO UPDATE SET
			enabled = EXCLUDED.enabled, default_7d_pct = EXCLUDED.default_7d_pct, default_5h_pct = EXCLUDED.default_5h_pct,
			spare_7d_min_pct = EXCLUDED.spare_7d_min_pct, spare_7d_within_hours = EXCLUDED.spare_7d_within_hours,
			spare_5h_min_pct = EXCLUDED.spare_5h_min_pct, spare_5h_within_minutes = EXCLUDED.spare_5h_within_minutes,
			updated_at = NOW()
	`, s.GroupID, s.Enabled, s.Default7dPct, s.Default5hPct, s.Spare7dMinPct, s.Spare7dWithinHours,
		s.Spare5hMinPct, s.Spare5hWithinMinutes); err != nil {
		return err
	}

	userIDs := make([]int64, 0, len(s.Limits))
	l7 := make([]sql.NullFloat64, 0, len(s.Limits))
	l5 := make([]sql.NullFloat64, 0, len(s.Limits))
	for _, l := range s.Limits {
		if l.Limit7dPct == nil && l.Limit5hPct == nil {
			continue // no override
		}
		userIDs = append(userIDs, l.UserID)
		l7 = append(l7, toNullFloat(l.Limit7dPct))
		l5 = append(l5, toNullFloat(l.Limit5hPct))
	}
	_, err := r.sql.ExecContext(ctx, `
		WITH removed AS (
			DELETE FROM fork_usage_share_limits
			WHERE group_id = $1 AND NOT (user_id = ANY($2::bigint[]))
		)
		INSERT INTO fork_usage_share_limits (group_id, user_id, limit_7d_pct, limit_5h_pct, updated_at)
		SELECT $1, t.user_id, t.l7, t.l5, NOW()
		FROM unnest($2::bigint[], $3::float8[], $4::float8[]) AS t(user_id, l7, l5)
		JOIN users u ON u.id = t.user_id AND u.deleted_at IS NULL
		ON CONFLICT (group_id, user_id) DO UPDATE
			SET limit_7d_pct = EXCLUDED.limit_7d_pct, limit_5h_pct = EXCLUDED.limit_5h_pct, updated_at = NOW()
	`, s.GroupID, pq.Array(userIDs), pq.Array(l7), pq.Array(l5))
	return err
}

func toNullFloat(v *float64) sql.NullFloat64 {
	if v == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *v, Valid: true}
}

func (r *accountRepository) ListUsageShareGroupAccounts(ctx context.Context, groupIDs []int64) (map[int64][]int64, error) {
	if r.sql == nil {
		return nil, errUsageShareNoSQL
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT ag.group_id, a.id
		FROM account_groups ag
		JOIN accounts a ON a.id = ag.account_id
		WHERE ag.group_id = ANY($1::bigint[]) AND a.deleted_at IS NULL AND a.status = 'active'
			AND a.platform = 'anthropic' AND a.type IN ('oauth', 'setup-token')
		ORDER BY ag.group_id, a.id
	`, pq.Array(groupIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int64][]int64)
	for rows.Next() {
		var groupID, accountID int64
		if err := rows.Scan(&groupID, &accountID); err != nil {
			return nil, err
		}
		out[groupID] = append(out[groupID], accountID)
	}
	return out, rows.Err()
}

func (r *accountRepository) ListUsageShareUsers(ctx context.Context, groupID int64, extraUserIDs []int64) ([]service.UsageShareUser, error) {
	if r.sql == nil {
		return nil, errUsageShareNoSQL
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT u.id, u.email, u.username
		FROM users u
		WHERE u.deleted_at IS NULL AND (
			u.id = ANY($2::bigint[])
			OR EXISTS (SELECT 1 FROM api_keys k WHERE k.user_id = u.id AND k.group_id = $1 AND k.deleted_at IS NULL)
		)
		ORDER BY u.id
	`, groupID, pq.Array(extraUserIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.UsageShareUser, 0)
	for rows.Next() {
		var u service.UsageShareUser
		if err := rows.Scan(&u.ID, &u.Email, &u.Username); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *accountRepository) PruneUsageShare(ctx context.Context, before time.Time) error {
	if r.sql == nil {
		return errUsageShareNoSQL
	}
	if _, err := r.sql.ExecContext(ctx, `DELETE FROM fork_usage_attributions WHERE segment_end < $1 AND reset_at < $1`, before); err != nil {
		return err
	}
	_, err := r.sql.ExecContext(ctx, `
		DELETE FROM fork_usage_samples s
		WHERE s.sampled_at < $1
			AND s.id <> (SELECT MAX(x.id) FROM fork_usage_samples x WHERE x.account_id = s.account_id AND x.window_kind = s.window_kind)
	`, before)
	return err
}

var _ service.UsageShareKeyGroupStore = (*accountRepository)(nil)

func (r *accountRepository) ListUsageShareKeyGroups(ctx context.Context, userID *int64) ([]service.UsageShareKeyGroup, error) {
	if r.sql == nil {
		return nil, errUsageShareNoSQL
	}
	var uid sql.NullInt64
	if userID != nil {
		uid = sql.NullInt64{Int64: *userID, Valid: true}
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT DISTINCT k.user_id, g.id, g.name
		FROM api_keys k
		JOIN groups g ON g.id = k.group_id AND g.deleted_at IS NULL
		JOIN users u ON u.id = k.user_id AND u.deleted_at IS NULL
		WHERE k.deleted_at IS NULL AND ($1::bigint IS NULL OR k.user_id = $1)
		ORDER BY k.user_id, g.id
	`, uid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.UsageShareKeyGroup, 0)
	for rows.Next() {
		var p service.UsageShareKeyGroup
		if err := rows.Scan(&p.UserID, &p.GroupID, &p.GroupName); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
