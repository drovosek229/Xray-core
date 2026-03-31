package core

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestVersionStatementIncludesReleaseTagWhenPresent(t *testing.T) {
	previous := releaseTag
	releaseTag = "0.0.2"
	t.Cleanup(func() {
		releaseTag = previous
	})

	statement := strings.Join(VersionStatement(), "\n")
	if !strings.Contains(statement, "release 0.0.2;") {
		t.Fatalf("expected release tag in statement, got %q", statement)
	}
	if !strings.Contains(statement, "internet core 1.0.0") {
		t.Fatalf("expected product version in statement, got %q", statement)
	}
}

func TestBuildMetadataFromInfoUsesSemverMainVersionAsReleaseTag(t *testing.T) {
	build, release := buildMetadataFromInfo(&debug.BuildInfo{
		Main: debug.Module{
			Version: "v1.260401.0",
		},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "4b84a0d6ed952fe4068b86e7c49bdc5b3175195c"},
			{Key: "vcs.modified", Value: "true"},
		},
	})

	if got, want := build, "4b84a0d-dirty"; got != want {
		t.Fatalf("unexpected build metadata: got %q want %q", got, want)
	}
	if got, want := release, "v1.260401.0"; got != want {
		t.Fatalf("unexpected release tag: got %q want %q", got, want)
	}
}

func TestBuildMetadataFromInfoIgnoresNonSemverMainVersion(t *testing.T) {
	build, release := buildMetadataFromInfo(&debug.BuildInfo{
		Main: debug.Module{
			Version: "(devel)",
		},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "4b84a0d6ed952fe4068b86e7c49bdc5b3175195c"},
		},
	})

	if got, want := build, "4b84a0d"; got != want {
		t.Fatalf("unexpected build metadata: got %q want %q", got, want)
	}
	if release != "" {
		t.Fatalf("expected empty release tag, got %q", release)
	}
}
