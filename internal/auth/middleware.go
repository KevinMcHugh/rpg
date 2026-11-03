package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
)

// SessionCookie holds a web session: an access token minted by /login,
// HttpOnly so page script never sees it.
const SessionCookie = "rpg_session"

// Resolve finds who a raw token acts as: an api key itself, or an OAuth
// access token (or web session) one approved. It returns pgx.ErrNoRows for
// a token that is unknown, expired, revoked, or whose key was deleted.
func Resolve(ctx context.Context, q *dbgen.Queries, token string) (Caller, error) {
	// The access-token prefix is the more specific, so it is checked first.
	if strings.HasPrefix(token, AccessTokenPrefix) {
		row, err := q.ResolveAccessToken(ctx, Hash(token))
		if err != nil {
			return Caller{}, err
		}
		// A web session (no client) is the person who signed in; a
		// connector token is the model they connected.
		return Caller{KeyID: row.ApiKeyID, Human: row.Kind == KindHuman && row.ClientID == nil}, nil
	}
	if !strings.HasPrefix(token, KeyPrefix) {
		return Caller{}, pgx.ErrNoRows
	}
	row, err := q.GetAPIKeyByHash(ctx, Hash(token))
	if err != nil {
		return Caller{}, err
	}
	_ = q.TouchAPIKey(ctx, row.ID) // best effort: a failed stamp must not fail the request
	return Caller{KeyID: row.ID, Human: row.Kind == KindHuman}, nil
}

// TokenFrom is the request's credential: the Authorization bearer, else the
// session cookie.
func TokenFrom(r *http.Request) string {
	if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if c, err := r.Cookie(SessionCookie); err == nil {
		return c.Value
	}
	return ""
}

// Require is middleware that resolves the request's credential and puts its
// caller on the context, or hands the request to deny. The REST API and MCP
// deny with a 401 that points at the OAuth discovery document (Unauthorized);
// web pages deny by sending the browser to /login.
func Require(q *dbgen.Queries, deny http.HandlerFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := TokenFrom(r)
			if token == "" {
				deny(w, r)
				return
			}
			caller, err := Resolve(r.Context(), q, token)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					deny(w, r)
					return
				}
				http.Error(w, "auth lookup failed", http.StatusInternalServerError)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithCaller(r.Context(), caller)))
		})
	}
}

// Optional is middleware for pages anyone may read: it puts the request's
// caller on the context when its credential is good, and otherwise lets the
// request through anonymous, so a stale session cookie just reads as signed
// out instead of locking a public page.
func Optional(q *dbgen.Queries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token := TokenFrom(r); token != "" {
				caller, err := Resolve(r.Context(), q, token)
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					http.Error(w, "auth lookup failed", http.StatusInternalServerError)
					return
				}
				if err == nil {
					r = r.WithContext(WithCaller(r.Context(), caller))
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Unauthorized is the deny for API and MCP routes: a 401 whose
// WWW-Authenticate header tells an MCP client where OAuth discovery lives,
// which is how claude.ai starts the connector flow.
func Unauthorized(issuer string) http.HandlerFunc {
	wwwAuth := `Bearer resource_metadata="` + issuer + `/.well-known/oauth-protected-resource"`
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", wwwAuth)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}
