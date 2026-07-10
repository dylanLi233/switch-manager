package pluginapi

import "testing"

func TestVersionCompatibility(t *testing.T) {
	t.Parallel()
	runtime := Version{Major: 1, Minor: 2, Patch: 0}
	if !runtime.CompatibleWith(Version{Major: 1, Minor: 1, Patch: 9}) {
		t.Fatal("older same-major SDK must be compatible")
	}
	if runtime.CompatibleWith(Version{Major: 1, Minor: 3, Patch: 0}) {
		t.Fatal("newer SDK must not be compatible")
	}
	if runtime.CompatibleWith(Version{Major: 2, Minor: 0, Patch: 0}) {
		t.Fatal("different major SDK must not be compatible")
	}
}

func TestParseVersionStrict(t *testing.T) {
	t.Parallel()
	version, err := ParseVersion("1.2.3")
	if err != nil || version.String() != "1.2.3" {
		t.Fatalf("ParseVersion() = %v, %v", version, err)
	}
	for _, value := range []string{"1.2", "v1.2.3", "01.2.3", "0.0.0", "1.-1.0"} {
		if _, err := ParseVersion(value); err == nil {
			t.Fatalf("ParseVersion(%q) expected error", value)
		}
	}
}
