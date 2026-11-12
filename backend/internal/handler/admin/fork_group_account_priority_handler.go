package admin

// [fork] Per-group account priority overrides. Available in simple mode too.

import (
	"context"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type groupAccountPriorityAdmin interface {
	GetGroupAccountPriorities(ctx context.Context, groupID int64) ([]service.GroupAccountPriority, error)
	SetGroupAccountPriorities(ctx context.Context, groupID int64, updates []service.GroupAccountPriorityUpdate) ([]service.GroupAccountPriority, error)
}

// SetGroupAccountPrioritiesRequest replaces all overrides of the group.
type SetGroupAccountPrioritiesRequest struct {
	Items []service.GroupAccountPriorityUpdate `json:"items"`
}

func (h *GroupHandler) groupAccountPriorityAdmin(c *gin.Context) (groupAccountPriorityAdmin, int64, bool) {
	svc, ok := h.adminService.(groupAccountPriorityAdmin)
	if !ok {
		response.InternalError(c, "group account priorities unsupported")
		return nil, 0, false
	}
	groupID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "Invalid group ID")
		return nil, 0, false
	}
	return svc, groupID, true
}

// GetGroupAccountPriorities lists accounts bound to the group with their
// account-level, overridden and effective priority.
// GET /api/v1/admin/groups/:id/account-priorities
func (h *GroupHandler) GetGroupAccountPriorities(c *gin.Context) {
	svc, groupID, ok := h.groupAccountPriorityAdmin(c)
	if !ok {
		return
	}
	items, err := svc.GetGroupAccountPriorities(c.Request.Context(), groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

// SetGroupAccountPriorities replaces the group's priority overrides.
// PUT /api/v1/admin/groups/:id/account-priorities
func (h *GroupHandler) SetGroupAccountPriorities(c *gin.Context) {
	svc, groupID, ok := h.groupAccountPriorityAdmin(c)
	if !ok {
		return
	}
	var req SetGroupAccountPrioritiesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	items, err := svc.SetGroupAccountPriorities(c.Request.Context(), groupID, req.Items)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}
