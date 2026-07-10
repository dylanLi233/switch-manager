package postgres

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/authn"
	"github.com/dylanLi233/switch-manager/internal/health"
	"github.com/dylanLi233/switch-manager/internal/transport/httpserver"
	"github.com/golang-jwt/jwt/v5"
)

func TestJWTToPostgreSQLRBACHTTPIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	runMigration(t, root, dsn, "down", "all")
	runMigration(t, root, dsn, "up")

	ctx := context.Background()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const userID = "00000000-0000-0000-0000-000000000301"
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO users(id, external_subject, username, status)
		VALUES ($1::uuid, 'e2e-subject', 'e2e-user', 'ACTIVE')`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO user_role_bindings(user_id, role_id, scope_type, scope_id)
		SELECT $1::uuid, id, 'GLOBAL', '' FROM roles WHERE name='VIEWER'`, userID); err != nil {
		t.Fatal(err)
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	verifier, err := authn.NewJWTVerifier(publicPEM, authn.JWTConfig{
		Issuer: "ops-platform", Audience: "switch-manager", KeyID: "e2e-key",
		UsernameClaim: "preferred_username", ServiceActorClaim: "azp",
	})
	if err != nil {
		t.Fatal(err)
	}
	authentication, err := authn.NewService(verifier, store.Repositories().Access)
	if err != nil {
		t.Fatal(err)
	}

	claims := jwt.MapClaims{
		"iss": "ops-platform", "aud": "switch-manager", "sub": "e2e-subject",
		"exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Add(-time.Second).Unix(),
		"preferred_username": "token-name-must-not-override-db", "azp": "ops-web",
		"roles": []string{"ADMIN"},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "e2e-key"
	raw, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}

	router := httpserver.NewAuthenticatedRouter(health.NewHandler(time.Second), 1024, authentication)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	request.Header.Set("Authorization", "Bearer "+raw)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Success bool `json:"success"`
		Data struct {
			UserID   string   `json:"user_id"`
			Username string   `json:"username"`
			Roles    []string `json:"roles"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.Data.UserID != userID || response.Data.Username != "e2e-user" {
		t.Fatalf("response=%+v", response)
	}
	if len(response.Data.Roles) != 1 || response.Data.Roles[0] != "VIEWER" {
		t.Fatalf("JWT role claim must be ignored; roles=%v", response.Data.Roles)
	}
}
