package handler

// [fork] Usage share status endpoints for end users and the admin Users page.

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ForkKeyUsageShare returns the calling API key's usage share in its group.
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

// ForkUserUsageShare returns the logged-in user's usage share per group.
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

// ForkAdminUsageShareUsers returns every user's usage share per group.
// GET /api/v1/admin/usage-share/users
func ForkAdminUsageShareUsers(c *gin.Context) {
	statuses, err := service.ForkUsageShareStatuses(c.Request.Context(), nil)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, statuses)
}
