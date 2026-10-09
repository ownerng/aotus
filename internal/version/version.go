// Package version holds the build version, set at link time with
//
//	-ldflags "-X aotus/internal/version.Version=v0.1.0"
package version

// Version is the product version; "dev" for local builds.
var Version = "dev"
