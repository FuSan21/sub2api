package service

// [fork] Usage share: per-subscription quotas measured in Pro-account equivalents.
//
// Anthropic reports each account's 5h and 7d utilization on every response
// (stored by upstream in accounts.extra). Every 30s the engine records changes
// and splits each utilization rise across the (user, group) pairs that made
// requests on that account since the previous rise, weighted by API-equivalent
// cost (usage_logs.total_cost). A rise with no sub2api traffic is "outside" usage.
//
// Usage is expressed in x: 1x = one full window of a $20 Claude Pro account. A
// rise of 10% on a Max 20x account (weight 20) is 2x. A user's usage in a group
// is the sum over accounts of their attributed share of each account's *current*
// window times the account weight, so it follows each account's own reset.
//
// Quotas (in x, per window) belong to the user's live subscription in the group:
// a per-subscription row (copied from the plan on purchase, or set by an admin)
// or else the group default. Without a subscription nothing is limited. Over a
// quota, a subscription may only use accounts that still have spare capacity
// shortly before they reset; with none, requests get a 429.

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

	// Quota sources reported to the UI.
	UsageQuotaSourcePlan  = "plan"
	UsageQuotaSourceAdmin = "admin"
	UsageQuotaSourceGroup = "group"
	UsageQuotaSourceNone  = "none"

	// Normalized account tiers.
	UsageTierPro    = "pro"
	UsageTierMax5x  = "max_5x"
	UsageTierMax20x = "max_20x"

	usageQuotaMaxX  = 10000
	usageWeightMaxX = 1000
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
	Type      string
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

// UsageUserCost is one (user, group) pair's request cost on an account in a segment.
type UsageUserCost struct {
	UserID   int64
	GroupID  *int64
	Cost     float64
	Requests int64
}

// UsageAttribution assigns part of a utilization rise to a user in a group
// (UserID nil = outside sub2api).
type UsageAttribution struct {
	UserID  *int64
	GroupID *int64
	Delta   float64
}

// UsageAttributionSum is the attributed total for one (user, group) in one account window.
type UsageAttributionSum struct {
	AccountID int64
	Window    string
	ResetAt   time.Time
	UserID    *int64
	GroupID   *int64
	Delta     float64
}

// UsageQuota is a per-window quota in x (nil = unlimited).
type UsageQuota struct {
	Quota5hX *float64 `json:"quota_5h_x"`
	Quota7dX *float64 `json:"quota_7d_x"`
}

func (q UsageQuota) forWindow(w string) *float64 {
	if w == UsageWindow5h {
		return q.Quota5hX
	}
	return q.Quota7dX
}

func (q UsageQuota) limited() bool { return q.Quota5hX != nil || q.Quota7dX != nil }

// UsageQuotaSettings is the global spare-capacity rule.
type UsageQuotaSettings struct {
	Spare7dMinPct        float64 `json:"spare_7d_min_pct"`
	Spare7dWithinHours   int     `json:"spare_7d_within_hours"`
	Spare5hMinPct        float64 `json:"spare_5h_min_pct"`
	Spare5hWithinMinutes int     `json:"spare_5h_within_minutes"`
}

func defaultUsageQuotaSettings() UsageQuotaSettings {
	return UsageQuotaSettings{Spare7dMinPct: 40, Spare7dWithinHours: 24, Spare5hMinPct: 40, Spare5hWithinMinutes: 60}
}

func (s UsageQuotaSettings) spareRule(window string) (minRemainingPct float64, within time.Duration) {
	if window == UsageWindow5h {
		return s.Spare5hMinPct, time.Duration(s.Spare5hWithinMinutes) * time.Minute
	}
	return s.Spare7dMinPct, time.Duration(s.Spare7dWithinHours) * time.Hour
}

// UsageAccountCapacity is an account's detected tier and admin weight overrides.
type UsageAccountCapacity struct {
	AccountID    int64      `json:"account_id"`
	DetectedTier *string    `json:"detected_tier"`
	DetectedAt   *time.Time `json:"detected_at"`
	DetectError  *string    `json:"detect_error"`
	Weight5h     *float64   `json:"weight_5h_override"`
	Weight7d     *float64   `json:"weight_7d_override"`
}

// usageTierWeight maps a normalized tier to its size in Pro-equivalents.
func usageTierWeight(tier string) (float64, bool) {
	switch tier {
	case UsageTierPro:
		return 1, true
	case UsageTierMax5x:
		return 5, true
	case UsageTierMax20x:
		return 20, true
	}
	return 0, false
}

// weight is the account's effective size in x for the window: the admin override,
// else the detected tier's preset, else 1.
func (c UsageAccountCapacity) weight(w string) float64 {
	override := c.Weight7d
	if w == UsageWindow5h {
		override = c.Weight5h
	}
	if override != nil && *override > 0 {
		return *override
	}
	if c.DetectedTier != nil {
		if v, ok := usageTierWeight(*c.DetectedTier); ok {
			return v
		}
	}
	return 1
}

// UsageQuotaSubscription is a live subscription with its effective quota.
type UsageQuotaSubscription struct {
	ID        int64
	UserID    int64
	GroupID   int64
	GroupName string
	Email     string
	Username  string
	ExpiresAt time.Time
	Quota     UsageQuota
	Source    string // plan / admin / group / none
	PlanID    *int64
}

// UsageShareStore is implemented by the account repository.
type UsageShareStore interface {
	ListUsageShareAccountStates(ctx context.Context) ([]UsageShareAccountState, error)
	LatestUsageSamples(ctx context.Context) ([]UsageSample, error)
	UsageCostByUser(ctx context.Context, accountID int64, from, to time.Time) ([]UsageUserCost, error)
	RecordUsageSegment(ctx context.Context, sample UsageSample, segmentStart time.Time, attributions []UsageAttribution, estimated bool) error
	ListCurrentUsageAttributions(ctx context.Context, now time.Time) ([]UsageAttributionSum, error)
	PruneUsageShare(ctx context.Context, before time.Time) error

	GetUsageQuotaSettings(ctx context.Context) (UsageQuotaSettings, error)
	SaveUsageQuotaSettings(ctx context.Context, s UsageQuotaSettings) error
	ListAccountCapacities(ctx context.Context) (map[int64]UsageAccountCapacity, error)
	SaveAccountWeights(ctx context.Context, accountID int64, weight5h, weight7d *float64, redetect bool) error
	SaveDetectedTier(ctx context.Context, accountID int64, tier, detectErr *string, at time.Time) error
	ListUsageShareGroupAccounts(ctx context.Context) (map[int64][]int64, error)
	ListQuotaSubscriptions(ctx context.Context) ([]UsageQuotaSubscription, error)
	GetGroupQuotaDefault(ctx context.Context, groupID int64) (*UsageQuota, error)
	SaveGroupQuotaDefault(ctx context.Context, groupID int64, q *UsageQuota) error
	SaveSubscriptionQuota(ctx context.Context, subscriptionID int64, q *UsageQuota) error
	ListPlanQuotas(ctx context.Context) (map[int64]UsageQuota, error)
	SavePlanQuota(ctx context.Context, planID int64, q *UsageQuota) error
	ApplyPlanQuota(ctx context.Context, planID, orderID, userID, groupID int64) (bool, error)
}

// splitUsageDelta divides a utilization rise across (user, group) pairs by cost,
// falling back to request count when all costs are zero, or to outside usage when idle.
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
		var groupID *int64
		if c.GroupID != nil {
			g := *c.GroupID
			groupID = &g
		}
		out = append(out, UsageAttribution{UserID: &userID, GroupID: groupID, Delta: delta * weight})
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

// --- snapshot ----------------------------------------------------------------

// usageKey identifies who used capacity; user 0 = outside sub2api, group 0 = no group.
type usageKey struct {
	userID  int64
	groupID int64
}

type usageContribution struct {
	resetAt time.Time
	x       float64
}

// usageView is one (user, group)'s current usage in x.
type usageView struct {
	used    map[string]float64
	contrib map[string][]usageContribution
}

type usageAccountView struct {
	name   string
	util   map[string]float64
	reset  map[string]time.Time
	weight map[string]float64
	cap    UsageAccountCapacity
}

type usageSubView struct {
	sub   UsageQuotaSubscription
	usage *usageView
}

type usageShareSnapshot struct {
	at            time.Time
	settings      UsageQuotaSettings
	accounts      map[int64]*usageAccountView
	groupAccounts map[int64][]int64
	usage         map[usageKey]*usageView
	subs          map[int64]*usageSubView
	byUserGroup   map[usageKey]*usageSubView
	// account -> window -> (user, group) -> attributed fraction of the window
	current map[int64]map[string]map[usageKey]float64
}

func (s *usageShareSnapshot) account(id int64) *usageAccountView {
	if a := s.accounts[id]; a != nil {
		return a
	}
	return &usageAccountView{util: map[string]float64{}, reset: map[string]time.Time{}, weight: map[string]float64{UsageWindow5h: 1, UsageWindow7d: 1}}
}

func (s *usageShareSnapshot) subFor(userID, groupID int64) *usageSubView {
	return s.byUserGroup[usageKey{userID: userID, groupID: groupID}]
}

func (s *usageShareSnapshot) capacity(groupID int64, w string) float64 {
	var total float64
	for _, id := range s.groupAccounts[groupID] {
		total += s.account(id).weight[w]
	}
	return total
}

type usageShareEngine struct {
	store     UsageShareStore
	now       func() time.Time
	mu        sync.Mutex
	last      map[string]*UsageSample
	loaded    bool
	names     map[int64]string
	types     map[int64]string
	lastPrune time.Time
	snapshot  atomic.Pointer[usageShareSnapshot]
	detecting atomic.Bool
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
	engine := &usageShareEngine{store: store, now: time.Now, last: map[string]*UsageSample{}, names: map[int64]string{}, types: map[int64]string{}}
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
		e.startTierDetection()
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
		e.types[st.ID] = st.Type
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
	snap, err := e.buildSnapshot(ctx)
	if err != nil {
		return err
	}
	e.snapshot.Store(snap)
	return nil
}

// rebuild refreshes the snapshot after an admin change.
func (e *usageShareEngine) rebuild(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rebuildLocked(ctx)
}

func (e *usageShareEngine) buildSnapshot(ctx context.Context) (*usageShareSnapshot, error) {
	now := e.now()
	settings, err := e.store.GetUsageQuotaSettings(ctx)
	if err != nil {
		return nil, err
	}
	caps, err := e.store.ListAccountCapacities(ctx)
	if err != nil {
		return nil, err
	}
	groupAccounts, err := e.store.ListUsageShareGroupAccounts(ctx)
	if err != nil {
		return nil, err
	}
	subs, err := e.store.ListQuotaSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	sums, err := e.store.ListCurrentUsageAttributions(ctx, now)
	if err != nil {
		return nil, err
	}

	snap := &usageShareSnapshot{
		at:            now,
		settings:      settings,
		accounts:      map[int64]*usageAccountView{},
		groupAccounts: groupAccounts,
		usage:         map[usageKey]*usageView{},
		subs:          map[int64]*usageSubView{},
		byUserGroup:   map[usageKey]*usageSubView{},
		current:       map[int64]map[string]map[usageKey]float64{},
	}
	ensureAccount := func(id int64) *usageAccountView {
		if a := snap.accounts[id]; a != nil {
			return a
		}
		c := caps[id]
		c.AccountID = id
		a := &usageAccountView{name: e.names[id], util: map[string]float64{}, reset: map[string]time.Time{}, cap: c,
			weight: map[string]float64{UsageWindow5h: c.weight(UsageWindow5h), UsageWindow7d: c.weight(UsageWindow7d)}}
		snap.accounts[id] = a
		return a
	}
	for _, s := range e.last {
		a := ensureAccount(s.AccountID)
		if s.ResetAt.After(now) {
			a.util[s.Window] = s.Utilization
			a.reset[s.Window] = s.ResetAt
		}
	}
	for _, ids := range groupAccounts {
		for _, id := range ids {
			ensureAccount(id)
		}
	}

	for _, sum := range sums {
		acc, ok := snap.accounts[sum.AccountID]
		if !ok {
			continue
		}
		reset, ok := acc.reset[sum.Window]
		if !ok || !reset.Equal(sum.ResetAt) {
			continue // attribution of a finished window
		}
		var key usageKey
		if sum.UserID != nil {
			key.userID = *sum.UserID
			if sum.GroupID != nil {
				key.groupID = *sum.GroupID
			}
		}
		byWindow := snap.current[sum.AccountID]
		if byWindow == nil {
			byWindow = map[string]map[usageKey]float64{}
			snap.current[sum.AccountID] = byWindow
		}
		if byWindow[sum.Window] == nil {
			byWindow[sum.Window] = map[usageKey]float64{}
		}
		byWindow[sum.Window][key] += sum.Delta
		if key.userID == 0 {
			continue
		}
		u := snap.usage[key]
		if u == nil {
			u = &usageView{used: map[string]float64{}, contrib: map[string][]usageContribution{}}
			snap.usage[key] = u
		}
		x := sum.Delta * acc.weight[sum.Window]
		u.used[sum.Window] += x
		u.contrib[sum.Window] = append(u.contrib[sum.Window], usageContribution{resetAt: reset, x: x})
	}

	for _, s := range subs {
		key := usageKey{userID: s.UserID, groupID: s.GroupID}
		v := &usageSubView{sub: s, usage: snap.usage[key]}
		snap.subs[s.ID] = v
		snap.byUserGroup[key] = v
	}
	return snap, nil
}

// --- enforcement -------------------------------------------------------------

func (e *usageShareEngine) fresh() *usageShareSnapshot {
	snap := e.snapshot.Load()
	if snap == nil || e.now().Sub(snap.at) > usageShareStaleAfter {
		return nil // fail open when data is stale
	}
	return snap
}

func (v *usageSubView) used(w string) float64 {
	if v.usage == nil {
		return 0
	}
	return v.usage.used[w]
}

// exceeded returns the windows in which the subscription is at or over its quota.
func (v *usageSubView) exceeded() []string {
	var out []string
	for _, w := range usageWindowKinds {
		if q := v.sub.Quota.forWindow(w); q != nil && v.used(w) >= *q {
			out = append(out, w)
		}
	}
	return out
}

// accountHasSpare reports whether an over-quota subscription may still use the
// account because it has unused capacity that resets soon in every exceeded window.
func (s *usageShareSnapshot) accountHasSpare(accountID int64, windows []string, now time.Time) bool {
	acc := s.account(accountID)
	for _, w := range windows {
		reset, ok := acc.reset[w]
		if !ok {
			return false
		}
		minPct, within := s.settings.spareRule(w)
		if (1-acc.util[w])*100 < minPct || reset.Sub(now) > within {
			return false
		}
	}
	return true
}

func (s *usageShareSnapshot) groupHasSpare(groupID int64, windows []string, now time.Time) bool {
	for _, id := range s.groupAccounts[groupID] {
		if s.accountHasSpare(id, windows, now) {
			return true
		}
	}
	return false
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
	sub := snap.subFor(userID, groupID)
	if sub == nil {
		return true
	}
	over := sub.exceeded()
	if len(over) == 0 {
		return true
	}
	return snap.accountHasSpare(account.ID, over, e.now())
}

// ForkUsageShareBlocked reports whether the user must be rejected before
// scheduling: over their subscription quota with no group account that has
// spare capacity.
func ForkUsageShareBlocked(groupID, userID int64) (string, bool) {
	e := activeUsageShare.Load()
	if e == nil {
		return "", false
	}
	snap := e.fresh()
	if snap == nil {
		return "", false
	}
	sub := snap.subFor(userID, groupID)
	if sub == nil {
		return "", false
	}
	over := sub.exceeded()
	if len(over) == 0 {
		return "", false
	}
	now := e.now()
	if snap.groupHasSpare(groupID, over, now) {
		return "", false
	}
	return sub.blockMessage(over, now), true
}

// freesAt is when the subscription's usage in the window drops below its quota
// as account windows reset.
func (v *usageSubView) freesAt(window string, now time.Time) time.Time {
	q := v.sub.Quota.forWindow(window)
	if v.usage == nil || q == nil {
		return now
	}
	contrib := append([]usageContribution(nil), v.usage.contrib[window]...)
	sort.Slice(contrib, func(i, j int) bool { return contrib[i].resetAt.Before(contrib[j].resetAt) })
	used := v.used(window)
	for _, c := range contrib {
		used -= c.x
		if used < *q {
			return c.resetAt
		}
	}
	return now
}

func (v *usageSubView) freesAtAll(windows []string, now time.Time) time.Time {
	var until time.Time
	for _, w := range windows {
		if t := v.freesAt(w, now); t.After(until) {
			until = t
		}
	}
	return until
}

func usageWindowLabel(w string) string {
	if w == UsageWindow5h {
		return "5-hour"
	}
	return "weekly"
}

func (v *usageSubView) blockMessage(windows []string, now time.Time) string {
	parts := make([]string, 0, len(windows))
	for _, w := range windows {
		parts = append(parts, fmt.Sprintf("%s %.2fx of %.2fx", usageWindowLabel(w), v.used(w), *v.sub.Quota.forWindow(w)))
	}
	msg := "Usage quota reached (" + strings.Join(parts, ", ") + "; 1x = one Claude Pro account)."
	if until := v.freesAtAll(windows, now); until.After(now) {
		msg += " Frees up around " + until.In(time.Local).Format("Mon Jan 2 15:04 MST") + "."
	}
	return msg
}

// --- validation --------------------------------------------------------------

func validUsageX(v *float64, max float64) bool {
	return v == nil || (!math.IsNaN(*v) && *v >= 0 && *v <= max)
}

func validateUsageQuota(q *UsageQuota) error {
	if q == nil {
		return nil
	}
	if !validUsageX(q.Quota5hX, usageQuotaMaxX) || !validUsageX(q.Quota7dX, usageQuotaMaxX) {
		return infraerrors.BadRequest("INVALID_USAGE_QUOTA", fmt.Sprintf("quotas must be between 0 and %d x", usageQuotaMaxX))
	}
	return nil
}

func validateUsageQuotaSettings(s UsageQuotaSettings) error {
	if s.Spare5hMinPct < 0 || s.Spare5hMinPct > 100 || s.Spare7dMinPct < 0 || s.Spare7dMinPct > 100 ||
		s.Spare5hWithinMinutes < 0 || s.Spare7dWithinHours < 0 {
		return infraerrors.BadRequest("INVALID_USAGE_QUOTA_SETTINGS", "percentages must be 0-100 and durations >= 0")
	}
	return nil
}

func validateUsageWeights(w5, w7 *float64) error {
	ok := func(v *float64) bool { return v == nil || (*v > 0 && *v <= usageWeightMaxX) }
	if !ok(w5) || !ok(w7) {
		return infraerrors.BadRequest("INVALID_ACCOUNT_WEIGHT", fmt.Sprintf("weights must be > 0 and <= %d", usageWeightMaxX))
	}
	return nil
}
