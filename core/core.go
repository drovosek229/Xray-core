// Package core provides an entry point to use Xray core functionalities.
//
// Xray makes it possible to accept incoming network connections with certain
// protocol, process the data, and send them through another connection with
// the same or a difference protocol on demand.
//
// It may be configured to work with multiple protocols at the same time, and
// uses the internal router to tunnel through different inbound and outbound
// connections.
package core

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/xtls/xray-core/common/serial"
)

var (
	// These bytes are preserved for upstream protocol compatibility and are used
	// by REALITY's session identifier path.
	Version_x byte = 26
	Version_y byte = 2
	Version_z byte = 6
)

var (
	ProductVersion_x byte = 1
	ProductVersion_y byte = 0
	ProductVersion_z byte = 0
)

var (
	build       = "Custom"
	productName = "internet core"
	codename    = "internet"
	intro       = "A fork-owned Xray core for the internet client."
)

func init() {
	// Manually injected
	if build != "Custom" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	var isDirty bool
	var foundBuild bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if len(setting.Value) < 7 {
				return
			}
			build = setting.Value[:7]
			foundBuild = true
		case "vcs.modified":
			isDirty = setting.Value == "true"
		}
	}
	if isDirty && foundBuild {
		build += "-dirty"
	}
}

// ProductName returns the product name for this fork's user-visible versioning.
func ProductName() string {
	return productName
}

// UpstreamVersion returns the upstream Xray compatibility version used by this fork.
func UpstreamVersion() string {
	return fmt.Sprintf("%v.%v.%v", Version_x, Version_y, Version_z)
}

// Version returns this fork's product version as a string, in the form of "x.y.z" where x, y and z are numbers.
// ".z" part may be omitted in regular releases.
func Version() string {
	return fmt.Sprintf("%v.%v.%v", ProductVersion_x, ProductVersion_y, ProductVersion_z)
}

// VersionStatement returns a list of strings representing the full version info.
func VersionStatement() []string {
	return []string{
		serial.Concat(ProductName(), " ", Version(), " (", codename, "; based on Xray ", UpstreamVersion(), ") ", build, " (", runtime.Version(), " ", runtime.GOOS, "/", runtime.GOARCH, ")"),
		intro,
	}
}
