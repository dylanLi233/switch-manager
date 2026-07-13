package config

import "testing"

func TestLoadQueryLimits(t *testing.T) {
	limits, err := LoadQueryLimits(func(string) (string, bool) { return "", false })
	if err != nil || limits.ResultLimit != DefaultQueryResultLimit {
		t.Fatalf("limits=%+v err=%v", limits, err)
	}
	limits, err = LoadQueryLimits(func(key string) (string, bool) {
		if key == QueryResultLimitEnv {
			return "1234", true
		}
		return "", false
	})
	if err != nil || limits.ResultLimit != 1234 {
		t.Fatalf("limits=%+v err=%v", limits, err)
	}
}

func TestLoadQueryLimitsRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"0", "100001", "invalid"} {
		if _, err := LoadQueryLimits(func(string) (string, bool) { return value, true }); err == nil {
			t.Fatalf("value=%q expected error", value)
		}
	}
}
