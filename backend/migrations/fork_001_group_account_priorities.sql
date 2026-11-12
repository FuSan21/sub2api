-- [fork] Per-group account priority overrides.
-- When a row exists for (group_id, account_id), the scheduler uses its priority
-- instead of accounts.priority for requests routed through that group.
-- Upstream ignores this table, so switching back to the upstream image is safe.
CREATE TABLE IF NOT EXISTS fork_group_account_priorities (
    group_id   BIGINT      NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    account_id BIGINT      NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    priority   INTEGER     NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, account_id)
);

CREATE INDEX IF NOT EXISTS idx_fork_group_account_priorities_account
    ON fork_group_account_priorities (account_id);
