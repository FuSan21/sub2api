package service

// [fork] This build is published as ghcr.io/fusan21/sub2api and versioned
// "<upstream>+fusan". Upstream's in-place binary update/rollback would swap in
// an upstream release binary, so it is disabled; updates come from the fork image.

import (
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var errForkSelfUpdateDisabled = infraerrors.Conflict("FORK_SELF_UPDATE_DISABLED",
	"this is the FuSan21/sub2api fork: updates arrive through the ghcr.io/fusan21/sub2api image (rebuilt daily after each upstream release), not the in-app updater")

// forkSelfUpdateBlocked is true for fork builds (version "<upstream>+fusan").
func (s *UpdateService) forkSelfUpdateBlocked() bool {
	return strings.HasSuffix(s.currentVersion, "+fusan")
}
