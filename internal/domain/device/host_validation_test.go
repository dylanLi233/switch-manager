package device

import "testing"

func TestValidateHost(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"192.0.2.1", "2001:db8::1", "core-switch.example.internal", "switch-01"} {
		if err := ValidateHost(host); err != nil {
			t.Errorf("ValidateHost(%q) error = %v", host, err)
		}
	}
	for _, host := range []string{"", " https://example.com", "https://example.com", "host:22", "host/path", "bad host", "-bad.example", "bad-.example", "bad_name"} {
		if err := ValidateHost(host); err == nil {
			t.Errorf("ValidateHost(%q) expected error", host)
		}
	}
}
