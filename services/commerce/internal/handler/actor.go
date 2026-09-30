package handler

import (
	"net/http"
	"strings"

	"github.com/ecommerce/services/commerce/internal/service"
)

// Header names used to carry the authenticated identity.
//
// In production these are set by the gateway after it has validated the token.
// The service trusts them because the gateway is the trust boundary: the
// service must not be reachable without it, or a client could simply send
// `X-Actor-Roles: platform_admin`.
const (
	HeaderActorID     = "X-Actor-Id"
	HeaderActorRoles  = "X-Actor-Roles"
	HeaderActorTeams  = "X-Actor-Teams"
	HeaderActorScopes = "X-Actor-Scopes"
)

// ActorFromRequest reconstructs the authenticated principal from request
// headers.
//
// A request with no identity headers becomes the zero Actor, which holds no
// roles and no scopes and therefore fails every permission check. That is the
// fail-closed default: an unauthenticated request gets read-only behaviour at
// most, and only where a policy explicitly allows it.
func ActorFromRequest(r *http.Request) service.Actor {
	return service.Actor{
		ID:     strings.TrimSpace(r.Header.Get(HeaderActorID)),
		Roles:  splitList(r.Header.Get(HeaderActorRoles)),
		Teams:  splitList(r.Header.Get(HeaderActorTeams)),
		Scopes: splitList(r.Header.Get(HeaderActorScopes)),
	}
}

// splitList parses a comma-separated header value, dropping empty entries.
func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
