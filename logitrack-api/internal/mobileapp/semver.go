package mobileapp

import (
	"regexp"
	"strconv"
	"strings"
)

// Driver-app version comparison: the Go port of logitrack-web/lib/mobileVersion.ts (ADR 0007), the single rule for
// "is this installation behind?" on the server. The inputs are MAJOR.MINOR.PATCH out of logitrack-mobile/pubspec.yaml,
// which the device compares with pub_semver; never compare them as strings ("2.10.0" < "2.9.3" lexicographically).

// Version is a parsed MAJOR.MINOR.PATCH triple.
type Version [3]int64

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER: parseSemver refuses a part beyond it (Number.isSafeInteger).
const maxSafeInteger = 1<<53 - 1

// semverRx is the shape parseSemver accepts after the trim and the "+build" cut: three runs of ASCII digits.
var semverRx = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// ParseVersion parses a MAJOR.MINOR.PATCH string, tolerating surrounding white space and a trailing "+build"
// suffix (pubspec writes 2.9.3+1), exactly as parseSemver does. Anything else ("v2.9.3", "2.9", "2.9.3.1", "",
// the "—" placeholder) is not a version: callers treat false as "unknown", never as zero.
func ParseVersion(raw string) (Version, bool) {
	s, _, _ := strings.Cut(strings.TrimSpace(raw), "+")
	if !semverRx.MatchString(s) {
		return Version{}, false
	}
	var v Version
	for i, p := range strings.Split(s, ".") {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n > maxSafeInteger {
			return Version{}, false
		}
		v[i] = n
	}
	return v, true
}

// Compare orders two versions: negative if v < w, 0 if equal, positive if v > w.
func (v Version) Compare(w Version) int {
	for i := range v {
		switch {
		case v[i] < w[i]:
			return -1
		case v[i] > w[i]:
			return 1
		}
	}
	return 0
}

// String is the canonical MAJOR.MINOR.PATCH spelling.
func (v Version) String() string {
	return strconv.FormatInt(v[0], 10) + "." + strconv.FormatInt(v[1], 10) + "." + strconv.FormatInt(v[2], 10)
}

// CompareVersions is compareSemver: the order of a and b, and false when either side does not parse (callers must
// handle "unknown" instead of a falsy 0 that reads as "equal").
func CompareVersions(a, b string) (int, bool) {
	va, ok := ParseVersion(a)
	if !ok {
		return 0, false
	}
	vb, ok := ParseVersion(b)
	if !ok {
		return 0, false
	}
	return va.Compare(vb), true
}

// VersionStatus is where one installation sits relative to the published release and the enforced floor.
type VersionStatus string

// The statuses of getVersionStatus.
const (
	// StatusBlocked: below minAllowedVersion; the app refuses to run right now.
	StatusBlocked VersionStatus = "blocked"
	// StatusOutdated: runnable, but behind the latest published build.
	StatusOutdated VersionStatus = "outdated"
	// StatusAhead: newer than what was published (usually a dev build loose in the fleet).
	StatusAhead VersionStatus = "ahead"
	// StatusCurrent: on the latest published build.
	StatusCurrent VersionStatus = "current"
	// StatusUnknown: the installed version, or the reference it is compared against, does not parse.
	StatusUnknown VersionStatus = "unknown"
)

// StatusOf is getVersionStatus: blocked is checked before outdated (an install can be both, and the operator needs
// the fact that means "this driver cannot work right now"); a missing floor or latest release skips its comparison
// instead of standing for version zero.
func StatusOf(installed, latest, minAllowed string) VersionStatus {
	if _, ok := ParseVersion(installed); !ok {
		return StatusUnknown
	}
	if c, ok := CompareVersions(installed, minAllowed); ok && c < 0 {
		return StatusBlocked
	}
	c, ok := CompareVersions(installed, latest)
	switch {
	case !ok:
		return StatusUnknown
	case c < 0:
		return StatusOutdated
	case c > 0:
		return StatusAhead
	}
	return StatusCurrent
}

// StatusCounts are the per-status totals of countVersionStatuses.
type StatusCounts struct {
	Current  int `json:"current"`
	Outdated int `json:"outdated"`
	Blocked  int `json:"blocked"`
	Ahead    int `json:"ahead"`
	Unknown  int `json:"unknown"`
}

// Add counts n installations of status s.
func (c *StatusCounts) Add(s VersionStatus, n int) {
	switch s {
	case StatusCurrent:
		c.Current += n
	case StatusOutdated:
		c.Outdated += n
	case StatusBlocked:
		c.Blocked += n
	case StatusAhead:
		c.Ahead += n
	default:
		c.Unknown += n
	}
}

// CountStatuses is countVersionStatuses: StatusOf tallied across installed versions.
func CountStatuses(installed []string, latest, minAllowed string) StatusCounts {
	var c StatusCounts
	for _, v := range installed {
		c.Add(StatusOf(v, latest, minAllowed), 1)
	}
	return c
}

// BlockedByFloor reports whether an installation of version installed would be locked out by a floor of target: only
// what can be proved (an unparseable version, or an unparseable target, is not counted, so the confirmation of a
// floor change never overstates its impact).
func BlockedByFloor(installed, target string) bool {
	c, ok := CompareVersions(installed, target)
	return ok && c < 0
}

// CountBlockedByFloor is countBlockedByFloor: how many installed versions a floor of target would lock out.
func CountBlockedByFloor(installed []string, target string) int {
	n := 0
	for _, v := range installed {
		if BlockedByFloor(v, target) {
			n++
		}
	}
	return n
}

// releaseVersionRx is the version of a published release and of a floor: canonical MAJOR.MINOR.PATCH without leading
// zeros or a build suffix (mobile_app_releases.version CHECK, the pub_semver gate), at most nine digits a part. The
// APK key embeds it, so "3.5.0" and "03.5.0" never name two objects of one version.
var releaseVersionRx = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

// ValidReleaseVersion reports whether s is a canonical release (or floor) version.
func ValidReleaseVersion(s string) bool { return releaseVersionRx.MatchString(s) }
