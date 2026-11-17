package handler

// [fork] Usage share endpoints: status for end users and the status line, and
// admin endpoints for quotas, account weights and the spare-capacity rule.

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ForkKeyUsageShare returns the calling API key's usage in its group.
// GET /v1/usage-share (API key auth; used by the Claude Code status line)
func ForkKeyUsageShare(c *gin.Context) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.User == nil {
		response.Unauthorized(c, "Invalid API key")
		return
	}
	if apiKey.GroupID == nil {
		response.Success(c, nil)
		return
	}
	groupName := ""
	if apiKey.Group != nil {
		groupName = apiKey.Group.Name
	}
	status, err := service.ForkUsageShareStatusForGroup(c.Request.Context(), apiKey.User.ID, *apiKey.GroupID, groupName)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

// ForkUserUsageShare returns the logged-in user's usage per live subscription.
// GET /api/v1/user/usage-share
func ForkUserUsageShare(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	userID := subject.UserID
	statuses, err := service.ForkUsageShareStatuses(c.Request.Context(), &userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, statuses)
}

// ForkRegisterUsageShareAdminRoutes registers the admin usage share endpoints
// under /api/v1/admin/usage-share.
func ForkRegisterUsageShareAdminRoutes(admin *gin.RouterGroup) {
	g := admin.Group("/usage-share")
	g.GET("/users", forkAdminUsageShareStatuses)
	g.GET("/subscriptions", forkAdminUsageShareStatuses)
	g.PUT("/subscriptions/:id", forkAdminSetSubscriptionQuota)
	g.GET("/settings", forkAdminGetUsageQuotaSettings)
	g.PUT("/settings", forkAdminSetUsageQuotaSettings)
	g.PUT("/accounts/:id", forkAdminSetAccountWeights)
	g.GET("/plans", forkAdminListPlanQuotas)
	g.PUT("/plans/:id", forkAdminSetPlanQuota)
}

func forkParamID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid ID")
		return 0, false
	}
	return id, true
}

// GET /api/v1/admin/usage-share/subscriptions — every live subscription's usage and quota.
func forkAdminUsageShareStatuses(c *gin.Context) {
	statuses, err := service.ForkUsageShareStatuses(c.Request.Context(), nil)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, statuses)
}

// PUT /api/v1/admin/usage-share/subscriptions/:id {"quota_5h_x": 0.5, "quota_7d_x": 0.5} or {"clear": true}
func forkAdminSetSubscriptionQuota(c *gin.Context) {
	id, ok := forkParamID(c)
	if !ok {
		return
	}
	var req service.UsageQuotaInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := service.ForkSetSubscriptionQuota(c.Request.Context(), id, req); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, nil)
}

func forkAdminGetUsageQuotaSettings(c *gin.Context) {
	s, err := service.ForkGetUsageQuotaSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, s)
}

func forkAdminSetUsageQuotaSettings(c *gin.Context) {
	var req service.UsageQuotaSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	s, err := service.ForkSetUsageQuotaSettings(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, s)
}

// PUT /api/v1/admin/usage-share/accounts/:id {"weight_5h_override": 5, "weight_7d_override": null, "redetect": false}
func forkAdminSetAccountWeights(c *gin.Context) {
	id, ok := forkParamID(c)
	if !ok {
		return
	}
	var req service.UsageAccountWeightInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := service.ForkSetAccountWeights(c.Request.Context(), id, req); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, nil)
}

// GET /api/v1/admin/usage-share/plans — {plan_id: {quota_5h_x, quota_7d_x}}
func forkAdminListPlanQuotas(c *gin.Context) {
	quotas, err := service.ForkListPlanQuotas(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, quotas)
}

// PUT /api/v1/admin/usage-share/plans/:id {"quota_5h_x": 1, "quota_7d_x": 1} or {"clear": true}
func forkAdminSetPlanQuota(c *gin.Context) {
	id, ok := forkParamID(c)
	if !ok {
		return
	}
	var req service.UsageQuotaInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := service.ForkSetPlanQuota(c.Request.Context(), id, req); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, nil)
}
