package buildinfo

import (
	_ "embed"
	"strings"
)

//go:embed version.txt
var version string

//go:embed notices.txt
var Notices string

// Distribution is set only by the release build. Development binaries never
// replace themselves, even when built with the same candidate version number.
var Distribution = "source"

func Version() string { return strings.TrimSpace(version) }
