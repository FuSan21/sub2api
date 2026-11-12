-- [fork] Usage share: attribute Anthropic account 5h/7d utilization to users and
-- enforce per-user limits expressed as % of the group's account pool.
-- Upstream ignores these tables, so switching back to the upstream image is safe.

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

-- Each utilization rise split across the users active on the account in that segment.
-- user_id NULL = usage from outside sub2api. delta is a fraction of the account window (0.01 = 1%).
CREATE TABLE IF NOT EXISTS fork_usage_attributions (
    id            BIGSERIAL PRIMARY KEY,
    account_id    BIGINT        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    window_kind   VARCHAR(4)    NOT NULL CHECK (window_kind IN ('5h', '7d')),
    reset_at      TIMESTAMPTZ   NOT NULL,
    user_id       BIGINT        REFERENCES users(id) ON DELETE CASCADE,
    delta         NUMERIC(10,8) NOT NULL,
    segment_start TIMESTAMPTZ   NOT NULL,
    segment_end   TIMESTAMPTZ   NOT NULL,
    estimated     BOOLEAN       NOT NULL DEFAULT FALSE
);
CREATE INDEX IF NOT EXISTS idx_fork_usage_attributions_window
    ON fork_usage_attributions (account_id, window_kind, reset_at);
CREATE INDEX IF NOT EXISTS idx_fork_usage_attributions_segment_end
    ON fork_usage_attributions (segment_end);

CREATE TABLE IF NOT EXISTS fork_usage_share_settings (
    group_id                BIGINT       PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
    enabled                 BOOLEAN      NOT NULL DEFAULT FALSE,
    default_7d_pct          NUMERIC(6,2),
    default_5h_pct          NUMERIC(6,2),
    spare_7d_min_pct        NUMERIC(6,2) NOT NULL DEFAULT 40,
    spare_7d_within_hours   INTEGER      NOT NULL DEFAULT 24,
    spare_5h_min_pct        NUMERIC(6,2) NOT NULL DEFAULT 40,
    spare_5h_within_minutes INTEGER      NOT NULL DEFAULT 60,
    updated_at              TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Per-user overrides; NULL = use the group default.
CREATE TABLE IF NOT EXISTS fork_usage_share_limits (
    group_id     BIGINT       NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id      BIGINT       NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    limit_7d_pct NUMERIC(6,2),
    limit_5h_pct NUMERIC(6,2),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);
