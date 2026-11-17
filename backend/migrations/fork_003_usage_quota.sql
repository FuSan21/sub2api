-- [fork] Usage share v2: per-subscription quotas measured in Pro-account equivalents.
--
-- 1x = one full 5h (or 7d) window of a $20 Claude Pro account; a Max 5x account is
-- worth 5x, a Max 20x account 20x. Quotas live on subscriptions (copied from the
-- plan on purchase, editable by admins) with a per-group default as fallback.
-- Replaces the per-group %-of-pool limits of fork_002 (its data is dropped).
-- Upstream ignores these tables, so switching back to the upstream image is safe.

DROP TABLE IF EXISTS fork_usage_share_limits;
DROP TABLE IF EXISTS fork_usage_share_settings;
DROP TABLE IF EXISTS fork_usage_attributions;
DROP TABLE IF EXISTS fork_usage_samples;

-- Last observed utilization per account window (a row is written only on change).
CREATE TABLE IF NOT EXISTS fork_usage_samples (
    id          BIGSERIAL PRIMARY KEY,
    account_id  BIGINT        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    window_kind VARCHAR(4)    NOT NULL CHECK (window_kind IN ('5h', '7d')),
    utilization NUMERIC(8,6)  NOT NULL,
    reset_at    TIMESTAMPTZ   NOT NULL,
    sampled_at  TIMESTAMPTZ   NOT NULL,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_fork_usage_samples_latest
    ON fork_usage_samples (account_id, window_kind, id DESC);

-- Each utilization rise split across the (user, group) pairs active on the account in
-- that segment. user_id NULL = usage from outside sub2api. group_id is the API key's
-- group (usage_logs.group_id); a user's live subscription in that group is charged.
-- delta is a fraction of *that account's* window (0.01 = 1%).
CREATE TABLE IF NOT EXISTS fork_usage_attributions (
    id            BIGSERIAL PRIMARY KEY,
    account_id    BIGINT        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    window_kind   VARCHAR(4)    NOT NULL CHECK (window_kind IN ('5h', '7d')),
    reset_at      TIMESTAMPTZ   NOT NULL,
    user_id       BIGINT        REFERENCES users(id) ON DELETE CASCADE,
    group_id      BIGINT,
    delta         NUMERIC(10,8) NOT NULL,
    segment_start TIMESTAMPTZ   NOT NULL,
    segment_end   TIMESTAMPTZ   NOT NULL,
    estimated     BOOLEAN       NOT NULL DEFAULT FALSE
);
CREATE INDEX IF NOT EXISTS idx_fork_usage_attributions_window
    ON fork_usage_attributions (account_id, window_kind, reset_at);
CREATE INDEX IF NOT EXISTS idx_fork_usage_attributions_segment_end
    ON fork_usage_attributions (segment_end);

-- Account size in Pro-equivalents. detected_tier comes from /api/oauth/profile
-- ('pro', 'max_5x', 'max_20x' or the raw upstream value); weight_* are admin overrides.
CREATE TABLE IF NOT EXISTS fork_account_capacity (
    account_id    BIGINT       PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    detected_tier TEXT,
    detected_at   TIMESTAMPTZ,
    detect_error  TEXT,
    weight_5h     NUMERIC(6,2),
    weight_7d     NUMERIC(6,2),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Global spare-capacity rule: a subscription over its quota may still use accounts
-- with >= min % left that reset within the given time.
CREATE TABLE IF NOT EXISTS fork_quota_settings (
    id                      SMALLINT     PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    spare_7d_min_pct        NUMERIC(6,2) NOT NULL DEFAULT 40,
    spare_7d_within_hours   INTEGER      NOT NULL DEFAULT 24,
    spare_5h_min_pct        NUMERIC(6,2) NOT NULL DEFAULT 40,
    spare_5h_within_minutes INTEGER      NOT NULL DEFAULT 60,
    updated_at              TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
INSERT INTO fork_quota_settings (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

-- Quotas in x (NULL = unlimited for that window).
CREATE TABLE IF NOT EXISTS fork_group_quota_defaults (
    group_id   BIGINT       PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
    quota_5h_x NUMERIC(8,3),
    quota_7d_x NUMERIC(8,3),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS fork_plan_quotas (
    plan_id    BIGINT       PRIMARY KEY REFERENCES subscription_plans(id) ON DELETE CASCADE,
    quota_5h_x NUMERIC(8,3),
    quota_7d_x NUMERIC(8,3),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- A row here replaces the group default for the subscription (both windows).
-- order_id guards against an old order's retried fulfillment overwriting a newer plan.
CREATE TABLE IF NOT EXISTS fork_subscription_quotas (
    subscription_id BIGINT       PRIMARY KEY REFERENCES user_subscriptions(id) ON DELETE CASCADE,
    quota_5h_x      NUMERIC(8,3),
    quota_7d_x      NUMERIC(8,3),
    source          VARCHAR(10)  NOT NULL CHECK (source IN ('plan', 'admin')),
    plan_id         BIGINT,
    order_id        BIGINT,
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
