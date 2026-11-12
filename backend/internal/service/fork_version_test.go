package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestForkVersionComparisonIgnoresBuildMetadata(t *testing.T) {
	require.Equal(t, 0, compareVersions("0.2.21+fusan", "v0.2.21"))
	require.Equal(t, -1, compareVersions("0.2.21+fusan", "v0.2.22"))
	require.Equal(t, 1, compareVersions("0.2.22+fusan", "v0.2.21"))
}

func TestForkSelfUpdateDisabled(t *testing.T) {
	s := &UpdateService{currentVersion: "0.2.21+fusan"}
	require.ErrorIs(t, s.PerformUpdate(context.Background()), errForkSelfUpdateDisabled)
	require.ErrorIs(t, s.Rollback(), errForkSelfUpdateDisabled)
	require.ErrorIs(t, s.RollbackToVersion(context.Background(), "0.2.20"), errForkSelfUpdateDisabled)
}
