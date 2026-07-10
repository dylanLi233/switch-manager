package authn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/golang-jwt/jwt/v5"
)

func TestJWTVerifierAcceptsValidToken(t *testing.T) {
	t.Parallel()
	privateKey, publicPEM := testRSAKey(t, 2048)
	verifier, err := NewJWTVerifier(publicPEM, JWTConfig{
		Issuer: "ops-platform", Audience: "switch-manager", KeyID: "key-1",
		ClockSkew: time.Second, UsernameClaim: "preferred_username", ServiceActorClaim: "azp",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := signToken(t, privateKey, "key-1", jwt.MapClaims{
		"iss": "ops-platform", "aud": "switch-manager", "sub": "subject-1",
		"exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Add(-time.Second).Unix(),
		"preferred_username": "alice", "azp": "ops-web",
	})
	identity, err := verifier.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if identity.Subject != "subject-1" || identity.TokenUsername != "alice" || identity.ServiceActorID != "ops-web" {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestJWTVerifierRejectsInvalidClaimsAndSignature(t *testing.T) {
	t.Parallel()
	privateKey, publicPEM := testRSAKey(t, 2048)
	otherKey, _ := testRSAKey(t, 2048)
	verifier, err := NewJWTVerifier(publicPEM, JWTConfig{
		Issuer: "ops-platform", Audience: "switch-manager", KeyID: "key-1",
		UsernameClaim: "preferred_username",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tests := []struct {
		name   string
		key    *rsa.PrivateKey
		kid    string
		claims jwt.MapClaims
	}{
		{"wrong signature", otherKey, "key-1", jwt.MapClaims{"iss": "ops-platform", "aud": "switch-manager", "sub": "s", "exp": now.Add(time.Minute).Unix()}},
		{"wrong audience", privateKey, "key-1", jwt.MapClaims{"iss": "ops-platform", "aud": "other", "sub": "s", "exp": now.Add(time.Minute).Unix()}},
		{"expired", privateKey, "key-1", jwt.MapClaims{"iss": "ops-platform", "aud": "switch-manager", "sub": "s", "exp": now.Add(-time.Minute).Unix()}},
		{"missing expiration", privateKey, "key-1", jwt.MapClaims{"iss": "ops-platform", "aud": "switch-manager", "sub": "s"}},
		{"wrong kid", privateKey, "key-2", jwt.MapClaims{"iss": "ops-platform", "aud": "switch-manager", "sub": "s", "exp": now.Add(time.Minute).Unix()}},
		{"missing subject", privateKey, "key-1", jwt.MapClaims{"iss": "ops-platform", "aud": "switch-manager", "exp": now.Add(time.Minute).Unix()}},
		{"invalid username type", privateKey, "key-1", jwt.MapClaims{"iss": "ops-platform", "aud": "switch-manager", "sub": "s", "exp": now.Add(time.Minute).Unix(), "preferred_username": 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := signToken(t, tc.key, tc.kid, tc.claims)
			if _, err := verifier.Verify(context.Background(), raw); !apperror.IsCode(err, apperror.CodeUnauthorized) {
				t.Fatalf("Verify() error = %v", err)
			}
		})
	}
}

func TestJWTVerifierRejectsWeakKey(t *testing.T) {
	t.Parallel()
	_, publicPEM := testRSAKey(t, 1024)
	if _, err := NewJWTVerifier(publicPEM, JWTConfig{Issuer: "issuer", Audience: "audience"}); err == nil {
		t.Fatal("expected weak key rejection")
	}
}

func testRSAKey(t *testing.T, bits int) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func signToken(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	raw, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
