package core_test

import (
	"strings"
	"testing"

	core "github.com/xtls/xray-core/core"
)

func TestForkVersioning(t *testing.T) {
	if got, want := core.ProductName(), "internet core"; got != want {
		t.Fatalf("unexpected product name: got %q want %q", got, want)
	}
	if got, want := core.Version(), "1.0.0"; got != want {
		t.Fatalf("unexpected product version: got %q want %q", got, want)
	}
	if got, want := core.UpstreamVersion(), "26.2.6"; got != want {
		t.Fatalf("unexpected upstream version: got %q want %q", got, want)
	}
}

func TestForkVersionStatementIncludesBaseVersion(t *testing.T) {
	statement := strings.Join(core.VersionStatement(), "\n")
	if !strings.Contains(statement, "internet core 1.0.0") {
		t.Fatalf("expected fork version in statement, got %q", statement)
	}
	if !strings.Contains(statement, "based on Xray 26.2.6") {
		t.Fatalf("expected upstream base version in statement, got %q", statement)
	}
}
