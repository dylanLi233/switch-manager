// Package vlan defines vendor-neutral VLAN identifiers and normalized results.
package vlan

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	MinID     = 1
	MaxID     = 4094
	MaxName   = 64
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

type VLAN struct {
	ID   int    `json:"vlan_id"`
	Name string `json:"name"`
}

func ValidateID(id int) error {
	if id < MinID || id > MaxID {
		return fmt.Errorf("vlan_id must be between %d and %d", MinID, MaxID)
	}
	return nil
}

// NormalizeName validates a conservative vendor-neutral subset. Empty names
// are allowed for create/get/list because some devices support unnamed VLANs.
func NormalizeName(name string, required bool) (string, error) {
	if name != strings.TrimSpace(name) {
		return "", errors.New("VLAN name cannot have leading or trailing whitespace")
	}
	if name == "" {
		if required {
			return "", errors.New("VLAN name is required")
		}
		return "", nil
	}
	if len(name) > MaxName || !namePattern.MatchString(name) {
		return "", errors.New("VLAN name contains unsupported characters or exceeds 64 bytes")
	}
	return name, nil
}

func (v VLAN) Validate() error {
	if err := ValidateID(v.ID); err != nil {
		return err
	}
	_, err := NormalizeName(v.Name, false)
	return err
}
