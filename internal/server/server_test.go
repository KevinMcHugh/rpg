package server_test

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	apigen "github.com/KevinMcHugh/rpg/internal/api/gen"
	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/auth"
	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
	"github.com/KevinMcHugh/rpg/internal/server"
)

// fakeStore answers the few queries these tests reach from memory. Any
// other query panics through the nil Store, which is the point: a refused
// operation must be refused before it reaches the store.
type fakeStore struct {
	server.Store
	character dbgen.Character
	notes     []dbgen.Note
}

func (f *fakeStore) GetGame(_ context.Context, id string) (dbgen.Game, error) {
	if id != "g1" {
		return dbgen.Game{}, pgx.ErrNoRows
	}
	return dbgen.Game{ID: "g1", Key: "first", Name: "First", Ruleset: "classic"}, nil
}

// GameAccess has u1 writing in g1, which holds every character and note
// these tests reach.
func (f *fakeStore) GameAccess(_ context.Context, arg dbgen.GameAccessParams) (dbgen.GameAccessRow, error) {
	if arg.Kind == "game" && arg.TargetID != "g1" {
		return dbgen.GameAccessRow{}, pgx.ErrNoRows
	}
	role := ""
	if arg.UserID == "u1" {
		role = app.RoleWriter
	}
	return dbgen.GameAccessRow{GameID: "g1", Role: role}, nil
}

func (f *fakeStore) PageCharacters(context.Context, string, app.PageRequest) (app.Page[dbgen.Character], error) {
	return app.Page[dbgen.Character]{Items: []dbgen.Character{f.character}}, nil
}

func (f *fakeStore) GetCharacter(context.Context, string) (dbgen.Character, error) {
	return f.character, nil
}

func (f *fakeStore) CreateNote(_ context.Context, arg dbgen.CreateNoteParams) (dbgen.Note, error) {
	n := dbgen.Note{ID: arg.ID, GameID: arg.GameID, TargetKind: arg.TargetKind, TargetID: arg.TargetID, Kind: arg.Kind, Body: arg.Body, Status: "open", ByHuman: arg.ByHuman}
	f.notes = append(f.notes, n)
	return n, nil
}

func (f *fakeStore) GetNote(_ context.Context, id string) (dbgen.GetNoteRow, error) {
	name := "claude"
	for _, n := range f.notes {
		if n.ID == id {
			return dbgen.GetNoteRow{Note: n, AuthorName: &name}, nil
		}
	}
	panic("no note " + id)
}

// TestPublicOperations pins which operations anyone may call: none, since
// the content is unreleased. Update it when the spec marks an operation
// `security: []`, and only then.
func TestPublicOperations(t *testing.T) {
	if got := slices.Sorted(maps.Keys(server.PublicOperations())); len(got) != 0 {
		t.Fatalf("public operations %v, want none", got)
	}
}

// TestAgentWritableOperations pins the writes an agent may make. The game's
// text is written by people, so this is only ever notes. Update it when the
// spec marks a write `x-agent-writable: true`, and think twice first.
func TestAgentWritableOperations(t *testing.T) {
	got := slices.Sorted(maps.Keys(server.AgentWritableOperations()))
	want := []string{"POST /games/{gameId}/notes"}
	if !slices.Equal(got, want) {
		t.Fatalf("agent-writable operations %v, want %v", got, want)
	}
}

// TestHumanOnlyOperations checks that every write but the agent-writable
// ones needs a person, including writes added after this test was.
func TestHumanOnlyOperations(t *testing.T) {
	human, agent := server.HumanOnlyOperations(), server.AgentWritableOperations()
	for _, op := range []string{
		"POST /games", "PATCH /games/{gameId}", "DELETE /games/{gameId}",
		"POST /games/{gameId}/characters", "PATCH /characters/{characterId}", "DELETE /characters/{characterId}",
		"POST /games/{gameId}/conversations", "PATCH /conversations/{conversationId}", "DELETE /conversations/{conversationId}",
		"POST /conversations/{conversationId}/nodes", "PATCH /nodes/{nodeId}", "DELETE /nodes/{nodeId}",
		"POST /nodes/{nodeId}/choices", "PATCH /choices/{choiceId}", "DELETE /choices/{choiceId}",
		"PATCH /notes/{noteId}", "DELETE /notes/{noteId}",
		"POST /games/{gameId}/flags", "PATCH /flags/{flagId}", "POST /games/{gameId}/skills", "POST /games/{gameId}/quests",
		"POST /quests/{questId}/stages", "PATCH /stages/{stageId}", "DELETE /stages/{stageId}",
		"POST /games/{gameId}/invites", "DELETE /invites/{inviteId}", "DELETE /games/{gameId}/members/{userId}",
	} {
		if !human[op] {
			t.Errorf("%s is not human-only", op)
		}
	}
	for op := range human {
		if strings.HasPrefix(op, "GET ") || agent[op] {
			t.Errorf("%s is human-only but should not be", op)
		}
	}
}

// TestEveryPatchIsGuarded checks the stale-write contract on every PATCH,
// including ones added after this test was: the body requires the
// updatedAt the caller loaded, and the operation declares the 409 it gets
// when that is stale. Server's handlers run them with RunGuarded, so the
// endpoint behind each one has to name its Guard to compile.
func TestEveryPatchIsGuarded(t *testing.T) {
	spec, err := apigen.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for path, item := range spec.Paths.Map() {
		op := item.Patch
		if op == nil {
			continue
		}
		n++
		body := op.RequestBody.Value.Content.Get("application/json").Schema.Value
		if !slices.Contains(body.Required, "updatedAt") {
			t.Errorf("PATCH %s: the body does not require updatedAt", path)
		}
		if p := body.Properties["updatedAt"]; p == nil || p.Value.Format != "date-time" {
			t.Errorf("PATCH %s: updatedAt is not a date-time", path)
		}
		if op.Responses.Status(http.StatusConflict) == nil {
			t.Errorf("PATCH %s: no 409 response", path)
		}
	}
	if n == 0 {
		t.Fatal("no PATCH operations in the spec")
	}
}

// guardedNoTx is a Guarded endpoint that never opens a transaction, so
// nothing checks its guard.
type guardedNoTx struct{}

func (guardedNoTx) Interact(context.Context, string) (string, error) { return "wrote", nil }
func (guardedNoTx) Build(m string) string                            { return m }
func (guardedNoTx) Render(vm string) string                          { return vm }
func (guardedNoTx) Guard(string) app.Guard {
	return app.Guard{Kind: "character", ID: "c1", Seen: time.Now()}
}

type unguarded struct{ guardedNoTx }

func (unguarded) Guard() {} // not Guarded: the signature does not match

// A Guarded endpoint whose write skipped InTx fails closed on every
// surface, rather than writing with no comparison; others are untouched.
func TestUncheckedGuardFailsClosed(t *testing.T) {
	ctx := context.Background()
	if _, err := server.RunGuarded(ctx, guardedNoTx{}, "req"); err == nil {
		t.Fatal("Run passed a guard nothing checked")
	}
	if _, err := server.BuildViewModel(ctx, guardedNoTx{}, "req"); err == nil {
		t.Fatal("BuildViewModel passed a guard nothing checked")
	}
	if got, err := server.Run(ctx, unguarded{}, "req"); err != nil || got != "wrote" {
		t.Fatalf("an unguarded endpoint: %q, %v", got, err)
	}
}

// mount builds the API with a stand-in for auth: anonymous callers get a
// 401, and caller (if set) is put on every other request's context.
func mount(store server.Store, caller *auth.Caller) http.Handler {
	r := chi.NewRouter()
	requireAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if caller == nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithCaller(r.Context(), *caller)))
		})
	}
	server.New(store, app.NewLinks("https://app.example")).Mount(r, requireAuth)
	return r
}

type call struct {
	method, path, body string
	want               int
}

func (c call) run(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != c.want {
		t.Errorf("%s %s: %d, want %d: %s", c.method, c.path, rec.Code, c.want, rec.Body)
	}
	return rec
}

func TestAnonymousCallers(t *testing.T) {
	h := mount(&fakeStore{}, nil)
	for _, c := range []call{
		{"GET", "/api/games", "", 401},
		{"GET", "/api/games/g1/characters", "", 401},
		{"GET", "/api/conversations/c1", "", 401},
		{"GET", "/api/games/g1/notes", "", 401},
		{"GET", "/api/games/g1/build", "", 401},
		{"POST", "/api/games/g1/notes", `{"body":"x"}`, 401},
		{"POST", "/api/games/g1/characters", `{"name":"x"}`, 401},
		{"GET", "/api/me", "", 401},
	} {
		c.run(t, h)
	}
}

// An agent may read everything and leave notes, and nothing else. Its
// writes are refused before they reach the store (which would panic).
func TestAgentCallers(t *testing.T) {
	store := &fakeStore{character: dbgen.Character{ID: "c1", GameID: "g1", Name: "Aria"}}
	h := mount(store, &auth.Caller{KeyID: "k1", Human: false, UserID: "u1"})
	for _, c := range []call{
		{"POST", "/api/games", `{"name":"Second"}`, 403},
		{"POST", "/api/games/g1/characters", `{"name":"Mallory"}`, 403},
		{"PATCH", "/api/characters/c1", `{"name":"Mallory"}`, 403},
		{"DELETE", "/api/characters/c1", "", 403},
		{"POST", "/api/games/g1/conversations", `{"title":"x"}`, 403},
		{"POST", "/api/conversations/v1/nodes", `{"text":"Hello."}`, 403},
		{"PATCH", "/api/nodes/n1", `{"text":"Hello."}`, 403},
		{"POST", "/api/nodes/n1/choices", `{"text":"Goodbye."}`, 403},
		{"PATCH", "/api/choices/x1", `{"text":"Goodbye."}`, 403},
		{"PATCH", "/api/notes/n1", `{"status":"resolved"}`, 403},
		{"DELETE", "/api/notes/n1", "", 403},
		// A bad body is still a 403, not a 400: the guard runs first.
		{"POST", "/api/games/g1/characters", `not json`, 403},
		{"GET", "/api/games/g1/characters", "", 200},
		{"GET", "/api/games/nope/characters", "", 404},
	} {
		c.run(t, h)
	}

	rec := call{"POST", "/api/games/g1/notes", `{"targetKind":"character","targetId":"c1","kind":"gap","body":"Her bible never says what she wants."}`, 201}.run(t, h)
	var note struct {
		Kind, Body, URL string
		ByHuman         bool
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &note); err != nil {
		t.Fatal(err)
	}
	if note.Kind != "gap" || note.ByHuman || note.URL != "https://app.example/characters/c1" {
		t.Fatalf("agent note %+v", note)
	}
}

// The endpoints refuse an agent on their own, for the surfaces that do not
// pass the REST guard (MCP, and pages signed in with an agent key).
func TestEndpointsRefuseAgents(t *testing.T) {
	ctx := auth.WithCaller(context.Background(), auth.Caller{KeyID: "k1", UserID: "u1"})
	if err := app.RequireHuman(ctx); err == nil {
		t.Fatal("an agent passed RequireHuman")
	}
	if _, err := app.Revise(ctx, nil, "node", "n1", "text", "Hello."); err == nil {
		t.Fatal("an agent wrote a text revision")
	}
	human := auth.WithCaller(context.Background(), auth.Caller{KeyID: "k1", Human: true, UserID: "u1"})
	if err := app.RequireHuman(human); err != nil {
		t.Fatalf("a person was refused: %v", err)
	}
}

// Someone who is not in a game cannot tell it exists: every read and write
// of anything in it is a 404, the same as for something that does not exist.
func TestNonMembers(t *testing.T) {
	store := &fakeStore{character: dbgen.Character{ID: "c1", GameID: "g1", Name: "Aria"}}
	for _, human := range []bool{true, false} {
		h := mount(store, &auth.Caller{KeyID: "k2", Human: human, UserID: "u2"})
		for _, c := range []call{
			{"GET", "/api/games/g1", "", 404},
			{"GET", "/api/games/g1/characters", "", 404},
			{"GET", "/api/characters/c1", "", 404},
			{"GET", "/api/games/g1/members", "", 404},
			{"POST", "/api/games/g1/notes", `{"targetKind":"game","kind":"gap","body":"x"}`, 404},
		} {
			c.run(t, h)
		}
	}
	// A key with no user sees nothing either.
	h := mount(store, &auth.Caller{KeyID: "k3", Human: true})
	call{"GET", "/api/games/g1/characters", "", 404}.run(t, h)
	call{"GET", "/api/games", "", 200}.run(t, h)
}

// TestEveryListIsPaged checks the pagination contract on every list
// operation, including ones added after this test was: it takes limit and
// after, its body carries the next cursor, and it declares the 422 it gets
// for a bad limit or cursor. Server's handlers run them with RunPaged, so
// the endpoint behind each one has to page to compile.
func TestEveryListIsPaged(t *testing.T) {
	spec, err := apigen.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for path, item := range spec.Paths.Map() {
		op := item.Get
		if op == nil || !strings.HasPrefix(op.OperationID, "List") {
			continue
		}
		n++
		for _, name := range []string{"limit", "after"} {
			if op.Parameters.GetByInAndName("query", name) == nil {
				t.Errorf("GET %s: no %s parameter", path, name)
			}
		}
		body := op.Responses.Status(http.StatusOK).Value.Content.Get("application/json").Schema.Value
		if !slices.Contains(body.Required, "next") {
			t.Errorf("GET %s: the body has no next", path)
		}
		if op.Responses.Status(http.StatusUnprocessableEntity) == nil {
			t.Errorf("GET %s: no 422 response", path)
		}
	}
	if n == 0 {
		t.Fatal("no list operations in the spec")
	}
}

// overlong is a Paged endpoint that ignores the limit it was asked for.
type overlong struct{}

func (overlong) Interact(context.Context, int) (app.Page[int], error) {
	return app.Page[int]{Items: []int{1, 2, 3}}, nil
}
func (overlong) Build(p app.Page[int]) app.Page[int] { return p }
func (overlong) Render(p app.Page[int]) int          { return p.Len() }
func (overlong) Page(limit int) app.PageRequest      { return app.PageRequest{List: "n", Limit: limit} }

// A list endpoint that returns more than the caller asked for fails closed
// on every surface, rather than handing back a large list whole.
func TestOverlongPageFailsClosed(t *testing.T) {
	ctx := context.Background()
	if _, err := server.RunPaged(ctx, overlong{}, 2); err == nil {
		t.Fatal("Run passed a page of 3 for a limit of 2")
	}
	if _, err := server.BuildViewModel(ctx, overlong{}, 2); err == nil {
		t.Fatal("BuildViewModel passed a page of 3 for a limit of 2")
	}
	if n, err := server.RunPaged(ctx, overlong{}, 3); err != nil || n != 3 {
		t.Fatalf("a page within its limit: %d, %v", n, err)
	}
}
