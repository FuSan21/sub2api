package service

// [fork] Usage share: detect each Claude OAuth account's plan (Pro / Max 5x /
// Ultra) from /api/oauth/profile, so usage can be weighted in Pro-equivalents.
// Reuses ClaudeResetCreditService's token, proxy and header handling.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	usageTierRefreshAfter = 24 * time.Hour
	usageTierRetryAfter   = time.Hour
)

type usageTierDetector interface {
	forkDetectTier(ctx context.Context, accountID int64) (string, error)
}

var activeUsageTierDetector atomic.Pointer[ClaudeResetCreditService]

// ForkRegisterTierDetector lets the usage share engine query account profiles.
func ForkRegisterTierDetector(s *ClaudeResetCreditService) {
	if s != nil {
		activeUsageTierDetector.Store(s)
	}
}

// claudeProfileTier is the part of /api/oauth/profile that identifies the plan.
type claudeProfileTier struct {
	Account struct {
		HasClaudeMax bool `json:"has_claude_max"`
		HasClaudePro bool `json:"has_claude_pro"`
	} `json:"account"`
	Organization struct {
		OrganizationType string `json:"organization_type"`
		RateLimitTier    string `json:"rate_limit_tier"`
	} `json:"organization"`
}

// normalizeClaudeTier maps the profile to pro / max_5x / max_20x, or returns the
// raw values when the plan isn't recognized (weighted 1 until an admin overrides).
func normalizeClaudeTier(p claudeProfileTier) string {
	rate := strings.ToLower(p.Organization.RateLimitTier)
	org := strings.ToLower(p.Organization.OrganizationType)
	switch {
	case strings.Contains(rate, "max_20x"):
		return UsageTierMax20x
	case strings.Contains(rate, "max_5x"):
		return UsageTierMax5x
	case org == "claude_pro" || rate == "default_claude_ai" || (org == "" && p.Account.HasClaudePro && !p.Account.HasClaudeMax):
		return UsageTierPro
	}
	raw := strings.Trim(org+"/"+rate, "/")
	if raw == "" {
		return "unknown"
	}
	return raw
}

func (s *ClaudeResetCreditService) forkDetectTier(ctx context.Context, accountID int64) (string, error) {
	_, token, proxy, err := s.account(ctx, accountID)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, claudeResetProfileURL, nil)
	if err != nil {
		return "", err
	}
	s.headers(ctx, req, token)
	resp, err := s.do(req, proxy)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("profile returned HTTP %d", resp.StatusCode)
	}
	var body claudeProfileTier
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("decode profile: %w", err)
	}
	return normalizeClaudeTier(body), nil
}

// startTierDetection detects tiers of OAuth accounts that were never checked,
// whose result is older than a day, or whose last attempt failed over an hour ago.
// Runs in the background, one pass at a time.
func (e *usageShareEngine) startTierDetection() {
	detector := activeUsageTierDetector.Load()
	if detector == nil || !e.detecting.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer e.detecting.Store(false)
		e.detectTiers(detector)
	}()
}

func (e *usageShareEngine) detectTiers(detector usageTierDetector) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	caps, err := e.store.ListAccountCapacities(ctx)
	if err != nil {
		slog.Warn("fork_usage_tier_list_failed", "error", err)
		return
	}
	e.mu.Lock()
	ids := make([]int64, 0, len(e.types))
	for id, typ := range e.types {
		if typ == AccountTypeOAuth {
			ids = append(ids, id)
		}
	}
	e.mu.Unlock()

	now := e.now()
	changed := false
	for _, id := range ids {
		c, ok := caps[id]
		if ok && c.DetectedAt != nil {
			after := usageTierRefreshAfter
			if c.DetectError != nil {
				after = usageTierRetryAfter
			}
			if now.Sub(*c.DetectedAt) < after {
				continue
			}
		}
		reqCtx, reqCancel := context.WithTimeout(ctx, 30*time.Second)
		tier, err := detector.forkDetectTier(reqCtx, id)
		reqCancel()
		var tierPtr, errPtr *string
		if err != nil {
			msg := err.Error()
			errPtr = &msg
			tierPtr = c.DetectedTier // keep the previous result
			slog.Info("fork_usage_tier_detect_failed", "account_id", id, "error", err)
		} else {
			tierPtr = &tier
		}
		if err := e.store.SaveDetectedTier(ctx, id, tierPtr, errPtr, e.now()); err != nil {
			slog.Warn("fork_usage_tier_save_failed", "account_id", id, "error", err)
			continue
		}
		changed = true
	}
	if changed {
		if err := e.rebuild(ctx); err != nil {
			slog.Warn("fork_usage_share_rebuild_failed", "error", err)
		}
	}
}
