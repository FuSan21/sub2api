package service

// [fork] Usage share.
//
// Anthropic reports each account's 5h and 7d utilization on every response
// (stored by upstream in accounts.extra). Every 30s the engine records changes
// and splits each utilization rise across the users who made requests on that
// account since the previous rise, weighted by API-equivalent cost
// (usage_logs.total_cost). A rise with no sub2api traffic is "outside" usage.
//
// A user's consumption in a group is the sum of their attributed share of each
// group account's *current* window, as a % of the group pool
// (100% = every account in the group fully used). Groups can set per-user limits
// for 5h and 7d; a user over a limit may only use accounts that still have spare
// capacity shortly before they reset, otherwise requests get a 429.

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	UsageWindow5h = "5h"
	UsageWindow7d = "7d"

	usageShareTickInterval   = 30 * time.Second
	usageShareStaleAfter     = 5 * time.Minute
	usageSharePruneInterval  = time.Hour
	usageShareRetention      = 9 * 24 * time.Hour
	usageShareResetTolerance = time.Hour
	usageShareEpsilon        = 1e-9
)

var usageWindowKinds = []string{UsageWindow5h, UsageWindow7d}

func usageWindowLength(kind string) time.Duration {
	if kind == UsageWindow5h {
		return 5 * time.Hour
	}
	return 7 * 24 * time.Hour
}

// UsageShareAccountState is an Anthropic OAuth account's latest reported usage.
type UsageShareAccountState struct {
	ID        int64
	Name      string
	Status    string
	Util      map[string]*float64
	Reset     map[string]*time.Time
	SampledAt *time.Time
}

// UsageSample is the last recorded utilization of one account window.
type UsageSample struct {
	AccountID   int64
	Window      string
	Utilization float64
	ResetAt     time.Time
	SampledAt   time.Time
}

// UsageUserCost is one user's request cost on an account in a segment.
type UsageUserCost struct {
	UserID   int64
	Cost     float64
	Requests int64
}

// UsageAttribution assigns part of a utilization rise to a user (nil = outside sub2api).
type UsageAttribution struct {
	UserID *int64
	Delta  float64
}

// UsageAttributionSum is the attributed total for one user in one account window.
type UsageAttributionSum struct {
	AccountID int64
	Window    string
	ResetAt   time.Time
	UserID    *int64
	Delta     float64
}

// UsageShareUserLimit overrides the group defaults for one user (nil = default).
type UsageShareUserLimit struct {
	UserID     int64    `json:"user_id"`
	Limit7dPct *float64 `json:"limit_7d_pct"`
	Limit5hPct *float64 `json:"limit_5h_pct"`
}

// UsageShareSettings configures usage share for a group.
type UsageShareSettings struct {
	GroupID              int64                 `json:"group_id"`
	Enabled              bool                  `json:"enabled"`
	Default7dPct         *float64              `json:"default_7d_pct"`
	Default5hPct         *float64              `json:"default_5h_pct"`
	Spare7dMinPct        float64               `json:"spare_7d_min_pct"`
	Spare7dWithinHours   int                   `json:"spare_7d_within_hours"`
	Spare5hMinPct        float64               `json:"spare_5h_min_pct"`
	Spare5hWithinMinutes int                   `json:"spare_5h_within_minutes"`
	Limits               []UsageShareUserLimit `json:"limits"`
}

func defaultUsageShareSettings(groupID int64) UsageShareSettings {
	return UsageShareSettings{
		GroupID:              groupID,
		Spare7dMinPct:        40,
		Spare7dWithinHours:   24,
		Spare5hMinPct:        40,
		Spare5hWithinMinutes: 60,
	}
}

func (s UsageShareSettings) limitFor(userID int64, window string) *float64 {
	for _, l := range s.Limits {
		if l.UserID != userID {
			continue
		}
		if window == UsageWindow5h && l.Limit5hPct != nil {
			return l.Limit5hPct
		}
		if window == UsageWindow7d && l.Limit7dPct != nil {
			return l.Limit7dPct
		}
		break
	}
	if window == UsageWindow5h {
		return s.Default5hPct
	}
	return s.Default7dPct
}

func (s UsageShareSettings) spareRule(window string) (minRemainingPct float64, within time.Duration) {
	if window == UsageWindow5h {
		return s.Spare5hMinPct, time.Duration(s.Spare5hWithinMinutes) * time.Minute
	}
	return s.Spare7dMinPct, time.Duration(s.Spare7dWithinHours) * time.Hour
}

// UsageShareUser identifies a user in reports.
type UsageShareUser struct {
	ID       int64  `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
}

// UsageShareStore is implemented by the account repository.
type UsageShareStore interface {
	ListUsageShareAccountStates(ctx context.Context) ([]UsageShareAccountState, error)
	LatestUsageSamples(ctx context.Context) ([]UsageSample, error)
	UsageCostByUser(ctx context.Context, accountID int64, from, to time.Time) ([]UsageUserCost, error)
	RecordUsageSegment(ctx context.Context, sample UsageSample, segmentStart time.Time, attributions []UsageAttribution, estimated bool) error
	ListCurrentUsageAttributions(ctx context.Context, now time.Time) ([]UsageAttributionSum, error)
	ListUsageShareSettings(ctx context.Context) ([]UsageShareSettings, error)
	SaveUsageShareSettings(ctx context.Context, settings UsageShareSettings) error
	ListUsageShareGroupAccounts(ctx context.Context, groupIDs []int64) (map[int64][]int64, error)
	ListUsageShareUsers(ctx context.Context, groupID int64, extraUserIDs []int64) ([]UsageShareUser, error)
	PruneUsageShare(ctx context.Context, before time.Time) error
}

// splitUsageDelta divides a utilization rise across users by cost, falling back
// to request count when all costs are zero, or to outside usage when idle.
func splitUsageDelta(delta float64, costs []UsageUserCost) []UsageAttribution {
	if delta <= usageShareEpsilon {
		return nil
	}
	var totalCost float64
	var totalRequests int64
	for _, c := range costs {
		if c.Cost > 0 {
			totalCost += c.Cost
		}
		totalRequests += c.Requests
	}
	out := make([]UsageAttribution, 0, len(costs))
	for _, c := range costs {
		var weight float64
		switch {
		case totalCost > 0:
			weight = math.Max(c.Cost, 0) / totalCost
		case totalRequests > 0:
			weight = float64(c.Requests) / float64(totalRequests)
		}
		if weight <= 0 {
			continue
		}
		userID := c.UserID
		out = append(out, UsageAttribution{UserID: &userID, Delta: delta * weight})
	}
	if len(out) == 0 {
		out = append(out, UsageAttribution{Delta: delta})
	}
	return out
}

// usageSegment is the outcome of comparing a new observation with the last sample.
type usageSegment struct {
	record    bool // write the new sample
	delta     float64
	start     time.Time
	resetAt   time.Time // canonical window key
	estimated bool
}

// planUsageSegment decides what a new observation means relative to the last sample.
func planUsageSegment(last *UsageSample, window string, util float64, resetAt, sampledAt time.Time) usageSegment {
	length := usageWindowLength(window)
	windowStart := resetAt.Add(-length)
	if last == nil {
		// First observation: backfill the current window by cost ratio.
		return usageSegment{record: true, delta: util, start: windowStart, resetAt: resetAt, estimated: true}
	}
	diff := resetAt.Sub(last.ResetAt)
	switch {
	case math.Abs(float64(diff)) <= float64(usageShareResetTolerance):
		// Same window; keep the first-seen reset as the key.
		if util > last.Utilization+usageShareEpsilon {
			return usageSegment{record: true, delta: util - last.Utilization, start: last.SampledAt, resetAt: last.ResetAt}
		}
		if util < last.Utilization-usageShareEpsilon {
			// Shouldn't happen inside a window; rebaseline without attributing.
			return usageSegment{record: true, resetAt: last.ResetAt}
		}
		return usageSegment{}
	case diff > 0:
		// A new window started; it began at zero.
		start := last.SampledAt
		if windowStart.After(start) {
			start = windowStart
		}
		return usageSegment{record: true, delta: util, start: start, resetAt: resetAt}
	default:
		return usageSegment{}
	}
}

type usageContribution struct {
	resetAt time.Time
	pct     float64
}

type usageShareUserView struct {
	pct     map[string]float64
	contrib map[string][]usageContribution
}

type usageShareGroupView struct {
	settings   UsageShareSettings
	accountIDs []int64
	users      map[int64]*usageShareUserView
	// per account, per window: user -> attributed fraction; key 0 = outside
	perAccount map[int64]map[string]map[int64]float64
}

type usageShareAccountView struct {
	util  map[string]float64
	reset map[string]time.Time
}

type usageShareSnapshot struct {
	at       time.Time
	groups   map[int64]*usageShareGroupView
	accounts map[int64]usageShareAccountView
}

type usageShareEngine struct {
	store     UsageShareStore
	now       func() time.Time
	mu        sync.Mutex
	last      map[string]*UsageSample
	loaded    bool
	names     map[int64]string
	lastPrune time.Time
	snapshot  atomic.Pointer[usageShareSnapshot]
}

var activeUsageShare atomic.Pointer[usageShareEngine]

func usageSampleKey(accountID int64, window string) string {
	return fmt.Sprintf("%d/%s", accountID, window)
}

// StartForkUsageShare starts the background engine once per process.
func StartForkUsageShare(repo AccountRepository, cfg *config.Config) {
	store, ok := repo.(UsageShareStore)
	if !ok {
		return
	}
	engine := &usageShareEngine{store: store, now: time.Now, last: map[string]*UsageSample{}, names: map[int64]string{}}
	if !activeUsageShare.CompareAndSwap(nil, engine) {
		return
	}
	go engine.run()
}

func (e *usageShareEngine) run() {
	time.Sleep(10 * time.Second)
	ticker := time.NewTicker(usageShareTickInterval)
	defer ticker.Stop()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		if err := e.tick(ctx); err != nil {
			slog.Warn("fork_usage_share_tick_failed", "error", err)
		}
		cancel()
		<-ticker.C
	}
}

func (e *usageShareEngine) tick(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.ingest(ctx); err != nil {
		return err
	}
	if now := e.now(); now.Sub(e.lastPrune) > usageSharePruneInterval {
		if err := e.store.PruneUsageShare(ctx, now.Add(-usageShareRetention)); err != nil {
			slog.Warn("fork_usage_share_prune_failed", "error", err)
		}
		e.lastPrune = now
	}
	return e.rebuildLocked(ctx)
}

func (e *usageShareEngine) ingest(ctx context.Context) error {
	if !e.loaded {
		samples, err := e.store.LatestUsageSamples(ctx)
		if err != nil {
			return err
		}
		for i := range samples {
			s := samples[i]
			e.last[usageSampleKey(s.AccountID, s.Window)] = &s
		}
		e.loaded = true
	}
	states, err := e.store.ListUsageShareAccountStates(ctx)
	if err != nil {
		return err
	}
	now := e.now()
	for _, st := range states {
		e.names[st.ID] = st.Name
		sampledAt := now
		if st.SampledAt != nil && !st.SampledAt.After(now) {
			sampledAt = *st.SampledAt
		}
		for _, w := range usageWindowKinds {
			util, reset := st.Util[w], st.Reset[w]
			if util == nil || reset == nil {
				continue
			}
			key := usageSampleKey(st.ID, w)
			last := e.last[key]
			if last != nil && !sampledAt.After(last.SampledAt) {
				continue // nothing new since the last sample
			}
			seg := planUsageSegment(last, w, *util, *reset, sampledAt)
			if !seg.record {
				continue
			}
			var atts []UsageAttribution
			if seg.delta > usageShareEpsilon {
				costs, err := e.store.UsageCostByUser(ctx, st.ID, seg.start, sampledAt)
				if err != nil {
					return err
				}
				atts = splitUsageDelta(seg.delta, costs)
			}
			sample := UsageSample{AccountID: st.ID, Window: w, Utilization: *util, ResetAt: seg.resetAt, SampledAt: sampledAt}
			if err := e.store.RecordUsageSegment(ctx, sample, seg.start, atts, seg.estimated); err != nil {
				return err
			}
			e.last[key] = &sample
		}
	}
	return nil
}

func (e *usageShareEngine) rebuildLocked(ctx context.Context) error {
	settings, err := e.store.ListUsageShareSettings(ctx)
	if err != nil {
		return err
	}
	enabled := make([]UsageShareSettings, 0, len(settings))
	for _, s := range settings {
		if s.Enabled {
			enabled = append(enabled, s)
		}
	}
	snap, err := e.buildSnapshot(ctx, enabled)
	if err != nil {
		return err
	}
	e.snapshot.Store(snap)
	return nil
}

func (e *usageShareEngine) buildSnapshot(ctx context.Context, settings []UsageShareSettings) (*usageShareSnapshot, error) {
	now := e.now()
	snap := &usageShareSnapshot{at: now, groups: map[int64]*usageShareGroupView{}, accounts: map[int64]usageShareAccountView{}}
	for _, s := range e.last {
		view, ok := snap.accounts[s.AccountID]
		if !ok {
			view = usageShareAccountView{util: map[string]float64{}, reset: map[string]time.Time{}}
			snap.accounts[s.AccountID] = view
		}
		if s.ResetAt.After(now) {
			view.util[s.Window] = s.Utilization
			view.reset[s.Window] = s.ResetAt
		}
	}
	if len(settings) == 0 {
		return snap, nil
	}
	groupIDs := make([]int64, 0, len(settings))
	for _, s := range settings {
		groupIDs = append(groupIDs, s.GroupID)
	}
	groupAccounts, err := e.store.ListUsageShareGroupAccounts(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	sums, err := e.store.ListCurrentUsageAttributions(ctx, now)
	if err != nil {
		return nil, err
	}
	// account -> window -> user (0 = outside) -> fraction, current windows only
	current := map[int64]map[string]map[int64]float64{}
	for _, sum := range sums {
		acc, ok := snap.accounts[sum.AccountID]
		if !ok {
			continue
		}
		reset, ok := acc.reset[sum.Window]
		if !ok || !reset.Equal(sum.ResetAt) {
			continue
		}
		if current[sum.AccountID] == nil {
			current[sum.AccountID] = map[string]map[int64]float64{}
		}
		if current[sum.AccountID][sum.Window] == nil {
			current[sum.AccountID][sum.Window] = map[int64]float64{}
		}
		var uid int64
		if sum.UserID != nil {
			uid = *sum.UserID
		}
		current[sum.AccountID][sum.Window][uid] += sum.Delta
	}
	for _, s := range settings {
		g := &usageShareGroupView{settings: s, accountIDs: groupAccounts[s.GroupID], users: map[int64]*usageShareUserView{}, perAccount: map[int64]map[string]map[int64]float64{}}
		n := float64(len(g.accountIDs))
		for _, accID := range g.accountIDs {
			g.perAccount[accID] = current[accID]
			for w, byUser := range current[accID] {
				reset := snap.accounts[accID].reset[w]
				for uid, frac := range byUser {
					if uid == 0 {
						continue
					}
					u := g.users[uid]
					if u == nil {
						u = &usageShareUserView{pct: map[string]float64{}, contrib: map[string][]usageContribution{}}
						g.users[uid] = u
					}
					pct := 100 * frac / n
					u.pct[w] += pct
					u.contrib[w] = append(u.contrib[w], usageContribution{resetAt: reset, pct: pct})
				}
			}
		}
		snap.groups[s.GroupID] = g
	}
	return snap, nil
}

// --- enforcement -----------------------------------------------------------

func (e *usageShareEngine) fresh() *usageShareSnapshot {
	snap := e.snapshot.Load()
	if snap == nil || e.now().Sub(snap.at) > usageShareStaleAfter {
		return nil // fail open when data is stale
	}
	return snap
}

// exceeded returns the windows in which the user is at or over their limit.
func (g *usageShareGroupView) exceeded(userID int64) []string {
	var out []string
	u := g.users[userID]
	for _, w := range usageWindowKinds {
		limit := g.settings.limitFor(userID, w)
		if limit == nil {
			continue
		}
		var used float64
		if u != nil {
			used = u.pct[w]
		}
		if used >= *limit {
			out = append(out, w)
		}
	}
	return out
}

// accountHasSpare reports whether an over-limit user may still use the account
// because it has unused capacity that resets soon in every exceeded window.
func (g *usageShareGroupView) accountHasSpare(acc usageShareAccountView, windows []string, now time.Time) bool {
	for _, w := range windows {
		reset, ok := acc.reset[w]
		if !ok {
			return false
		}
		minPct, within := g.settings.spareRule(w)
		if (1-acc.util[w])*100 < minPct || reset.Sub(now) > within {
			return false
		}
	}
	return true
}

func usageShareRequestIdentity(ctx context.Context) (userID, groupID int64, ok bool) {
	userID, _ = ctx.Value(ctxkey.UserID).(int64)
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	if userID <= 0 || group == nil || group.ID <= 0 {
		return 0, 0, false
	}
	return userID, group.ID, true
}

// forkUsageShareAllowsAccount is consulted for every Anthropic account selection.
func forkUsageShareAllowsAccount(ctx context.Context, account *Account) bool {
	e := activeUsageShare.Load()
	if e == nil || account == nil {
		return true
	}
	snap := e.fresh()
	if snap == nil {
		return true
	}
	userID, groupID, ok := usageShareRequestIdentity(ctx)
	if !ok {
		return true
	}
	g := snap.groups[groupID]
	if g == nil {
		return true
	}
	over := g.exceeded(userID)
	if len(over) == 0 {
		return true
	}
	return g.accountHasSpare(snap.accounts[account.ID], over, e.now())
}

// ForkUsageShareBlocked reports whether the user must be rejected before
// scheduling: over a limit with no group account that has spare capacity.
func ForkUsageShareBlocked(groupID, userID int64) (string, bool) {
	e := activeUsageShare.Load()
	if e == nil {
		return "", false
	}
	snap := e.fresh()
	if snap == nil {
		return "", false
	}
	g := snap.groups[groupID]
	if g == nil {
		return "", false
	}
	over := g.exceeded(userID)
	if len(over) == 0 {
		return "", false
	}
	now := e.now()
	for _, accID := range g.accountIDs {
		if g.accountHasSpare(snap.accounts[accID], over, now) {
			return "", false
		}
	}
	return g.blockMessage(userID, over, now), true
}

// freesAt is when the user's usage in the window drops below the limit as
// account windows reset.
func (g *usageShareGroupView) freesAt(userID int64, window string, now time.Time) time.Time {
	u := g.users[userID]
	limit := g.settings.limitFor(userID, window)
	if u == nil || limit == nil {
		return now
	}
	contrib := append([]usageContribution(nil), u.contrib[window]...)
	sort.Slice(contrib, func(i, j int) bool { return contrib[i].resetAt.Before(contrib[j].resetAt) })
	used := u.pct[window]
	for _, c := range contrib {
		used -= c.pct
		if used < *limit {
			return c.resetAt
		}
	}
	return now
}

func (g *usageShareGroupView) blockMessage(userID int64, windows []string, now time.Time) string {
	parts := make([]string, 0, len(windows))
	var until time.Time
	for _, w := range windows {
		limit := g.settings.limitFor(userID, w)
		var used float64
		if u := g.users[userID]; u != nil {
			used = u.pct[w]
		}
		label := "weekly"
		if w == UsageWindow5h {
			label = "5-hour"
		}
		parts = append(parts, fmt.Sprintf("%s %.1f%% of %.1f%%", label, used, *limit))
		if t := g.freesAt(userID, w, now); t.After(until) {
			until = t
		}
	}
	msg := "Usage share limit reached (" + strings.Join(parts, ", ") + " of the account pool)."
	if until.After(now) {
		msg += " Frees up around " + until.In(time.Local).Format("Mon Jan 2 15:04 MST") + "."
	}
	return msg
}

// --- admin -----------------------------------------------------------------

// UsageShareReportAccount shows one group account's current windows.
type UsageShareReportAccount struct {
	ID        int64             `json:"id"`
	Name      string            `json:"name"`
	Util5h    *float64          `json:"util_5h_pct"`
	Reset5h   *time.Time        `json:"reset_5h"`
	Util7d    *float64          `json:"util_7d_pct"`
	Reset7d   *time.Time        `json:"reset_7d"`
	Outside5h float64           `json:"outside_5h_pct"`
	Outside7d float64           `json:"outside_7d_pct"`
	ByUser5h  map[int64]float64 `json:"by_user_5h_pct"`
	ByUser7d  map[int64]float64 `json:"by_user_7d_pct"`
}

// UsageShareReportUser shows one user's consumption in the group.
type UsageShareReportUser struct {
	UsageShareUser
	Used5hPct  float64              `json:"used_5h_pct"`
	Used7dPct  float64              `json:"used_7d_pct"`
	Limit5hPct *float64             `json:"limit_5h_pct"`
	Limit7dPct *float64             `json:"limit_7d_pct"`
	Override   *UsageShareUserLimit `json:"override"`
	Blocked    bool                 `json:"blocked"`
	FreesAt    *time.Time           `json:"frees_at,omitempty"`
}

// UsageShareReport is the admin view of a group.
type UsageShareReport struct {
	Settings UsageShareSettings        `json:"settings"`
	Accounts []UsageShareReportAccount `json:"accounts"`
	Users    []UsageShareReportUser    `json:"users"`
}

func (s *adminServiceImpl) usageShare() (*usageShareEngine, error) {
	e := activeUsageShare.Load()
	if e == nil {
		return nil, infraerrors.InternalServer("USAGE_SHARE_UNAVAILABLE", "usage share engine is not running")
	}
	return e, nil
}

func (s *adminServiceImpl) GetUsageShare(ctx context.Context, groupID int64) (*UsageShareReport, error) {
	if _, err := s.groupRepo.GetByIDLite(ctx, groupID); err != nil {
		return nil, err
	}
	e, err := s.usageShare()
	if err != nil {
		return nil, err
	}
	return e.report(ctx, groupID)
}

func (s *adminServiceImpl) SetUsageShare(ctx context.Context, groupID int64, input UsageShareSettings) (*UsageShareReport, error) {
	if _, err := s.groupRepo.GetByIDLite(ctx, groupID); err != nil {
		return nil, err
	}
	e, err := s.usageShare()
	if err != nil {
		return nil, err
	}
	input.GroupID = groupID
	if err := validateUsageShareSettings(input); err != nil {
		return nil, err
	}
	if err := e.store.SaveUsageShareSettings(ctx, input); err != nil {
		return nil, err
	}
	e.mu.Lock()
	err = e.rebuildLocked(ctx)
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return e.report(ctx, groupID)
}

func validateUsageShareSettings(s UsageShareSettings) error {
	pct := func(v *float64) bool { return v == nil || (*v >= 0 && *v <= 100) }
	if !pct(s.Default5hPct) || !pct(s.Default7dPct) || s.Spare5hMinPct < 0 || s.Spare5hMinPct > 100 ||
		s.Spare7dMinPct < 0 || s.Spare7dMinPct > 100 || s.Spare5hWithinMinutes < 0 || s.Spare7dWithinHours < 0 {
		return infraerrors.BadRequest("INVALID_USAGE_SHARE", "percentages must be 0-100 and durations >= 0")
	}
	for _, l := range s.Limits {
		if l.UserID <= 0 || !pct(l.Limit5hPct) || !pct(l.Limit7dPct) {
			return infraerrors.BadRequest("INVALID_USAGE_SHARE", "invalid per-user limit")
		}
	}
	return nil
}

func (e *usageShareEngine) report(ctx context.Context, groupID int64) (*UsageShareReport, error) {
	all, err := e.store.ListUsageShareSettings(ctx)
	if err != nil {
		return nil, err
	}
	settings := defaultUsageShareSettings(groupID)
	for _, s := range all {
		if s.GroupID == groupID {
			settings = s
		}
	}
	e.mu.Lock()
	snap, err := e.buildSnapshot(ctx, []UsageShareSettings{settings})
	names := make(map[int64]string, len(e.names))
	for k, v := range e.names {
		names[k] = v
	}
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	g := snap.groups[groupID]
	now := e.now()
	rep := &UsageShareReport{Settings: settings, Accounts: []UsageShareReportAccount{}, Users: []UsageShareReportUser{}}
	n := float64(len(g.accountIDs))
	for _, accID := range g.accountIDs {
		acc := snap.accounts[accID]
		ra := UsageShareReportAccount{ID: accID, Name: names[accID], ByUser5h: map[int64]float64{}, ByUser7d: map[int64]float64{}}
		for _, w := range usageWindowKinds {
			reset, ok := acc.reset[w]
			if !ok {
				continue
			}
			util, r := 100*acc.util[w], reset
			if w == UsageWindow5h {
				ra.Util5h, ra.Reset5h = &util, &r
			} else {
				ra.Util7d, ra.Reset7d = &util, &r
			}
			for uid, frac := range g.perAccount[accID][w] {
				pct := 100 * frac
				switch {
				case uid == 0 && w == UsageWindow5h:
					ra.Outside5h = pct
				case uid == 0:
					ra.Outside7d = pct
				case w == UsageWindow5h:
					ra.ByUser5h[uid] = pct
				default:
					ra.ByUser7d[uid] = pct
				}
			}
		}
		rep.Accounts = append(rep.Accounts, ra)
	}
	extra := make([]int64, 0, len(g.users))
	for uid := range g.users {
		extra = append(extra, uid)
	}
	users, err := e.store.ListUsageShareUsers(ctx, groupID, extra)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		ru := UsageShareReportUser{UsageShareUser: u, Limit5hPct: settings.limitFor(u.ID, UsageWindow5h), Limit7dPct: settings.limitFor(u.ID, UsageWindow7d)}
		if v := g.users[u.ID]; v != nil {
			ru.Used5hPct, ru.Used7dPct = v.pct[UsageWindow5h], v.pct[UsageWindow7d]
		}
		for i := range settings.Limits {
			if settings.Limits[i].UserID == u.ID {
				l := settings.Limits[i]
				ru.Override = &l
			}
		}
		if over := g.exceeded(u.ID); len(over) > 0 && n > 0 {
			ru.Blocked = true
			for _, accID := range g.accountIDs {
				if g.accountHasSpare(snap.accounts[accID], over, now) {
					ru.Blocked = false
				}
			}
			var until time.Time
			for _, w := range over {
				if t := g.freesAt(u.ID, w, now); t.After(until) {
					until = t
				}
			}
			if until.After(now) {
				ru.FreesAt = &until
			}
		}
		rep.Users = append(rep.Users, ru)
	}
	sort.Slice(rep.Users, func(i, j int) bool { return rep.Users[i].Used7dPct > rep.Users[j].Used7dPct })
	return rep, nil
}
