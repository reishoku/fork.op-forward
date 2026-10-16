package version

import (
	"fmt"
	"strconv"
	"strings"
)

// Compare returns -1 if a < b, 0 if a == b, 1 if a > b.
// Versions are in "major.minor.patch" format (e.g., "0.3.0"), optionally
// prefixed with "v". A SemVer pre-release or build suffix ("-rc.1",
// "+build.5") is ignored, so only the numeric core is compared.
// Unparseable versions like "dev" are treated as 0.0.0.
func Compare(a, b string) int {
	ap, _ := parse(a)
	bp, _ := parse(b)
	for i := 0; i < 3; i++ {
		if ap[i] < bp[i] {
			return -1
		}
		if ap[i] > bp[i] {
			return 1
		}
	}
	return 0
}

// CheckCompatibility evaluates a client version against server and minimum versions.
// Returns:
//   - upgradeRequired: true if clientVersion < minVersion (hard block)
//   - updateAvailable: non-empty string with server version if clientVersion < serverVersion
//   - message: human-readable explanation for the client
func CheckCompatibility(clientVersion, serverVersion, minVersion string) (upgradeRequired bool, updateAvailable string, message string) {
	// A version that cannot be parsed ("dev", a custom build string) says
	// nothing about compatibility. Comparing it as 0.0.0 would put it below
	// every minimum and lock the client out, so it is exempt like "dev".
	if _, ok := parse(clientVersion); !ok {
		return false, "", ""
	}

	if Compare(clientVersion, minVersion) < 0 {
		return true, serverVersion, fmt.Sprintf(
			"client version %s is below minimum required version %s — please update with: op-forward update",
			clientVersion, minVersion,
		)
	}

	if Compare(clientVersion, serverVersion) < 0 {
		return false, serverVersion, ""
	}

	return false, "", ""
}

// parse extracts the numeric major.minor.patch core of v. Missing trailing
// components are zero. The second result is false, with a zero core, when v
// has no numeric core: it is empty, a component is not a non-negative
// integer, or there are more than three components.
func parse(v string) ([3]int, bool) {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) > 3 {
		return [3]int{}, false
	}
	var result [3]int
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		result[i] = n
	}
	return result, true
}
