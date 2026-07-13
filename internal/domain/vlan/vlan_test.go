package vlan

import (
	"strings"
	"testing"
)

func TestValidateIDAndName(t *testing.T) {
	for _, id := range []int{1, 100, 4094} {
		if err := ValidateID(id); err != nil { t.Errorf("id=%d err=%v", id, err) }
	}
	for _, id := range []int{-1, 0, 4095} {
		if err := ValidateID(id); err == nil { t.Errorf("id=%d expected error", id) }
	}
	for _, name := range []string{"office", "Office 100", "guest_vlan", "lab.prod-1"} {
		if _, err := NormalizeName(name, true); err != nil { t.Errorf("name=%q err=%v", name, err) }
	}
	for _, name := range []string{"", " leading", "trailing ", "bad\nname", "bad\x00name", `bad"name`, strings.Repeat("a", 65)} {
		if _, err := NormalizeName(name, true); err == nil { t.Errorf("name=%q expected error", name) }
	}
}

func FuzzNormalizeName(f *testing.F) {
	for _, seed := range []string{"office", "", "bad\nname", "中文", strings.Repeat("x", 65), `quote"`} { f.Add(seed) }
	f.Fuzz(func(t *testing.T, value string) {
		name, err := NormalizeName(value, false)
		if err == nil && name != value { t.Fatalf("normalized value changed: %q -> %q", value, name) }
	})
}
