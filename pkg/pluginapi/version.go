// Package pluginapi defines the stable contract between the switch-manager core
// and statically compiled vendor plugins.
package pluginapi

import (
	"fmt"
	"strconv"
	"strings"
)

// CurrentSDKVersionString is the immutable SDK version implemented by this binary.
const CurrentSDKVersionString = "1.0.0"

// CurrentSDKVersion returns a fresh value so plugins cannot mutate runtime compatibility state.
func CurrentSDKVersion() Version {
	return Version{Major: 1, Minor: 0, Patch: 0}
}

// Version is a strict semantic version without prerelease metadata.
type Version struct {
	Major int
	Minor int
	Patch int
}

// ParseVersion parses an exact MAJOR.MINOR.PATCH version.
func ParseVersion(value string) (Version, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("version %q must use MAJOR.MINOR.PATCH", value)
	}
	numbers := [3]int{}
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return Version{}, fmt.Errorf("version %q contains an invalid component", value)
		}
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return Version{}, fmt.Errorf("version %q contains an invalid component", value)
		}
		numbers[i] = number
	}
	version := Version{Major: numbers[0], Minor: numbers[1], Patch: numbers[2]}
	if err := version.Validate(); err != nil {
		return Version{}, err
	}
	return version, nil
}

// Validate rejects negative components and the unversioned 0.0.0 value.
func (v Version) Validate() error {
	if v.Major < 0 || v.Minor < 0 || v.Patch < 0 {
		return errorsVersion("version components cannot be negative")
	}
	if v == (Version{}) {
		return errorsVersion("version 0.0.0 is not allowed")
	}
	return nil
}

func errorsVersion(message string) error { return fmt.Errorf("invalid version: %s", message) }

// String returns MAJOR.MINOR.PATCH.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Compare returns -1, 0, or 1 when v is lower, equal, or higher than other.
func (v Version) Compare(other Version) int {
	left := [...]int{v.Major, v.Minor, v.Patch}
	right := [...]int{other.Major, other.Minor, other.Patch}
	for i := range left {
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	return 0
}

// CompatibleWith reports whether runtime can host a plugin built for required.
// A runtime supports the same major SDK and any required version not newer than
// itself. Major version changes are intentionally breaking.
func (runtime Version) CompatibleWith(required Version) bool {
	if runtime.Validate() != nil || required.Validate() != nil {
		return false
	}
	return runtime.Major == required.Major && required.Compare(runtime) <= 0
}
