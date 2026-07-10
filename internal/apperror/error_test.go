package apperror

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestDefinitionsAreValid(t *testing.T) {
	t.Parallel()
	if len(definitions) != 37 {
		t.Fatalf("definitions=%d", len(definitions))
	}
	for code, def := range definitions {
		if strings.TrimSpace(string(code)) == "" || def.HTTPStatus < 400 || def.HTTPStatus > 599 || strings.TrimSpace(def.DefaultMessage) == "" {
			t.Fatalf("invalid definition %q: %+v", code, def)
		}
	}
}

func TestNewAndWrap(t *testing.T) {
	t.Parallel()
	cause := errors.New("secret database DSN")
	err := Wrap(CodeDatabaseUnavailable, "", cause)
	if err.Code != CodeDatabaseUnavailable || err.HTTPStatus != http.StatusServiceUnavailable || !err.Retryable {
		t.Fatalf("unexpected error: %+v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatal("wrapped cause unavailable")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatal("Error() leaked cause")
	}
}

func TestNormalizeUnknownError(t *testing.T) {
	t.Parallel()
	err := Normalize(errors.New("password=secret"))
	if err.Code != CodeInternalError || err.HTTPStatus != 500 || strings.Contains(err.Message, "secret") {
		t.Fatalf("unsafe normalize: %+v", err)
	}
}

func TestNormalizeRepairsStatus(t *testing.T) {
	t.Parallel()
	original := New(CodeForbidden, "custom")
	original.HTTPStatus = 200
	normalized := Normalize(original)
	if normalized.HTTPStatus != http.StatusForbidden || normalized.Message != "custom" {
		t.Fatalf("normalized=%+v", normalized)
	}
}

func TestUnknownCodeFallsBack(t *testing.T) {
	t.Parallel()
	err := New(Code("MADE_UP"), "unsafe custom")
	if err.Code != CodeInternalError || err.Message != "internal server error" {
		t.Fatalf("fallback=%+v", err)
	}
}

func TestWithDetailsDoesNotMutateOriginal(t *testing.T) {
	t.Parallel()
	original := New(CodeValidationError, "")
	clone := original.WithDetails(map[string]string{"field": "host"})
	if original.Details != nil || clone.Details == nil {
		t.Fatal("detail clone behavior invalid")
	}
}

func TestServerErrorsAlwaysUseGenericMessage(t *testing.T) {
	t.Parallel()
	err := New(CodeDatabaseUnavailable, "database password leaked")
	if err.Message != "database is unavailable" {
		t.Fatalf("message = %q", err.Message)
	}
	normalized := Normalize(err.WithDetails(map[string]string{"password": "secret"}))
	if normalized.Details != nil {
		t.Fatal("server error details must be removed")
	}
}
