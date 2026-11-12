package admin

// [fork] Usage share settings and report. Available in simple mode too.

import (
	"context"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type usageShareAdmin interface {
	GetUsageShare(ctx context.Context, groupID int64) (*service.UsageShareReport, error)
	SetUsageShare(ctx context.Context, groupID int64, input service.UsageShareSettings) (*service.UsageShareReport, error)
}

func (h *GroupHandler) usageShareAdmin(c *gin.Context) (usageShareAdmin, int64, bool) {
	svc, ok := h.adminService.(usageShareAdmin)
	if !ok {
		response.InternalError(c, "usage share unsupported")
		return nil, 0, false
	}
	groupID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "Invalid group ID")
		return nil, 0, false
	}
	return svc, groupID, true
}

// GetUsageShare returns the group's usage share settings and each user's
// consumption of the account pool.
// GET /api/v1/admin/groups/:id/usage-share
func (h *GroupHandler) GetUsageShare(c *gin.Context) {
	svc, groupID, ok := h.usageShareAdmin(c)
	if !ok {
		return
	}
	report, err := svc.GetUsageShare(c.Request.Context(), groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, report)
}

// SetUsageShare replaces the group's usage share settings and per-user limits.
// PUT /api/v1/admin/groups/:id/usage-share
func (h *GroupHandler) SetUsageShare(c *gin.Context) {
	svc, groupID, ok := h.usageShareAdmin(c)
	if !ok {
		return
	}
	var req service.UsageShareSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	report, err := svc.SetUsageShare(c.Request.Context(), groupID, req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, report)
}
