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
	"regexp"
	"runtime"
	"runtime/debug"

	"github.com/drovosek229/Xray-core/common/serial"
)

var (
	// These bytes are preserved for upstream protocol compatibility and are used
	// by REALITY's session identifier path.
	Version_x byte = 26
	Version_y byte = 3
	Version_z byte = 27
)

var (
	ProductVersion_x byte = 1
	ProductVersion_y byte = 0
	ProductVersion_z byte = 0
)

var (
	build       = "Custom"
	releaseTag  = ""
	productName = "internet core"
	codename    = "internet"
	intro       = "A fork-owned Xray core for the internet client."
)

var semverTagPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	detectedBuild, detectedReleaseTag := buildMetadataFromInfo(info)
	if build == "Custom" && detectedBuild != "" {
		build = detectedBuild
	}
	if releaseTag == "" && detectedReleaseTag != "" {
		releaseTag = detectedReleaseTag
	}
}

func buildMetadataFromInfo(info *debug.BuildInfo) (string, string) {
	if info == nil {
		return "", ""
	}

	var isDirty bool
	var foundBuild bool
	var build string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if len(setting.Value) < 7 {
				return "", releaseTagFromVersion(info.Main.Version)
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

	return build, releaseTagFromVersion(info.Main.Version)
}

func releaseTagFromVersion(version string) string {
	if !semverTagPattern.MatchString(version) {
		return ""
	}
	return version
}

// ProductName returns the product name for this fork's user-visible versioning.
func ProductName() string {
	return productName
}

// UpstreamVersion returns the upstream Xray compatibility version used by this fork.
func UpstreamVersion() string {
	return fmt.Sprintf("%v.%v.%v", Version_x, Version_y, Version_z)
}

// ReleaseTag returns the GitHub release tag embedded into the binary, if any.
func ReleaseTag() string {
	return releaseTag
}

// Version returns this fork's product version as a string, in the form of "x.y.z" where x, y and z are numbers.
// ".z" part may be omitted in regular releases.
func Version() string {
	return fmt.Sprintf("%v.%v.%v", ProductVersion_x, ProductVersion_y, ProductVersion_z)
}

// VersionStatement returns a list of strings representing the full version info.
func VersionStatement() []string {
	release := ""
	if tag := ReleaseTag(); tag != "" {
		release = "release " + tag + "; "
	}

	return []string{
		serial.Concat(ProductName(), " ", Version(), " (", release, codename, "; based on Xray ", UpstreamVersion(), ") ", build, " (", runtime.Version(), " ", runtime.GOOS, "/", runtime.GOARCH, ")"),
		intro,
	}
}
