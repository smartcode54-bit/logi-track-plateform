package app

import "runtime/debug"

// Version is set at build time with
// -ldflags "-X github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app.Version=<sha>".
// Without it, the VCS revision embedded by the Go toolchain is used.
var Version = ""

// BuildVersion returns Version, the short VCS revision, or "dev".
func BuildVersion() string {
	if Version != "" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		rev, dirty := "", false
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if len(rev) > 12 {
			rev = rev[:12]
		}
		if rev != "" {
			if dirty {
				return rev + "-dirty"
			}
			return rev
		}
	}
	return "dev"
}
