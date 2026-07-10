// Package authn verifies upstream identities and resolves local principals.
package authn

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/golang-jwt/jwt/v5"
)

const maxJWTBytes = 16 * 1024

// JWTConfig controls strict verification of tokens issued by the operations platform.
type JWTConfig struct {
	Issuer            string
	Audience          string
	KeyID             string
	ClockSkew         time.Duration
	UsernameClaim     string
	ServiceActorClaim string
}

// Validate rejects incomplete or unsafe JWT settings.
func (c JWTConfig) Validate() error {
	if strings.TrimSpace(c.Issuer) == "" {
		return errors.New("JWT issuer is required")
	}
	if strings.TrimSpace(c.Audience) == "" {
		return errors.New("JWT audience is required")
	}
	if c.ClockSkew < 0 || c.ClockSkew > 5*time.Minute {
		return errors.New("JWT clock skew must be between 0 and 5 minutes")
	}
	if strings.ContainsAny(c.UsernameClaim, " \t\r\n") {
		return errors.New("JWT username claim name must not contain whitespace")
	}
	if strings.ContainsAny(c.ServiceActorClaim, " \t\r\n") {
		return errors.New("JWT service actor claim name must not contain whitespace")
	}
	return nil
}

// Identity is the result of cryptographic JWT verification. Roles are
// intentionally absent because authorization is resolved from PostgreSQL.
type Identity struct {
	Subject        string
	TokenUsername  string
	ServiceActorID string
}

// TokenVerifier verifies a bearer token and returns its upstream identity.
type TokenVerifier interface {
	Verify(context.Context, string) (Identity, error)
}

// JWTVerifier verifies RS256 JWTs with one configured RSA public key.
type JWTVerifier struct {
	parser            *jwt.Parser
	key               *rsa.PublicKey
	keyID             string
	usernameClaim     string
	serviceActorClaim string
}

// NewJWTVerifier parses the RSA public key and creates a strict verifier.
func NewJWTVerifier(publicKeyPEM []byte, cfg JWTConfig) (*JWTVerifier, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	key, err := parseRSAPublicKey(publicKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse JWT public key: %w", err)
	}
	if key.N.BitLen() < 2048 {
		return nil, errors.New("JWT RSA public key must be at least 2048 bits")
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(cfg.Issuer),
		jwt.WithAudience(cfg.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(cfg.ClockSkew),
		jwt.WithIssuedAt(),
	)
	return &JWTVerifier{
		parser: parser, key: key, keyID: cfg.KeyID,
		usernameClaim: cfg.UsernameClaim, serviceActorClaim: cfg.ServiceActorClaim,
	}, nil
}

// NewJWTVerifierFromFile reads a PEM public key from disk.
func NewJWTVerifierFromFile(path string, cfg JWTConfig) (*JWTVerifier, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("JWT public key file is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read JWT public key: %w", err)
	}
	return NewJWTVerifier(data, cfg)
}

// Verify validates signature, registered claims and configured custom claims.
func (v *JWTVerifier) Verify(ctx context.Context, raw string) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	if v == nil || v.parser == nil || v.key == nil {
		return Identity{}, apperror.Wrap(apperror.CodeUnauthorized, "", errors.New("JWT verifier is not initialized"))
	}
	if len(raw) == 0 || len(raw) > maxJWTBytes {
		return Identity{}, apperror.Wrap(apperror.CodeUnauthorized, "", errors.New("invalid JWT length"))
	}

	claims := jwt.MapClaims{}
	token, err := v.parser.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodRS256 {
			return nil, fmt.Errorf("unexpected JWT signing method %q", token.Method.Alg())
		}
		if v.keyID != "" {
			kid, ok := token.Header["kid"].(string)
			if !ok || kid != v.keyID {
				return nil, errors.New("JWT key ID does not match")
			}
		}
		return v.key, nil
	})
	if err != nil || token == nil || !token.Valid {
		if err == nil {
			err = errors.New("JWT is invalid")
		}
		return Identity{}, apperror.Wrap(apperror.CodeUnauthorized, "", err)
	}

	subject, ok := stringClaim(claims, "sub")
	if !ok || strings.TrimSpace(subject) == "" {
		return Identity{}, apperror.Wrap(apperror.CodeUnauthorized, "", errors.New("JWT subject is required"))
	}
	identity := Identity{Subject: subject}
	if v.usernameClaim != "" {
		if value, exists := claims[v.usernameClaim]; exists {
			username, ok := value.(string)
			if !ok || strings.TrimSpace(username) == "" {
				return Identity{}, apperror.Wrap(apperror.CodeUnauthorized, "", errors.New("JWT username claim is invalid"))
			}
			identity.TokenUsername = username
		}
	}
	if v.serviceActorClaim != "" {
		if value, exists := claims[v.serviceActorClaim]; exists {
			serviceActorID, ok := value.(string)
			if !ok || strings.TrimSpace(serviceActorID) == "" {
				return Identity{}, apperror.Wrap(apperror.CodeUnauthorized, "", errors.New("JWT service actor claim is invalid"))
			}
			identity.ServiceActorID = serviceActorID
		}
	}
	return identity, nil
}

func stringClaim(claims jwt.MapClaims, name string) (string, bool) {
	value, exists := claims[name]
	if !exists {
		return "", false
	}
	result, ok := value.(string)
	return result, ok
}

func parseRSAPublicKey(data []byte) (*rsa.PublicKey, error) {
	block, rest := pem.Decode(data)
	if block == nil {
		return nil, errors.New("PEM public key block not found")
	}
	if strings.TrimSpace(string(rest)) != "" {
		return nil, errors.New("unexpected trailing data after public key")
	}
	switch block.Type {
	case "PUBLIC KEY":
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		key, ok := parsed.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("public key is not RSA")
		}
		return key, nil
	case "RSA PUBLIC KEY":
		return x509.ParsePKCS1PublicKey(block.Bytes)
	case "CERTIFICATE":
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		key, ok := certificate.PublicKey.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("certificate public key is not RSA")
		}
		return key, nil
	default:
		return nil, fmt.Errorf("unsupported PEM block type %q", block.Type)
	}
}
