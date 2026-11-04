// Package web is the app's HTTP surface: it mounts the JSON API (package
// server, generated from the OpenAPI spec), the MCP endpoint and OAuth,
// serves the authoring tool (a React app, package ui, embedded in the
// binary), and the server-rendered pages that need no app: sign-in and the
// playtest.
package web

import (
	"embed"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/appmcp"
	"github.com/KevinMcHugh/rpg/internal/auth"
	"github.com/KevinMcHugh/rpg/internal/oauth"
	"github.com/KevinMcHugh/rpg/internal/server"
	"github.com/KevinMcHugh/rpg/internal/ui"
)

//go:embed templates/*.html
var templateFS embed.FS

// Server holds what the routes need.
type Server struct {
	Store  *app.Store
	Links  app.Links
	API    *server.Server // the JSON API, mounted at /api
	OAuth  *oauth.Handler
	Issuer string       // public origin, no trailing slash
	MCP    http.Handler // the streamable MCP handler, mounted at /mcp/rpc
	// Google turns on signing in with Google when it has a client id and
	// secret (docs/configuration.md).
	Google GoogleConfig

	pages map[string]*template.Template
}

// New assembles the server on store: the API and the MCP tools over the same
// endpoints, and OAuth issuing for issuer, which is also the origin of every
// link in a response.
func New(store *app.Store, issuer string) *Server {
	links := app.NewLinks(issuer)
	mcpServer := appmcp.NewServer(store, links)
	return &Server{
		Store:  store,
		Links:  links,
		API:    server.New(store, links),
		OAuth:  &oauth.Handler{Issuer: issuer, AppName: app.Name, Q: store.Queries, Invites: store},
		Issuer: issuer,
		MCP:    mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return mcpServer }, nil),
	}
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	s.pages = parsePages()
	q := s.Store.Queries
	denyAPI := auth.Unauthorized(s.Issuer)

	r := chi.NewRouter()
	r.Use(redactCodes)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	if s.Google.enabled() {
		s.OAuth.GoogleStart = "/auth/google"
		r.With(auth.Optional(q), pageHeaders).Get("/auth/google", s.googleStart)
		r.With(pageHeaders).Get("/auth/google/callback", s.googleCallback)
	}
	s.OAuth.Mount(r)
	// A game's invite link. Anyone may open it to see what it is for;
	// accepting it needs a person signed in.
	r.With(auth.Optional(q), pageHeaders).Get("/join/{code}", s.joinGET)
	r.With(s.sameOrigin, auth.Require(q, s.toLogin)).Post("/join/{code}", s.joinPOST)
	r.Get("/login", s.loginGET)
	r.With(s.sameOrigin).Post("/login", s.loginPOST)
	r.With(s.sameOrigin).Post("/logout", s.logout)

	// The JSON API decides per operation: the spec marks some operations
	// public, and everything else needs a credential. The authoring app
	// calls it with the session cookie, so writes must come from this
	// site too (a bearer-token client sends no Origin and passes).
	r.Group(func(r chi.Router) {
		r.Use(s.sameOrigin)
		s.API.Mount(r, auth.Require(q, denyAPI))
	})

	// The bare /mcp path is intercepted by the sprites.dev proxy (a POST
	// hangs before reaching the app), so MCP lives at /mcp/rpc.
	r.Group(func(r chi.Router) {
		r.Use(auth.Require(q, denyAPI))
		r.Handle("/mcp/rpc", s.MCP)
		r.Handle("/mcp/rpc/*", s.MCP)
	})

	// The authoring tool: a React app (internal/ui) that does everything
	// through /api. Its routes all serve the same page, behind a session,
	// since the content is unreleased. Its built files are public; they
	// hold no content.
	r.Handle("/assets/*", ui.Assets())
	r.Group(func(r chi.Router) {
		r.Use(auth.Require(q, s.toLogin))
		for _, path := range []string{
			"/", "/games", "/games/{id}", "/games/{id}/world", "/games/{id}/notes", "/games/{id}/build", "/games/{id}/members",
			"/characters/{id}", "/conversations/{id}", "/quests/{id}",
			// From before there were games; the app sends them to the first.
			"/world", "/notes", "/build",
		} {
			r.Get(path, ui.Index)
		}
		// Notes and build issues link to a node, choice or stage by id
		// alone; these find the page that holds it.
		r.Get("/nodes/{id}", s.nodeRedirect)
		r.Get("/choices/{id}", s.choiceRedirect)
		r.Get("/stages/{id}", s.stageRedirect)
	})

	// Playtests: the game itself, playing the content as it stands, with
	// saves kept per api key. An agent may play too; playing writes only
	// its own saves.
	r.Group(func(r chi.Router) {
		r.Use(auth.Require(q, s.toLogin))
		r.Use(s.sameOrigin)
		r.Use(pageHeaders)
		s.playtest().Routes(r)
	})
	return r
}

// ---- middleware -------------------------------------------------------------

// toLogin is the deny for pages: send the browser to /login and back.
func (s *Server) toLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
}

// sameOrigin refuses a state-changing request whose Origin header names
// another site. The session cookie is SameSite=Lax, which already keeps it
// off cross-site POSTs in current browsers; this is the second lock.
func (s *Server) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if o := r.Header.Get("Origin"); o != "" && o != "null" && o != s.Issuer && o != requestOrigin(r) {
				http.Error(w, "cross-origin request refused", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requestOrigin is the origin the request was addressed to, for local runs
// where PUBLIC_URL is not set to the address in the browser.
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// pageHeaders forbids every script on the server-rendered pages (sign-in
// and the playtest). Nothing on them needs one.
func pageHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// ---- login ------------------------------------------------------------------

func (s *Server) loginGET(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "login", map[string]any{"Next": safeNext(r.URL.Query().Get("next"))})
}

func (s *Server) loginPOST(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.FormValue("next"))
	raw := strings.TrimSpace(r.FormValue("api_key"))
	key, err := s.Store.GetAPIKeyByHash(r.Context(), auth.Hash(raw))
	if err != nil {
		status, msg := http.StatusUnauthorized, "That api key is not recognized."
		if !errors.Is(err, pgx.ErrNoRows) {
			status, msg = http.StatusInternalServerError, "Sign-in failed; try again."
		}
		s.render(w, r, status, "login", map[string]any{"Next": next, "Error": msg})
		return
	}
	if err := s.signIn(w, r, key.ID); err != nil {
		http.Error(w, "session failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.SessionCookie); err == nil {
		_ = s.OAuth.RevokeSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// safeNext keeps a post-login redirect on this site.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}
