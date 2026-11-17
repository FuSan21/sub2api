package repository

// [fork] Usage share storage (fork_usage_*, fork_*_quota*, fork_account_capacity
// tables). Discovered through service.UsageShareStore by type assertion; no
// upstream interface changes.

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
		SELECT id, name, type, status,
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
		if err := rows.Scan(&st.ID, &st.Name, &st.Type, &st.Status, &util5h, &reset5h, &util7d, &reset7, &sampled); err != nil {
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

func nullFloatPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

func forkNullStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func forkNullInt64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	i := v.Int64
	return &i
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
		SELECT user_id, group_id, COALESCE(SUM(total_cost), 0)::float8, COUNT(*)
		FROM usage_logs
		WHERE account_id = $1 AND created_at > $2 AND created_at <= $3
		GROUP BY user_id, group_id
	`, accountID, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.UsageUserCost, 0)
	for rows.Next() {
		var c service.UsageUserCost
		var gid sql.NullInt64
		if err := rows.Scan(&c.UserID, &gid, &c.Cost, &c.Requests); err != nil {
			return nil, err
		}
		c.GroupID = forkNullInt64Ptr(gid)
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
	groupIDs := make([]sql.NullInt64, 0, len(attributions))
	deltas := make([]float64, 0, len(attributions))
	for _, a := range attributions {
		userIDs = append(userIDs, forkToNullInt64(a.UserID))
		groupIDs = append(groupIDs, forkToNullInt64(a.GroupID))
		deltas = append(deltas, a.Delta)
	}
	_, err := r.sql.ExecContext(ctx, `
		WITH sample AS (
			INSERT INTO fork_usage_samples (account_id, window_kind, utilization, reset_at, sampled_at)
			VALUES ($1, $2, $3, $4, $5)
		)
		INSERT INTO fork_usage_attributions (account_id, window_kind, reset_at, user_id, group_id, delta, segment_start, segment_end, estimated)
		SELECT $1, $2, $4, t.user_id, t.group_id, t.delta, $6, $5, $7
		FROM unnest($8::bigint[], $9::bigint[], $10::float8[]) AS t(user_id, group_id, delta)
	`, sample.AccountID, sample.Window, sample.Utilization, sample.ResetAt, sample.SampledAt,
		segmentStart, estimated, pq.Array(userIDs), pq.Array(groupIDs), pq.Array(deltas))
	return err
}

func forkToNullInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func toNullFloat(v *float64) sql.NullFloat64 {
	if v == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *v, Valid: true}
}

func (r *accountRepository) ListCurrentUsageAttributions(ctx context.Context, now time.Time) ([]service.UsageAttributionSum, error) {
	if r.sql == nil {
		return nil, errUsageShareNoSQL
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT account_id, window_kind, reset_at, user_id, group_id, SUM(delta)::float8
		FROM fork_usage_attributions
		WHERE reset_at > $1
		GROUP BY account_id, window_kind, reset_at, user_id, group_id
	`, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.UsageAttributionSum, 0)
	for rows.Next() {
		var s service.UsageAttributionSum
		var uid, gid sql.NullInt64
		if err := rows.Scan(&s.AccountID, &s.Window, &s.ResetAt, &uid, &gid, &s.Delta); err != nil {
			return nil, err
		}
		s.UserID, s.GroupID = forkNullInt64Ptr(uid), forkNullInt64Ptr(gid)
		out = append(out, s)
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

// --- settings, capacities, quotas ----------------------------------------------

func (r *accountRepository) GetUsageQuotaSettings(ctx context.Context) (service.UsageQuotaSettings, error) {
	var s service.UsageQuotaSettings
	if r.sql == nil {
		return s, errUsageShareNoSQL
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT spare_7d_min_pct::float8, spare_7d_within_hours, spare_5h_min_pct::float8, spare_5h_within_minutes
		FROM fork_quota_settings WHERE id = 1
	`)
	if err != nil {
		return s, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return s, err
		}
		return service.UsageQuotaSettings{Spare7dMinPct: 40, Spare7dWithinHours: 24, Spare5hMinPct: 40, Spare5hWithinMinutes: 60}, nil
	}
	if err := rows.Scan(&s.Spare7dMinPct, &s.Spare7dWithinHours, &s.Spare5hMinPct, &s.Spare5hWithinMinutes); err != nil {
		return s, err
	}
	return s, rows.Err()
}

func (r *accountRepository) SaveUsageQuotaSettings(ctx context.Context, s service.UsageQuotaSettings) error {
	if r.sql == nil {
		return errUsageShareNoSQL
	}
	_, err := r.sql.ExecContext(ctx, `
		INSERT INTO fork_quota_settings (id, spare_7d_min_pct, spare_7d_within_hours, spare_5h_min_pct, spare_5h_within_minutes, updated_at)
		VALUES (1, $1, $2, $3, $4, NOW())
		ON CONFLICT (id) DO UPDATE SET
			spare_7d_min_pct = EXCLUDED.spare_7d_min_pct, spare_7d_within_hours = EXCLUDED.spare_7d_within_hours,
			spare_5h_min_pct = EXCLUDED.spare_5h_min_pct, spare_5h_within_minutes = EXCLUDED.spare_5h_within_minutes,
			updated_at = NOW()
	`, s.Spare7dMinPct, s.Spare7dWithinHours, s.Spare5hMinPct, s.Spare5hWithinMinutes)
	return err
}

func (r *accountRepository) ListAccountCapacities(ctx context.Context) (map[int64]service.UsageAccountCapacity, error) {
	if r.sql == nil {
		return nil, errUsageShareNoSQL
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT account_id, detected_tier, detected_at, detect_error, weight_5h::float8, weight_7d::float8
		FROM fork_account_capacity
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int64]service.UsageAccountCapacity)
	for rows.Next() {
		var (
			c          service.UsageAccountCapacity
			tier, derr sql.NullString
			at         sql.NullTime
			w5, w7     sql.NullFloat64
		)
		if err := rows.Scan(&c.AccountID, &tier, &at, &derr, &w5, &w7); err != nil {
			return nil, err
		}
		c.DetectedTier, c.DetectError = forkNullStringPtr(tier), forkNullStringPtr(derr)
		if at.Valid {
			t := at.Time
			c.DetectedAt = &t
		}
		c.Weight5h, c.Weight7d = nullFloatPtr(w5), nullFloatPtr(w7)
		out[c.AccountID] = c
	}
	return out, rows.Err()
}

func (r *accountRepository) SaveAccountWeights(ctx context.Context, accountID int64, weight5h, weight7d *float64, redetect bool) error {
	if r.sql == nil {
		return errUsageShareNoSQL
	}
	res, err := r.sql.ExecContext(ctx, `
		INSERT INTO fork_account_capacity (account_id, weight_5h, weight_7d, updated_at)
		SELECT a.id, $2, $3, NOW() FROM accounts a WHERE a.id = $1 AND a.deleted_at IS NULL
		ON CONFLICT (account_id) DO UPDATE SET
			weight_5h = EXCLUDED.weight_5h, weight_7d = EXCLUDED.weight_7d,
			detected_at = CASE WHEN $4 THEN NULL ELSE fork_account_capacity.detected_at END,
			updated_at = NOW()
	`, accountID, toNullFloat(weight5h), toNullFloat(weight7d), redetect)
	return forkRequireAffected(res, err)
}

func (r *accountRepository) SaveDetectedTier(ctx context.Context, accountID int64, tier, detectErr *string, at time.Time) error {
	if r.sql == nil {
		return errUsageShareNoSQL
	}
	_, err := r.sql.ExecContext(ctx, `
		INSERT INTO fork_account_capacity (account_id, detected_tier, detected_at, detect_error, updated_at)
		SELECT a.id, $2, $3, $4, NOW() FROM accounts a WHERE a.id = $1
		ON CONFLICT (account_id) DO UPDATE SET
			detected_tier = EXCLUDED.detected_tier, detected_at = EXCLUDED.detected_at,
			detect_error = EXCLUDED.detect_error, updated_at = NOW()
	`, accountID, tier, at, detectErr)
	return err
}

func forkRequireAffected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return service.ErrUsageQuotaTargetNotFound
	}
	return nil
}

// ListUsageShareGroupAccounts returns each group's active Anthropic OAuth accounts.
func (r *accountRepository) ListUsageShareGroupAccounts(ctx context.Context) (map[int64][]int64, error) {
	if r.sql == nil {
		return nil, errUsageShareNoSQL
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT ag.group_id, a.id
		FROM account_groups ag
		JOIN accounts a ON a.id = ag.account_id
		WHERE a.deleted_at IS NULL AND a.status = 'active'
			AND a.platform = 'anthropic' AND a.type IN ('oauth', 'setup-token')
		ORDER BY ag.group_id, a.id
	`)
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

// ListQuotaSubscriptions returns live subscriptions with their effective quota:
// the subscription's own row, else the group default, else none.
func (r *accountRepository) ListQuotaSubscriptions(ctx context.Context) ([]service.UsageQuotaSubscription, error) {
	if r.sql == nil {
		return nil, errUsageShareNoSQL
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT s.id, s.user_id, s.group_id, g.name, u.email, u.username, s.expires_at,
			CASE WHEN q.subscription_id IS NOT NULL THEN q.quota_5h_x ELSE d.quota_5h_x END::float8,
			CASE WHEN q.subscription_id IS NOT NULL THEN q.quota_7d_x ELSE d.quota_7d_x END::float8,
			CASE WHEN q.subscription_id IS NOT NULL THEN q.source
				WHEN d.group_id IS NOT NULL THEN 'group' ELSE 'none' END,
			q.plan_id
		FROM user_subscriptions s
		JOIN groups g ON g.id = s.group_id AND g.deleted_at IS NULL
		JOIN users u ON u.id = s.user_id AND u.deleted_at IS NULL
		LEFT JOIN fork_subscription_quotas q ON q.subscription_id = s.id
		LEFT JOIN fork_group_quota_defaults d ON d.group_id = s.group_id
		WHERE s.deleted_at IS NULL AND s.status = 'active' AND s.expires_at > NOW()
		ORDER BY s.id
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.UsageQuotaSubscription, 0)
	for rows.Next() {
		var (
			s      service.UsageQuotaSubscription
			q5, q7 sql.NullFloat64
			planID sql.NullInt64
		)
		if err := rows.Scan(&s.ID, &s.UserID, &s.GroupID, &s.GroupName, &s.Email, &s.Username, &s.ExpiresAt,
			&q5, &q7, &s.Source, &planID); err != nil {
			return nil, err
		}
		s.Quota = service.UsageQuota{Quota5hX: nullFloatPtr(q5), Quota7dX: nullFloatPtr(q7)}
		s.PlanID = forkNullInt64Ptr(planID)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *accountRepository) GetGroupQuotaDefault(ctx context.Context, groupID int64) (*service.UsageQuota, error) {
	if r.sql == nil {
		return nil, errUsageShareNoSQL
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT quota_5h_x::float8, quota_7d_x::float8 FROM fork_group_quota_defaults WHERE group_id = $1
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var q5, q7 sql.NullFloat64
	if err := rows.Scan(&q5, &q7); err != nil {
		return nil, err
	}
	return &service.UsageQuota{Quota5hX: nullFloatPtr(q5), Quota7dX: nullFloatPtr(q7)}, rows.Err()
}

func (r *accountRepository) SaveGroupQuotaDefault(ctx context.Context, groupID int64, q *service.UsageQuota) error {
	if r.sql == nil {
		return errUsageShareNoSQL
	}
	if q == nil {
		_, err := r.sql.ExecContext(ctx, `DELETE FROM fork_group_quota_defaults WHERE group_id = $1`, groupID)
		return err
	}
	_, err := r.sql.ExecContext(ctx, `
		INSERT INTO fork_group_quota_defaults (group_id, quota_5h_x, quota_7d_x, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (group_id) DO UPDATE SET
			quota_5h_x = EXCLUDED.quota_5h_x, quota_7d_x = EXCLUDED.quota_7d_x, updated_at = NOW()
	`, groupID, toNullFloat(q.Quota5hX), toNullFloat(q.Quota7dX))
	return err
}

// SaveSubscriptionQuota sets an admin quota on the subscription (nil = remove it,
// falling back to the group default). order_id is kept so a retried fulfillment
// of an older order can't overwrite the admin's edit.
func (r *accountRepository) SaveSubscriptionQuota(ctx context.Context, subscriptionID int64, q *service.UsageQuota) error {
	if r.sql == nil {
		return errUsageShareNoSQL
	}
	if q == nil {
		// Clearing a subscription without a row is a no-op.
		_, err := r.sql.ExecContext(ctx, `DELETE FROM fork_subscription_quotas WHERE subscription_id = $1`, subscriptionID)
		return err
	}
	res, err := r.sql.ExecContext(ctx, `
		INSERT INTO fork_subscription_quotas (subscription_id, quota_5h_x, quota_7d_x, source, updated_at)
		SELECT s.id, $2, $3, 'admin', NOW() FROM user_subscriptions s WHERE s.id = $1 AND s.deleted_at IS NULL
		ON CONFLICT (subscription_id) DO UPDATE SET
			quota_5h_x = EXCLUDED.quota_5h_x, quota_7d_x = EXCLUDED.quota_7d_x,
			source = 'admin', updated_at = NOW()
	`, subscriptionID, toNullFloat(q.Quota5hX), toNullFloat(q.Quota7dX))
	return forkRequireAffected(res, err)
}

func (r *accountRepository) ListPlanQuotas(ctx context.Context) (map[int64]service.UsageQuota, error) {
	if r.sql == nil {
		return nil, errUsageShareNoSQL
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT plan_id, quota_5h_x::float8, quota_7d_x::float8 FROM fork_plan_quotas`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int64]service.UsageQuota)
	for rows.Next() {
		var id int64
		var q5, q7 sql.NullFloat64
		if err := rows.Scan(&id, &q5, &q7); err != nil {
			return nil, err
		}
		out[id] = service.UsageQuota{Quota5hX: nullFloatPtr(q5), Quota7dX: nullFloatPtr(q7)}
	}
	return out, rows.Err()
}

func (r *accountRepository) SavePlanQuota(ctx context.Context, planID int64, q *service.UsageQuota) error {
	if r.sql == nil {
		return errUsageShareNoSQL
	}
	if q == nil {
		_, err := r.sql.ExecContext(ctx, `DELETE FROM fork_plan_quotas WHERE plan_id = $1`, planID)
		return err
	}
	res, err := r.sql.ExecContext(ctx, `
		INSERT INTO fork_plan_quotas (plan_id, quota_5h_x, quota_7d_x, updated_at)
		SELECT p.id, $2, $3, NOW() FROM subscription_plans p WHERE p.id = $1
		ON CONFLICT (plan_id) DO UPDATE SET
			quota_5h_x = EXCLUDED.quota_5h_x, quota_7d_x = EXCLUDED.quota_7d_x, updated_at = NOW()
	`, planID, toNullFloat(q.Quota5hX), toNullFloat(q.Quota7dX))
	return forkRequireAffected(res, err)
}

// ApplyPlanQuota copies the plan's quota onto the user's live subscription in the
// group. A newer order wins; the same or an older order never overwrites.
func (r *accountRepository) ApplyPlanQuota(ctx context.Context, planID, orderID, userID, groupID int64) (bool, error) {
	if r.sql == nil {
		return false, errUsageShareNoSQL
	}
	res, err := r.sql.ExecContext(ctx, `
		INSERT INTO fork_subscription_quotas (subscription_id, quota_5h_x, quota_7d_x, source, plan_id, order_id, updated_at)
		SELECT s.id, p.quota_5h_x, p.quota_7d_x, 'plan', p.plan_id, $2, NOW()
		FROM fork_plan_quotas p
		JOIN user_subscriptions s ON s.user_id = $3 AND s.group_id = $4 AND s.deleted_at IS NULL
		WHERE p.plan_id = $1
		ON CONFLICT (subscription_id) DO UPDATE SET
			quota_5h_x = EXCLUDED.quota_5h_x, quota_7d_x = EXCLUDED.quota_7d_x, source = 'plan',
			plan_id = EXCLUDED.plan_id, order_id = EXCLUDED.order_id, updated_at = NOW()
		WHERE fork_subscription_quotas.order_id IS NULL OR fork_subscription_quotas.order_id < EXCLUDED.order_id
	`, planID, orderID, userID, groupID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
