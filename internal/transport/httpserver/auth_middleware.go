package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
)

const maxBearerTokenBytes = 16 * 1024

// Authenticator verifies a bearer token and resolves its local principal.
type Authenticator interface {
	Authenticate(context.Context, string) (auth.Principal, error)
}

type principalContextKey struct{}
type authorizedRoleContextKey struct{}
type sourceIPContextKey struct{}

// AuthenticationMiddleware validates one Authorization header and stores the principal.
func AuthenticationMiddleware(authenticator Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if authenticator == nil {
				WriteError(w, r, errors.New("authenticator is not configured"))
				return
			}
			values := r.Header.Values("Authorization")
			if len(values) != 1 {
				WriteError(w, r, apperror.New(apperror.CodeUnauthorized, ""))
				return
			}
			parts := strings.Fields(values[0])
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) == 0 || len(parts[1]) > maxBearerTokenBytes {
				WriteError(w, r, apperror.New(apperror.CodeUnauthorized, ""))
				return
			}
			principal, err := authenticator.Authenticate(r.Context(), parts[1])
			if err != nil {
				WriteError(w, r, err)
				return
			}
			if err := principal.Validate(); err != nil {
				WriteError(w, r, apperror.Wrap(apperror.CodeInternalError, "", err))
				return
			}
			ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
			ctx = context.WithValue(ctx, sourceIPContextKey{}, remoteIP(r.RemoteAddr))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ScopeResolver determines the exact resource scope for one request.
type ScopeResolver func(*http.Request) (auth.Scope, error)

// RequirePermission authorizes the authenticated principal for a request scope.
func RequirePermission(permission auth.Permission, resolve ScopeResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFromContext(r.Context())
			if !ok {
				WriteError(w, r, apperror.New(apperror.CodeUnauthorized, ""))
				return
			}
			if err := permission.Validate(); err != nil {
				WriteError(w, r, apperror.Wrap(apperror.CodeInternalError, "", err))
				return
			}
			if resolve == nil {
				WriteError(w, r, errors.New("scope resolver is not configured"))
				return
			}
			target, err := resolve(r)
			if err != nil {
				WriteError(w, r, err)
				return
			}
			role, allowed := principal.Authorize(permission, target)
			if !allowed {
				WriteError(w, r, apperror.New(apperror.CodeForbidden, ""))
				return
			}
			ctx := context.WithValue(r.Context(), authorizedRoleContextKey{}, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// PrincipalFromContext retrieves the authenticated principal.
func PrincipalFromContext(ctx context.Context) (auth.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(auth.Principal)
	return principal, ok
}

// AuthorizedRoleFromContext returns the role that granted the current permission.
func AuthorizedRoleFromContext(ctx context.Context) (auth.Role, bool) {
	role, ok := ctx.Value(authorizedRoleContextKey{}).(auth.Role)
	return role, ok
}

// ActorFromRequest creates an audit actor after authorization succeeded.
func ActorFromRequest(r *http.Request) (auth.Actor, error) {
	if r == nil {
		return auth.Actor{}, errors.New("request is required")
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		return auth.Actor{}, errors.New("authenticated principal is missing")
	}
	role, ok := AuthorizedRoleFromContext(r.Context())
	if !ok {
		return auth.Actor{}, errors.New("authorized role is missing")
	}
	sourceIP, _ := r.Context().Value(sourceIPContextKey{}).(string)
	actor := auth.Actor{
		UserID: principal.UserID, Username: principal.Username, Role: role,
		ServiceActorID: principal.ServiceActorID, SourceIP: sourceIP,
	}
	if err := actor.Validate(); err != nil {
		return auth.Actor{}, err
	}
	return actor, nil
}

// AuthMeHandler returns non-secret local identity and RBAC bindings for integration checks.
func AuthMeHandler(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		WriteError(w, r, apperror.New(apperror.CodeUnauthorized, ""))
		return
	}
	type bindingResponse struct {
		Role        auth.Role         `json:"role"`
		Scope       auth.Scope        `json:"scope"`
		Permissions []auth.Permission `json:"permissions"`
	}
	bindings := make([]bindingResponse, 0, len(principal.Bindings))
	for _, binding := range principal.Bindings {
		bindings = append(bindings, bindingResponse{Role: binding.Role, Scope: binding.Scope, Permissions: binding.Permissions})
	}
	response := map[string]any{
		"request_id": RequestIDFromContext(r.Context()),
		"success": true,
		"data": map[string]any{
			"user_id": principal.UserID,
			"username": principal.Username,
			"roles": principal.Roles(),
			"bindings": bindings,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(response)
}

func remoteIP(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return strings.TrimSpace(remoteAddr)
}
