package buildinfo

import (
	_ "embed"
	"strings"
)

//go:embed version.txt
var version string

//go:embed notices.txt
var Notices string

func Version() string { return strings.TrimSpace(version) }
