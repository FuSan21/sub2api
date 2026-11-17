package service

// [fork] Usage share admin operations: group default quota and report, global
// spare rule, account weights, subscription and plan quotas, and copying a
// plan's quota onto the subscription it was bought for.

import (
	"context"
	"log/slog"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ErrUsageQuotaTargetNotFound is returned when the subscription, plan or account doesn't exist.
var ErrUsageQuotaTargetNotFound = infraerrors.NotFound("USAGE_QUOTA_TARGET_NOT_FOUND", "target not found")

// UsageShareGroupInput sets a group's default subscription quota (nil = none).
type UsageShareGroupInput struct {
	Default *UsageQuota `json:"default"`
}

// UsageAccountWeightInput overrides an account's size in x (nil = use the detected tier).
type UsageAccountWeightInput struct {
	Weight5h *float64 `json:"weight_5h_override"`
	Weight7d *float64 `json:"weight_7d_override"`
	Redetect bool     `json:"redetect"`
}

// UsageQuotaInput sets a subscription's or plan's quota; Clear removes it.
type UsageQuotaInput struct {
	UsageQuota
	Clear bool `json:"clear"`
}

func usageShareEngineForAdmin() (*usageShareEngine, error) {
	e := activeUsageShare.Load()
	if e == nil {
		return nil, errUsageShareUnavailable
	}
	return e, nil
}

func (s *adminServiceImpl) GetUsageShare(ctx context.Context, groupID int64) (*UsageShareReport, error) {
	if _, err := s.groupRepo.GetByIDLite(ctx, groupID); err != nil {
		return nil, err
	}
	e, err := usageShareEngineForAdmin()
	if err != nil {
		return nil, err
	}
	return e.report(ctx, groupID)
}

func (s *adminServiceImpl) SetUsageShare(ctx context.Context, groupID int64, input UsageShareGroupInput) (*UsageShareReport, error) {
	if _, err := s.groupRepo.GetByIDLite(ctx, groupID); err != nil {
		return nil, err
	}
	e, err := usageShareEngineForAdmin()
	if err != nil {
		return nil, err
	}
	if err := validateUsageQuota(input.Default); err != nil {
		return nil, err
	}
	if err := e.store.SaveGroupQuotaDefault(ctx, groupID, input.Default); err != nil {
		return nil, err
	}
	if err := e.rebuild(ctx); err != nil {
		return nil, err
	}
	return e.report(ctx, groupID)
}

// ForkGetUsageQuotaSettings returns the global spare-capacity rule.
func ForkGetUsageQuotaSettings(ctx context.Context) (*UsageQuotaSettings, error) {
	e, err := usageShareEngineForAdmin()
	if err != nil {
		return nil, err
	}
	s, err := e.store.GetUsageQuotaSettings(ctx)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ForkSetUsageQuotaSettings replaces the global spare-capacity rule.
func ForkSetUsageQuotaSettings(ctx context.Context, input UsageQuotaSettings) (*UsageQuotaSettings, error) {
	e, err := usageShareEngineForAdmin()
	if err != nil {
		return nil, err
	}
	if err := validateUsageQuotaSettings(input); err != nil {
		return nil, err
	}
	if err := e.store.SaveUsageQuotaSettings(ctx, input); err != nil {
		return nil, err
	}
	if err := e.rebuild(ctx); err != nil {
		return nil, err
	}
	return &input, nil
}

// ForkSetAccountWeights sets or clears an account's weight overrides; Redetect
// queues a fresh tier detection.
func ForkSetAccountWeights(ctx context.Context, accountID int64, input UsageAccountWeightInput) error {
	e, err := usageShareEngineForAdmin()
	if err != nil {
		return err
	}
	if err := validateUsageWeights(input.Weight5h, input.Weight7d); err != nil {
		return err
	}
	if err := e.store.SaveAccountWeights(ctx, accountID, input.Weight5h, input.Weight7d, input.Redetect); err != nil {
		return err
	}
	if input.Redetect {
		e.startTierDetection()
	}
	return e.rebuild(ctx)
}

// ForkSetSubscriptionQuota sets a subscription's own quota (source "admin") or,
// with Clear, falls back to the group default.
func ForkSetSubscriptionQuota(ctx context.Context, subscriptionID int64, input UsageQuotaInput) error {
	e, err := usageShareEngineForAdmin()
	if err != nil {
		return err
	}
	var q *UsageQuota
	if !input.Clear {
		q = &input.UsageQuota
		if err := validateUsageQuota(q); err != nil {
			return err
		}
	}
	if err := e.store.SaveSubscriptionQuota(ctx, subscriptionID, q); err != nil {
		return err
	}
	return e.rebuild(ctx)
}

// ForkListPlanQuotas returns every plan's quota keyed by plan ID.
func ForkListPlanQuotas(ctx context.Context) (map[int64]UsageQuota, error) {
	e, err := usageShareEngineForAdmin()
	if err != nil {
		return nil, err
	}
	return e.store.ListPlanQuotas(ctx)
}

// ForkSetPlanQuota sets (or with Clear removes) what buying the plan grants.
// Existing subscriptions are not changed; the quota is copied on purchase.
func ForkSetPlanQuota(ctx context.Context, planID int64, input UsageQuotaInput) error {
	e, err := usageShareEngineForAdmin()
	if err != nil {
		return err
	}
	var q *UsageQuota
	if !input.Clear {
		q = &input.UsageQuota
		if err := validateUsageQuota(q); err != nil {
			return err
		}
	}
	return e.store.SavePlanQuota(ctx, planID, q)
}

// forkApplyPlanQuota copies the purchased plan's quota onto the user's
// subscription. Called after subscription fulfillment commits; failures are
// logged only (the subscription then falls back to the group default).
func forkApplyPlanQuota(ctx context.Context, o *dbent.PaymentOrder, groupID int64) {
	e := activeUsageShare.Load()
	if e == nil || o == nil || o.PlanID == nil {
		return
	}
	applied, err := e.store.ApplyPlanQuota(ctx, *o.PlanID, o.ID, o.UserID, groupID)
	if err != nil {
		slog.Warn("fork_usage_share_apply_plan_quota_failed", "order_id", o.ID, "plan_id", *o.PlanID, "error", err)
		return
	}
	if applied {
		if err := e.rebuild(ctx); err != nil {
			slog.Warn("fork_usage_share_rebuild_failed", "error", err)
		}
	}
}
