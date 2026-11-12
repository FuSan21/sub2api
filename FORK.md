# FuSan21/sub2api fork

A fork of [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) that follows
upstream releases and adds a small set of patches.

Image: `ghcr.io/fusan21/sub2api:latest` (also `:<upstream-version>-fusan` and `:sha-<commit>`),
built for linux/amd64 and linux/arm64. The app reports its version as `<upstream-version>+fusan`.

## Branches

| Branch | Contents |
|---|---|
| `fusan` (default) | latest upstream release + all patches + fork infra. Images are built from here. |
| `patch/<name>` | one feature each, based on the upstream release it was written against. |
| `main` | untouched mirror of upstream `main`. |

## Automation (`.github/workflows/fork-release.yml`)

Daily at 03:00 Asia/Dhaka, the workflow:

1. looks up the latest upstream release tag,
2. merges it into `fusan` (skipped if already merged),
3. runs `go test -tags=unit ./...`,
4. pushes the merge and builds and pushes the image.

Pushes to `fusan` (e.g. a new patch) rebuild the image as well; `workflow_dispatch`
with `force_build` rebuilds manually.

A merge conflict or a failed test/build opens (or comments on) a GitHub issue and
publishes nothing, so `:latest` stays on the last good build. To fix a conflict:

```sh
git fetch upstream --tags
git switch fusan
git merge vX.Y.Z      # resolve, test
git push origin fusan # triggers a build
```

If upstream changes files under `.github/workflows/`, pushing the merge needs a
token with the `workflow` scope: add a fine-grained PAT (Contents + Workflows:
read/write on this repo) as the repository secret `FORK_PUSH_TOKEN`.

## Writing a patch

Keep upstream files nearly untouched so merges stay clean:

- put the code in new files prefixed with `fork_` (Go) / `Fork` / `fork` (frontend),
- reach new methods through type assertions instead of changing upstream interfaces or wire DI,
- mark the few hook lines in upstream files with `// [fork]` / `<!-- [fork] -->`,
- name migrations `backend/migrations/fork_NNN_*.sql` (they sort after upstream's numbered ones,
  and upstream simply ignores the extra tables),
- hardcode English UI strings instead of editing the upstream locale files.

Then: branch `patch/<name>` from the upstream tag that `fusan` is on, commit, and
`git merge --no-ff patch/<name>` into `fusan`.

## Patches

### `patch/group-account-priority`

- **Per-group account priority.** Upstream ranks accounts by `accounts.priority` only, so an
  account has the same rank in every group. Table `fork_group_account_priorities` holds optional
  per-(group, account) overrides that the scheduler uses for requests in that group.
  Groups page → **Priority** (also in simple mode), or
  `GET/PUT /api/v1/admin/groups/:id/account-priorities` with
  `{"items":[{"account_id":1,"priority":0},{"account_id":2,"priority":null}]}` (null clears).
- **Group-scoped scheduling in simple mode.** Upstream simple mode ignores groups (any key may use
  any account). In the fork, keys with a group only use accounts bound to that group; ungrouped keys
  behave as upstream.

Both are applied in `SchedulerSnapshotService.ListSchedulableAccounts`, so every platform's
scheduler sees them. Overrides are cached in-process for 10 s and invalidated on save.

### `patch/usage-share`

Measures each person's consumption in real account limits instead of dollars, and lets groups cap it.

- **Attribution.** Anthropic reports every account's 5h and 7d utilization on each response (upstream
  stores it in `accounts.extra`). Every 30 s the fork records changes (`fork_usage_samples`) and splits each
  rise across the users who made requests on that account since the previous rise, weighted by
  API-equivalent cost (`usage_logs.total_cost`), into `fork_usage_attributions`. A single active user gets the
  whole rise; a rise with no sub2api traffic is stored as **outside** usage (user NULL). On first start the
  current windows are backfilled by cost ratio (`estimated = true`).
- **Consumption** of a user in a group = Σ their share of each group account's *current* window / number of
  active Anthropic OAuth accounts in the group, i.e. % of the pool. It follows each account's own reset.
- **Limits** (`fork_usage_share_settings`, `fork_usage_share_limits`): per group default 5h/7d % and per-user
  overrides. Over a limit, a user may only use accounts that still have ≥ X% left and reset within Y
  (defaults 40% / 24 h for 7d, 40% / 60 min for 5h) — capacity that would otherwise be wasted. With no such
  account the request gets `429 USAGE_SHARE_EXCEEDED` and the time usage frees up. Enforcement fails open
  when data is older than 5 minutes.
- Groups page → **Usage** (also in simple mode), or `GET/PUT /api/v1/admin/groups/:id/usage-share`.
- Hooks: `GatewayService.isAccountSchedulableForModelSelection` (all Anthropic selection paths incl. sticky
  sessions), the API-key auth middleware (early 429), `ProvideSchedulerSnapshotService` (starts the engine).

### `patch/version-display`

- The version is `<upstream>+fusan`; upstream's update check parsed `21+fusan` as 0 and always showed
  "a new version is available". Build metadata (`+…`) is now ignored when comparing.
- In-app update and rollback are disabled for fork builds (they would swap in an upstream release binary).
  Updates arrive through the `ghcr.io/fusan21/sub2api` image.
