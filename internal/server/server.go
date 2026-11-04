package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"

	apigen "github.com/KevinMcHugh/rpg/internal/api/gen"
	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/auth"
	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
	"github.com/KevinMcHugh/rpg/internal/server/build"
	"github.com/KevinMcHugh/rpg/internal/server/characters"
	"github.com/KevinMcHugh/rpg/internal/server/conversations"
	"github.com/KevinMcHugh/rpg/internal/server/games"
	"github.com/KevinMcHugh/rpg/internal/server/me"
	"github.com/KevinMcHugh/rpg/internal/server/members"
	"github.com/KevinMcHugh/rpg/internal/server/notes"
	"github.com/KevinMcHugh/rpg/internal/server/world"
)

// BaseURL is where the API is mounted: the spec's servers entry.
const BaseURL = "/api"

// Store is everything the endpoints persist through. *app.Store satisfies
// it; each endpoint narrows it to its own interface. Add a method here when
// an endpoint needs a transaction that app.Store implements by hand.
type Store interface {
	dbgen.Querier
	InTx(ctx context.Context, fn func(q *dbgen.Queries) error) error
}

// Server implements apigen.StrictServerInterface by composing per-resource
// endpoints.
type Server struct {
	store Store
	links app.Links
}

// New builds the API on store, with links for the URLs in its responses.
func New(store Store, links app.Links) *Server {
	return &Server{store: store, links: links}
}

// Compile-time assertion: this fails to build until every operation in the
// spec has a method below.
var _ apigen.StrictServerInterface = (*Server)(nil)

// -----------------------------------------------------------------------------
// Mounting
// -----------------------------------------------------------------------------

// Mount attaches the API to r under BaseURL. requireAuth guards every
// operation except those the spec marks public with `security: []`, and an
// operation that changes something also needs a human caller unless the
// spec marks it `x-agent-writable: true`. So the spec, not the router,
// decides who may call what.
func (s *Server) Mount(r chi.Router, requireAuth func(http.Handler) http.Handler) {
	public := PublicOperations()
	humanOnly := HumanOnlyOperations()
	guard := func(next http.Handler) http.Handler {
		protected := requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The endpoints refuse an agent too (app.RequireHuman); refusing
			// here as well means an agent's 403 comes before any body
			// decoding, and an endpoint that forgot is still covered over
			// REST.
			if humanOnly[operation(r)] && !auth.IsHuman(r.Context()) {
				writeError(w, http.StatusForbidden, "only a person may change the game's content; an agent may leave a note instead")
				return
			}
			next.ServeHTTP(w, r)
		}))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if public[operation(r)] {
				// Anyone may read these, so any site may too. No
				// credentials are allowed cross-site, so this opens nothing
				// an anonymous caller could not already get.
				w.Header().Set("Access-Control-Allow-Origin", "*")
				next.ServeHTTP(w, r)
				return
			}
			protected.ServeHTTP(w, r)
		})
	}
	strict := apigen.NewStrictHandlerWithOptions(s, nil, apigen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: internalError,
	})
	r.Group(func(r chi.Router) {
		r.Use(limitBody)
		apigen.HandlerWithOptions(strict, apigen.ChiServerOptions{
			BaseURL:          BaseURL,
			BaseRouter:       r,
			Middlewares:      []apigen.MiddlewareFunc{guard},
			ErrorHandlerFunc: badRequest,
		})
	})
}

// operation is the matched route as the spec names it ("GET /notes/{noteId}").
func operation(r *http.Request) string {
	return r.Method + " " + strings.TrimPrefix(chi.RouteContext(r.Context()).RoutePattern(), BaseURL)
}

// PublicOperations is the set of operations ("GET /notes/{noteId}") whose
// spec entry overrides the global security with an empty list. An operation
// that does not mention security inherits the global requirement, so a new
// endpoint is private until the spec says otherwise.
func PublicOperations() map[string]bool {
	return operations(func(_ string, op *openapi3.Operation) bool {
		return op.Security != nil && len(*op.Security) == 0
	})
}

// HumanOnlyOperations is the set of operations only a human caller may
// make: every one that is not a GET, unless its spec entry says
// `x-agent-writable: true`. A new write is human-only until the spec says
// otherwise. See docs/authorship.md.
func HumanOnlyOperations() map[string]bool {
	return operations(func(method string, op *openapi3.Operation) bool {
		writable, _ := op.Extensions["x-agent-writable"].(bool)
		return method != http.MethodGet && !writable
	})
}

// AgentWritableOperations is the set of writes an agent may make.
func AgentWritableOperations() map[string]bool {
	return operations(func(method string, op *openapi3.Operation) bool {
		writable, _ := op.Extensions["x-agent-writable"].(bool)
		return method != http.MethodGet && writable
	})
}

// operations is the set of operations in the embedded spec that match.
func operations(match func(method string, op *openapi3.Operation) bool) map[string]bool {
	spec, err := apigen.GetSwagger()
	if err != nil {
		// The spec is embedded at build time, so this is a broken build.
		panic("embedded OpenAPI spec: " + err.Error())
	}
	out := map[string]bool{}
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			if match(method, op) {
				out[method+" "+path] = true
			}
		}
	}
	return out
}

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, app.MaxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

func badRequest(w http.ResponseWriter, _ *http.Request, err error) {
	writeError(w, http.StatusBadRequest, err.Error())
}

func internalError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("api", "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apigen.Error{Error: msg})
}

// -----------------------------------------------------------------------------
// Errors
// -----------------------------------------------------------------------------

func isNotFound(err error) bool { return errors.Is(err, app.ErrNotFound) }

// isForbidden is an agent caller trying to change the game's content.
func isForbidden(err error) bool { return errors.Is(err, app.ErrForbidden) }

// isUnprocessable is a request that parsed but cannot be done: a field out
// of bounds.
func isUnprocessable(err error) bool { return errors.Is(err, app.ErrBadInput) }

func notFound(err error) apigen.NotFoundJSONResponse {
	return apigen.NotFoundJSONResponse{Error: err.Error()}
}

func forbidden(err error) apigen.ForbiddenJSONResponse {
	return apigen.ForbiddenJSONResponse{Error: err.Error()}
}

func unprocessable(err error) apigen.UnprocessableJSONResponse {
	return apigen.UnprocessableJSONResponse{Error: err.Error()}
}

func bodyRequired() apigen.BadRequestJSONResponse {
	return apigen.BadRequestJSONResponse{Error: "body required"}
}

// -----------------------------------------------------------------------------
// Me
// -----------------------------------------------------------------------------

func (s *Server) GetMe(ctx context.Context, req apigen.GetMeRequestObject) (apigen.GetMeResponseObject, error) {
	return Run(ctx, me.Endpoint{Store: s.store}, req)
}

// -----------------------------------------------------------------------------
// Games
// -----------------------------------------------------------------------------

func (s *Server) ListGames(ctx context.Context, req apigen.ListGamesRequestObject) (apigen.ListGamesResponseObject, error) {
	return Run(ctx, games.ListEndpoint{Store: s.store, Links: s.links}, req)
}

func (s *Server) GetGame(ctx context.Context, req apigen.GetGameRequestObject) (apigen.GetGameResponseObject, error) {
	resp, err := Run(ctx, games.GetEndpoint{Store: s.store, Links: s.links}, req)
	if isNotFound(err) {
		return apigen.GetGame404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) CreateGame(ctx context.Context, req apigen.CreateGameRequestObject) (apigen.CreateGameResponseObject, error) {
	if req.Body == nil {
		return apigen.CreateGame400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, games.CreateEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.CreateGame403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isUnprocessable(err):
		return apigen.CreateGame422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) UpdateGame(ctx context.Context, req apigen.UpdateGameRequestObject) (apigen.UpdateGameResponseObject, error) {
	if req.Body == nil {
		return apigen.UpdateGame400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, games.UpdateEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.UpdateGame403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.UpdateGame404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.UpdateGame422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) DeleteGame(ctx context.Context, req apigen.DeleteGameRequestObject) (apigen.DeleteGameResponseObject, error) {
	resp, err := Run(ctx, games.DeleteEndpoint{Store: s.store}, req)
	switch {
	case isForbidden(err):
		return apigen.DeleteGame403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.DeleteGame404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

// -----------------------------------------------------------------------------
// Members and invites
// -----------------------------------------------------------------------------

func (s *Server) ListMembers(ctx context.Context, req apigen.ListMembersRequestObject) (apigen.ListMembersResponseObject, error) {
	resp, err := Run(ctx, members.ListEndpoint{Store: s.store}, req)
	if isNotFound(err) {
		return apigen.ListMembers404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) RemoveMember(ctx context.Context, req apigen.RemoveMemberRequestObject) (apigen.RemoveMemberResponseObject, error) {
	resp, err := Run(ctx, members.RemoveEndpoint{Store: s.store}, req)
	switch {
	case isForbidden(err):
		return apigen.RemoveMember403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.RemoveMember404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.RemoveMember422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) ListInvites(ctx context.Context, req apigen.ListInvitesRequestObject) (apigen.ListInvitesResponseObject, error) {
	resp, err := Run(ctx, members.ListInvitesEndpoint{Store: s.store}, req)
	switch {
	case isForbidden(err):
		return apigen.ListInvites403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.ListInvites404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) CreateInvite(ctx context.Context, req apigen.CreateInviteRequestObject) (apigen.CreateInviteResponseObject, error) {
	if req.Body == nil {
		return apigen.CreateInvite400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, members.CreateInviteEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.CreateInvite403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.CreateInvite404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.CreateInvite422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) DeleteInvite(ctx context.Context, req apigen.DeleteInviteRequestObject) (apigen.DeleteInviteResponseObject, error) {
	resp, err := Run(ctx, members.DeleteInviteEndpoint{Store: s.store}, req)
	switch {
	case isForbidden(err):
		return apigen.DeleteInvite403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.DeleteInvite404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

// -----------------------------------------------------------------------------
// Characters
// -----------------------------------------------------------------------------

func (s *Server) ListCharacters(ctx context.Context, req apigen.ListCharactersRequestObject) (apigen.ListCharactersResponseObject, error) {
	resp, err := Run(ctx, characters.ListEndpoint{Store: s.store, Links: s.links}, req)
	if isNotFound(err) {
		return apigen.ListCharacters404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) GetCharacter(ctx context.Context, req apigen.GetCharacterRequestObject) (apigen.GetCharacterResponseObject, error) {
	resp, err := Run(ctx, characters.GetEndpoint{Store: s.store, Links: s.links}, req)
	if isNotFound(err) {
		return apigen.GetCharacter404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) CreateCharacter(ctx context.Context, req apigen.CreateCharacterRequestObject) (apigen.CreateCharacterResponseObject, error) {
	if req.Body == nil {
		return apigen.CreateCharacter400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, characters.CreateEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.CreateCharacter403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.CreateCharacter404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.CreateCharacter422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) UpdateCharacter(ctx context.Context, req apigen.UpdateCharacterRequestObject) (apigen.UpdateCharacterResponseObject, error) {
	if req.Body == nil {
		return apigen.UpdateCharacter400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, characters.UpdateEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.UpdateCharacter403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.UpdateCharacter404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.UpdateCharacter422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) DeleteCharacter(ctx context.Context, req apigen.DeleteCharacterRequestObject) (apigen.DeleteCharacterResponseObject, error) {
	resp, err := Run(ctx, characters.DeleteEndpoint{Store: s.store}, req)
	switch {
	case isForbidden(err):
		return apigen.DeleteCharacter403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.DeleteCharacter404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

// -----------------------------------------------------------------------------
// Conversations, nodes and choices
// -----------------------------------------------------------------------------

func (s *Server) ListConversations(ctx context.Context, req apigen.ListConversationsRequestObject) (apigen.ListConversationsResponseObject, error) {
	resp, err := Run(ctx, conversations.ListEndpoint{Store: s.store, Links: s.links}, req)
	if isNotFound(err) {
		return apigen.ListConversations404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) GetConversation(ctx context.Context, req apigen.GetConversationRequestObject) (apigen.GetConversationResponseObject, error) {
	resp, err := Run(ctx, conversations.GetEndpoint{Store: s.store, Links: s.links}, req)
	if isNotFound(err) {
		return apigen.GetConversation404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) CreateConversation(ctx context.Context, req apigen.CreateConversationRequestObject) (apigen.CreateConversationResponseObject, error) {
	if req.Body == nil {
		return apigen.CreateConversation400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, conversations.CreateEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.CreateConversation403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.CreateConversation404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.CreateConversation422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) UpdateConversation(ctx context.Context, req apigen.UpdateConversationRequestObject) (apigen.UpdateConversationResponseObject, error) {
	if req.Body == nil {
		return apigen.UpdateConversation400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, conversations.UpdateEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.UpdateConversation403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.UpdateConversation404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.UpdateConversation422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) DeleteConversation(ctx context.Context, req apigen.DeleteConversationRequestObject) (apigen.DeleteConversationResponseObject, error) {
	resp, err := Run(ctx, conversations.DeleteEndpoint{Store: s.store}, req)
	switch {
	case isForbidden(err):
		return apigen.DeleteConversation403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.DeleteConversation404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) CreateNode(ctx context.Context, req apigen.CreateNodeRequestObject) (apigen.CreateNodeResponseObject, error) {
	if req.Body == nil {
		return apigen.CreateNode400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, conversations.CreateNodeEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.CreateNode403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.CreateNode404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.CreateNode422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) UpdateNode(ctx context.Context, req apigen.UpdateNodeRequestObject) (apigen.UpdateNodeResponseObject, error) {
	if req.Body == nil {
		return apigen.UpdateNode400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, conversations.UpdateNodeEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.UpdateNode403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.UpdateNode404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.UpdateNode422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) DeleteNode(ctx context.Context, req apigen.DeleteNodeRequestObject) (apigen.DeleteNodeResponseObject, error) {
	resp, err := Run(ctx, conversations.DeleteNodeEndpoint{Store: s.store}, req)
	switch {
	case isForbidden(err):
		return apigen.DeleteNode403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.DeleteNode404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) GetNodePath(ctx context.Context, req apigen.GetNodePathRequestObject) (apigen.GetNodePathResponseObject, error) {
	resp, err := Run(ctx, conversations.PathEndpoint{Store: s.store}, req)
	if isNotFound(err) {
		return apigen.GetNodePath404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) CreateChoice(ctx context.Context, req apigen.CreateChoiceRequestObject) (apigen.CreateChoiceResponseObject, error) {
	if req.Body == nil {
		return apigen.CreateChoice400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, conversations.CreateChoiceEndpoint{Store: s.store}, req)
	switch {
	case isForbidden(err):
		return apigen.CreateChoice403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.CreateChoice404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.CreateChoice422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) UpdateChoice(ctx context.Context, req apigen.UpdateChoiceRequestObject) (apigen.UpdateChoiceResponseObject, error) {
	if req.Body == nil {
		return apigen.UpdateChoice400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, conversations.UpdateChoiceEndpoint{Store: s.store}, req)
	switch {
	case isForbidden(err):
		return apigen.UpdateChoice403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.UpdateChoice404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.UpdateChoice422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) DeleteChoice(ctx context.Context, req apigen.DeleteChoiceRequestObject) (apigen.DeleteChoiceResponseObject, error) {
	resp, err := Run(ctx, conversations.DeleteChoiceEndpoint{Store: s.store}, req)
	switch {
	case isForbidden(err):
		return apigen.DeleteChoice403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.DeleteChoice404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

// -----------------------------------------------------------------------------
// World: flags, skills and quests
// -----------------------------------------------------------------------------

func (s *Server) ListFlags(ctx context.Context, req apigen.ListFlagsRequestObject) (apigen.ListFlagsResponseObject, error) {
	resp, err := Run(ctx, world.ListFlagsEndpoint{Store: s.store, Links: s.links}, req)
	if isNotFound(err) {
		return apigen.ListFlags404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) CreateFlag(ctx context.Context, req apigen.CreateFlagRequestObject) (apigen.CreateFlagResponseObject, error) {
	if req.Body == nil {
		return apigen.CreateFlag400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, world.CreateFlagEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.CreateFlag403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.CreateFlag404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.CreateFlag422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) UpdateFlag(ctx context.Context, req apigen.UpdateFlagRequestObject) (apigen.UpdateFlagResponseObject, error) {
	if req.Body == nil {
		return apigen.UpdateFlag400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, world.UpdateFlagEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.UpdateFlag403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.UpdateFlag404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.UpdateFlag422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) DeleteFlag(ctx context.Context, req apigen.DeleteFlagRequestObject) (apigen.DeleteFlagResponseObject, error) {
	resp, err := Run(ctx, world.DeleteFlagEndpoint{Store: s.store}, req)
	switch {
	case isForbidden(err):
		return apigen.DeleteFlag403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.DeleteFlag404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) ListSkills(ctx context.Context, req apigen.ListSkillsRequestObject) (apigen.ListSkillsResponseObject, error) {
	resp, err := Run(ctx, world.ListSkillsEndpoint{Store: s.store, Links: s.links}, req)
	if isNotFound(err) {
		return apigen.ListSkills404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) CreateSkill(ctx context.Context, req apigen.CreateSkillRequestObject) (apigen.CreateSkillResponseObject, error) {
	if req.Body == nil {
		return apigen.CreateSkill400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, world.CreateSkillEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.CreateSkill403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.CreateSkill404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.CreateSkill422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) UpdateSkill(ctx context.Context, req apigen.UpdateSkillRequestObject) (apigen.UpdateSkillResponseObject, error) {
	if req.Body == nil {
		return apigen.UpdateSkill400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, world.UpdateSkillEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.UpdateSkill403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.UpdateSkill404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.UpdateSkill422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) DeleteSkill(ctx context.Context, req apigen.DeleteSkillRequestObject) (apigen.DeleteSkillResponseObject, error) {
	resp, err := Run(ctx, world.DeleteSkillEndpoint{Store: s.store}, req)
	switch {
	case isForbidden(err):
		return apigen.DeleteSkill403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.DeleteSkill404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) ListQuests(ctx context.Context, req apigen.ListQuestsRequestObject) (apigen.ListQuestsResponseObject, error) {
	resp, err := Run(ctx, world.ListQuestsEndpoint{Store: s.store, Links: s.links}, req)
	if isNotFound(err) {
		return apigen.ListQuests404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) GetQuest(ctx context.Context, req apigen.GetQuestRequestObject) (apigen.GetQuestResponseObject, error) {
	resp, err := Run(ctx, world.GetQuestEndpoint{Store: s.store, Links: s.links}, req)
	if isNotFound(err) {
		return apigen.GetQuest404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) CreateQuest(ctx context.Context, req apigen.CreateQuestRequestObject) (apigen.CreateQuestResponseObject, error) {
	if req.Body == nil {
		return apigen.CreateQuest400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, world.CreateQuestEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.CreateQuest403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.CreateQuest404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.CreateQuest422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) UpdateQuest(ctx context.Context, req apigen.UpdateQuestRequestObject) (apigen.UpdateQuestResponseObject, error) {
	if req.Body == nil {
		return apigen.UpdateQuest400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, world.UpdateQuestEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.UpdateQuest403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.UpdateQuest404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.UpdateQuest422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) DeleteQuest(ctx context.Context, req apigen.DeleteQuestRequestObject) (apigen.DeleteQuestResponseObject, error) {
	resp, err := Run(ctx, world.DeleteQuestEndpoint{Store: s.store}, req)
	switch {
	case isForbidden(err):
		return apigen.DeleteQuest403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.DeleteQuest404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) CreateQuestStage(ctx context.Context, req apigen.CreateQuestStageRequestObject) (apigen.CreateQuestStageResponseObject, error) {
	if req.Body == nil {
		return apigen.CreateQuestStage400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, world.CreateStageEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.CreateQuestStage403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.CreateQuestStage404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.CreateQuestStage422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) UpdateQuestStage(ctx context.Context, req apigen.UpdateQuestStageRequestObject) (apigen.UpdateQuestStageResponseObject, error) {
	if req.Body == nil {
		return apigen.UpdateQuestStage400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, world.UpdateStageEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.UpdateQuestStage403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.UpdateQuestStage404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.UpdateQuestStage422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) DeleteQuestStage(ctx context.Context, req apigen.DeleteQuestStageRequestObject) (apigen.DeleteQuestStageResponseObject, error) {
	resp, err := Run(ctx, world.DeleteStageEndpoint{Store: s.store}, req)
	switch {
	case isForbidden(err):
		return apigen.DeleteQuestStage403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.DeleteQuestStage404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

// -----------------------------------------------------------------------------
// Notes
// -----------------------------------------------------------------------------

func (s *Server) ListNotes(ctx context.Context, req apigen.ListNotesRequestObject) (apigen.ListNotesResponseObject, error) {
	resp, err := Run(ctx, notes.ListEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isNotFound(err):
		return apigen.ListNotes404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.ListNotes422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) GetNote(ctx context.Context, req apigen.GetNoteRequestObject) (apigen.GetNoteResponseObject, error) {
	resp, err := Run(ctx, notes.GetEndpoint{Store: s.store, Links: s.links}, req)
	if isNotFound(err) {
		return apigen.GetNote404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

func (s *Server) CreateNote(ctx context.Context, req apigen.CreateNoteRequestObject) (apigen.CreateNoteResponseObject, error) {
	if req.Body == nil {
		return apigen.CreateNote400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, notes.CreateEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isNotFound(err):
		return apigen.CreateNote404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.CreateNote422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) UpdateNote(ctx context.Context, req apigen.UpdateNoteRequestObject) (apigen.UpdateNoteResponseObject, error) {
	if req.Body == nil {
		return apigen.UpdateNote400JSONResponse{BadRequestJSONResponse: bodyRequired()}, nil
	}
	resp, err := Run(ctx, notes.UpdateEndpoint{Store: s.store, Links: s.links}, req)
	switch {
	case isForbidden(err):
		return apigen.UpdateNote403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.UpdateNote404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	case isUnprocessable(err):
		return apigen.UpdateNote422JSONResponse{UnprocessableJSONResponse: unprocessable(err)}, nil
	}
	return resp, err
}

func (s *Server) DeleteNote(ctx context.Context, req apigen.DeleteNoteRequestObject) (apigen.DeleteNoteResponseObject, error) {
	resp, err := Run(ctx, notes.DeleteEndpoint{Store: s.store}, req)
	switch {
	case isForbidden(err):
		return apigen.DeleteNote403JSONResponse{ForbiddenJSONResponse: forbidden(err)}, nil
	case isNotFound(err):
		return apigen.DeleteNote404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}

// -----------------------------------------------------------------------------
// Build
// -----------------------------------------------------------------------------

func (s *Server) GetBuildReport(ctx context.Context, req apigen.GetBuildReportRequestObject) (apigen.GetBuildReportResponseObject, error) {
	resp, err := Run(ctx, build.Endpoint{Store: s.store, Links: s.links}, req)
	if isNotFound(err) {
		return apigen.GetBuildReport404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}
