package discover

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Resolve picks the best install for a version constraint: the highest
// discovered version whose version string matches constraint as a
// dot-boundary prefix ("8.2" matches 8.2.27 but not 8.20.1). It never falls
// back to a non-matching version.
func Resolve(installs []Install, constraint string) (Install, error) {
	if constraint == "" {
		return Install{}, fmt.Errorf("discover: empty php version constraint")
	}
	var matches []Install
	for _, in := range installs {
		if versionMatches(in.Version, constraint) {
			matches = append(matches, in)
		}
	}
	if len(matches) == 0 {
		return Install{}, fmt.Errorf("discover: no PHP install matches %q (found: %s)", constraint, summarize(installs))
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return compareVersions(matches[i].Version, matches[j].Version) > 0
	})
	return matches[0], nil
}

// versionMatches reports whether version satisfies constraint as a prefix on
// dot boundaries.
func versionMatches(version, constraint string) bool {
	if version == constraint {
		return true
	}
	return strings.HasPrefix(version, constraint+".")
}

// compareVersions compares dotted numeric versions: negative if a<b, zero if
// equal, positive if a>b. Missing segments count as zero (8.2 == 8.2.0).
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		av, bv := 0, 0
		if i < len(as) {
			av, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bv, _ = strconv.Atoi(bs[i])
		}
		if av != bv {
			return av - bv
		}
	}
	return 0
}

// summarize renders discovered versions for error messages.
func summarize(installs []Install) string {
	if len(installs) == 0 {
		return "none"
	}
	vs := make([]string, len(installs))
	for i, in := range installs {
		vs[i] = in.Version
	}
	return strings.Join(vs, ", ")
}
