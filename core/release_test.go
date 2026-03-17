package core

import (
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
