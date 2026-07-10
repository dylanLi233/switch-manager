package authn

import (
	"context"
	"errors"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
)

// Service combines cryptographic token verification with local RBAC resolution.
type Service struct {
	verifier TokenVerifier
	users    auth.PrincipalRepository
}

// NewService creates an authentication service.
func NewService(verifier TokenVerifier, users auth.PrincipalRepository) (*Service, error) {
	if verifier == nil {
		return nil, errors.New("token verifier is required")
	}
	if users == nil {
		return nil, errors.New("principal repository is required")
	}
	return &Service{verifier: verifier, users: users}, nil
}

// Authenticate verifies the JWT, then resolves roles and permissions from the database.
func (s *Service) Authenticate(ctx context.Context, rawToken string) (auth.Principal, error) {
	if s == nil || s.verifier == nil || s.users == nil {
		return auth.Principal{}, apperror.Wrap(apperror.CodeUnauthorized, "", errors.New("authentication service is not initialized"))
	}
	identity, err := s.verifier.Verify(ctx, rawToken)
	if err != nil {
		return auth.Principal{}, err
	}
	principal, err := s.users.ResolveBySubject(ctx, identity.Subject)
	if err != nil {
		return auth.Principal{}, err
	}
	if principal.Subject != identity.Subject {
		return auth.Principal{}, apperror.Wrap(apperror.CodeInternalError, "", errors.New("principal subject mismatch"))
	}
	principal.ServiceActorID = identity.ServiceActorID
	if err := principal.Validate(); err != nil {
		return auth.Principal{}, apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	return principal, nil
}
