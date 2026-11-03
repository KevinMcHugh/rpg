package web_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/xid"

	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/appmcp"
	"github.com/KevinMcHugh/rpg/internal/auth"
	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
	"github.com/KevinMcHugh/rpg/internal/server/build"
	"github.com/KevinMcHugh/rpg/internal/server/characters"
	"github.com/KevinMcHugh/rpg/internal/server/conversations"
	"github.com/KevinMcHugh/rpg/internal/server/notes"
	"github.com/KevinMcHugh/rpg/internal/server/world"
	"github.com/KevinMcHugh/rpg/internal/web"
)

// The end-to-end tests need a Postgres they may wipe: point
// TEST_DATABASE_URL at a scratch database. Without it they skip, so
// `go test ./...` passes on a machine with no Postgres.
const dbEnv = "TEST_DATABASE_URL"

type env struct {
	t       *testing.T
	url     string
	key     string // a person's key
	keyID   string
	agent   string // an agent's key
	agentID string
	q       *dbgen.Queries
	pool    *pgxpool.Pool
	client  *http.Client // never follows redirects
}

func newEnv(t *testing.T) *env {
	dsn := os.Getenv(dbEnv)
	if dsn == "" {
		t.Skip(dbEnv + " not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	migrate(t, pool)

	ts := httptest.NewServer(nil)
	t.Cleanup(ts.Close)
	store := app.NewStore(pool)
	ts.Config.Handler = web.New(store, ts.URL).Handler()

	mint := func(name, kind string) (raw, id string) {
		raw, err := auth.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		key, err := store.CreateAPIKey(ctx, dbgen.CreateAPIKeyParams{ID: xid.New().String(), Name: name, KeyHash: auth.Hash(raw), Kind: kind})
		if err != nil {
			t.Fatal(err)
		}
		return raw, key.ID
	}
	e := &env{t: t, url: ts.URL, q: store.Queries, pool: pool, client: &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	e.key, e.keyID = mint("test", auth.KindHuman)
	e.agent, e.agentID = mint("claude", auth.KindAgent)
	return e
}

// migrate rebuilds the schema from the dbmate files' up sections.
func migrate(t *testing.T, pool *pgxpool.Pool) {
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("../../db/migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found: %v", err)
	}
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(b), "-- migrate:down")
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
}

// do sends a request with a bearer token (if any) and decodes a JSON reply
// into out (if non-nil), failing unless the status is want.
func (e *env) do(method, path, token string, body any, want int, out any) *http.Response {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.url+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		e.t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, want, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			e.t.Fatalf("%s %s: decode %s: %v", method, path, data, err)
		}
	}
	return resp
}

func TestAPIFlow(t *testing.T) {
	e := newEnv(t)

	resp := e.do("GET", "/api/me", "", nil, http.StatusUnauthorized, nil)
	if !strings.Contains(resp.Header.Get("WWW-Authenticate"), "/.well-known/oauth-protected-resource") {
		t.Fatalf("401 does not point at OAuth discovery: %q", resp.Header.Get("WWW-Authenticate"))
	}
	e.do("GET", "/api/me", auth.KeyPrefix+"not-a-key", nil, http.StatusUnauthorized, nil)
	e.do("GET", "/api/characters", "", nil, http.StatusUnauthorized, nil)

	var me struct {
		KeyID, Name string
		Human       bool
	}
	e.do("GET", "/api/me", e.key, nil, http.StatusOK, &me)
	if me.KeyID != e.keyID || me.Name != "test" || !me.Human {
		t.Fatalf("me %+v", me)
	}
	e.do("GET", "/api/me", e.agent, nil, http.StatusOK, &me)
	if me.Human {
		t.Fatalf("agent me %+v", me)
	}

	// A person writes a character and a small conversation.
	var aria characters.Character
	e.do("POST", "/api/characters", e.key, map[string]any{"name": "  Aria ", "bible": "Wants her brother back."}, http.StatusCreated, &aria)
	if aria.Name != "Aria" || aria.URL != e.url+"/characters/"+aria.ID {
		t.Fatalf("character %+v", aria)
	}
	e.do("POST", "/api/characters", e.key, map[string]any{"name": " "}, http.StatusUnprocessableEntity, nil)

	var g conversations.Graph
	e.do("POST", "/api/conversations", e.key, map[string]any{"title": "Homecoming"}, http.StatusCreated, &g)
	conv := g.Conversation.ID
	var n1, n2 conversations.Node
	e.do("POST", "/api/conversations/"+conv+"/nodes", e.key, map[string]any{"speakerId": aria.ID, "text": "You came back."}, http.StatusCreated, &n1)
	e.do("POST", "/api/conversations/"+conv+"/nodes", e.key, map[string]any{"text": "She looks away."}, http.StatusCreated, &n2)
	if n1.SpeakerName != "Aria" || n2.SpeakerName != "" {
		t.Fatalf("speakers %q %q", n1.SpeakerName, n2.SpeakerName)
	}
	e.do("POST", "/api/conversations/"+conv+"/nodes", e.key, map[string]any{"speakerId": "nobody", "text": "x"}, http.StatusUnprocessableEntity, nil)
	var c1 conversations.Choice
	e.do("POST", "/api/nodes/"+n1.ID+"/choices", e.key, map[string]any{"text": "Ask about her brother.", "toNodeId": n2.ID}, http.StatusCreated, &c1)
	e.do("POST", "/api/nodes/"+n1.ID+"/choices", e.key, map[string]any{"text": "Leave.", "sortOrder": 1}, http.StatusCreated, nil)

	// The first line became the start, and the graph reads back whole.
	e.do("GET", "/api/conversations/"+conv, e.key, nil, http.StatusOK, &g)
	if g.Conversation.StartNodeID != n1.ID || len(g.Nodes) != 2 || len(g.Nodes[0].Choices) != 2 || g.Nodes[0].Choices[0].ToNodeID != n2.ID {
		t.Fatalf("graph %+v", g)
	}
	var path conversations.Path
	e.do("GET", "/api/nodes/"+n2.ID+"/path", e.agent, nil, http.StatusOK, &path)
	if !path.Reachable || len(path.Steps) != 2 || path.Steps[0].ChoiceText != "Ask about her brother." || path.Steps[1].ChoiceID != "" {
		t.Fatalf("path %+v", path)
	}

	// A PATCH changes only what it sends; an empty speaker is the narrator.
	var upd conversations.Node
	e.do("PATCH", "/api/nodes/"+n1.ID, e.key, map[string]any{"text": "So. You came back."}, http.StatusOK, &upd)
	if upd.Text != "So. You came back." || upd.SpeakerName != "Aria" || len(upd.Choices) != 2 {
		t.Fatalf("updated node %+v", upd)
	}
	e.do("PATCH", "/api/nodes/"+n1.ID, e.key, map[string]any{"speakerId": ""}, http.StatusOK, &upd)
	if upd.SpeakerID != "" || upd.Text != "So. You came back." {
		t.Fatalf("narrated node %+v", upd)
	}

	// An agent reads everything, writes none of it, and leaves notes.
	for _, w := range []struct {
		method, path string
		body         map[string]any
	}{
		{"POST", "/api/characters", map[string]any{"name": "Mallory"}},
		{"PATCH", "/api/characters/" + aria.ID, map[string]any{"bible": "x"}},
		{"POST", "/api/conversations", map[string]any{"title": "x"}},
		{"POST", "/api/conversations/" + conv + "/nodes", map[string]any{"text": "A line Claude wrote."}},
		{"PATCH", "/api/nodes/" + n1.ID, map[string]any{"text": "A line Claude wrote."}},
		{"POST", "/api/nodes/" + n1.ID + "/choices", map[string]any{"text": "A choice Claude wrote."}},
		{"PATCH", "/api/choices/" + c1.ID, map[string]any{"text": "A choice Claude wrote."}},
		{"DELETE", "/api/nodes/" + n2.ID, nil},
	} {
		e.do(w.method, w.path, e.agent, w.body, http.StatusForbidden, nil)
	}
	var note notes.Note
	e.do("POST", "/api/notes", e.agent, map[string]any{"targetKind": "node", "targetId": n2.ID, "kind": "gap", "body": "Nothing here acknowledges the letter from act one."}, http.StatusCreated, &note)
	if note.ByHuman || note.AuthorName != "claude" || note.Status != "open" || note.URL != e.url+"/nodes/"+n2.ID {
		t.Fatalf("note %+v", note)
	}
	var reply notes.Note
	e.do("POST", "/api/notes", e.key, map[string]any{"parentId": note.ID, "body": "Good catch."}, http.StatusCreated, &reply)
	if reply.TargetID != n2.ID || reply.Kind != "gap" || !reply.ByHuman {
		t.Fatalf("reply %+v", reply)
	}
	e.do("POST", "/api/notes", e.agent, map[string]any{"targetKind": "node", "targetId": "nope", "body": "x"}, http.StatusNotFound, nil)
	e.do("POST", "/api/notes", e.agent, map[string]any{"kind": "rant", "body": "x"}, http.StatusUnprocessableEntity, nil)
	e.do("PATCH", "/api/notes/"+note.ID, e.agent, map[string]any{"status": "resolved"}, http.StatusForbidden, nil)
	e.do("PATCH", "/api/notes/"+note.ID, e.key, map[string]any{"status": "resolved"}, http.StatusOK, &note)
	var list struct{ Notes []notes.Note }
	e.do("GET", "/api/notes?status=open", e.agent, nil, http.StatusOK, &list)
	if len(list.Notes) != 1 || list.Notes[0].ID != reply.ID {
		t.Fatalf("open notes %+v", list.Notes)
	}

	// The content builds.
	var report build.Report
	e.do("GET", "/api/build", e.agent, nil, http.StatusOK, &report)
	if !report.OK || report.Stats.Nodes != 2 || report.Stats.Choices != 2 {
		t.Fatalf("report %+v", report)
	}

	// Deleting a node leaves the choice into it dangling, and the build says so.
	e.do("DELETE", "/api/nodes/"+n2.ID, e.key, nil, http.StatusNoContent, nil)
	e.do("DELETE", "/api/nodes/"+n2.ID, e.key, nil, http.StatusNotFound, nil)
	e.do("GET", "/api/build", e.key, nil, http.StatusOK, &report)
	if report.OK || len(report.Errors) != 1 || report.Errors[0].Code != "dangling_choice" || report.Errors[0].URL != e.url+"/choices/"+c1.ID {
		t.Fatalf("report after delete %+v", report)
	}
	e.do("PATCH", "/api/choices/"+c1.ID, e.key, map[string]any{"toNodeId": ""}, http.StatusOK, nil)
	e.do("GET", "/api/build", e.key, nil, http.StatusOK, &report)
	if !report.OK {
		t.Fatalf("report after repointing %+v", report)
	}
}

// The database itself refuses text written by an agent key, and the build
// catches text changed behind the revisions' back.
func TestProvenanceBackstops(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, err := e.q.CreateTextRevision(ctx, dbgen.CreateTextRevisionParams{
		ID: xid.New().String(), TargetKind: "node", TargetID: xid.New().String(), Field: "text", Body: "A line Claude wrote.", AuthorKey: e.agentID,
	})
	if err == nil || !strings.Contains(err.Error(), "only a human key") {
		t.Fatalf("an agent's revision was stored: %v", err)
	}

	var g conversations.Graph
	e.do("POST", "/api/conversations", e.key, map[string]any{"title": "Homecoming"}, http.StatusCreated, &g)
	var n conversations.Node
	e.do("POST", "/api/conversations/"+g.Conversation.ID+"/nodes", e.key, map[string]any{"text": "You came back."}, http.StatusCreated, &n)
	if _, err := e.pool.Exec(ctx, `UPDATE nodes SET text = 'Edited in psql.' WHERE id = $1`, n.ID); err != nil {
		t.Fatal(err)
	}
	var report build.Report
	e.do("GET", "/api/build", e.key, nil, http.StatusOK, &report)
	if report.OK || report.Errors[0].Code != "provenance" || report.Errors[0].TargetID != n.ID {
		t.Fatalf("report %+v", report)
	}
}

// pkce is a verifier and its S256 challenge.
func pkce() (verifier, challenge string) {
	verifier = "a-verifier-that-is-long-enough-for-pkce-0123456789"
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestOAuthFlow(t *testing.T) {
	e := newEnv(t)
	const redirect = "https://claude.example/callback"

	var reg struct {
		ClientID string `json:"client_id"`
	}
	e.do("POST", "/oauth/register", "", map[string]any{"redirect_uris": []string{redirect}, "client_name": "test"}, http.StatusCreated, &reg)

	verifier, challenge := pkce()
	form := url.Values{
		"response_type": {"code"}, "client_id": {reg.ClientID}, "redirect_uri": {redirect},
		"state": {"xyz"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	resp, err := e.client.Get(e.url + "/oauth/authorize?" + form.Encode())
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("authorize page: %v %v", resp.StatusCode, err)
	}
	form.Set("api_key", auth.KeyPrefix+"wrong")
	if resp, _ = e.client.PostForm(e.url+"/oauth/authorize", form); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a wrong key should not authorize: %d", resp.StatusCode)
	}
	form.Set("api_key", e.key)
	resp, err = e.client.PostForm(e.url+"/oauth/authorize", form)
	if err != nil || resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("authorize: %v %v", resp.StatusCode, err)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("state") != "xyz" || loc.Query().Get("code") == "" {
		t.Fatalf("redirect %s", loc)
	}

	token := func(v url.Values, want int) map[string]any {
		t.Helper()
		resp, err := e.client.PostForm(e.url+"/oauth/token", v)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if resp.StatusCode != want {
			t.Fatalf("token: %d %v, want %d", resp.StatusCode, out, want)
		}
		return out
	}
	codeGrant := url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")},
		"code_verifier": {verifier}, "client_id": {reg.ClientID}, "redirect_uri": {redirect}}
	tok := token(codeGrant, http.StatusOK)
	token(codeGrant, http.StatusBadRequest) // a code works once

	access, refresh := tok["access_token"].(string), tok["refresh_token"].(string)

	// The connector acts as the person's key but is not the person: it may
	// read and leave notes, and may not write the game's text.
	var me struct{ Human bool }
	e.do("GET", "/api/me", access, nil, http.StatusOK, &me)
	if me.Human {
		t.Fatal("a connector token reads as human")
	}
	e.do("POST", "/api/characters", access, map[string]any{"name": "Mallory"}, http.StatusForbidden, nil)
	var note notes.Note
	e.do("POST", "/api/notes", access, map[string]any{"body": "Act two drags."}, http.StatusCreated, &note)
	if note.ByHuman || note.AuthorName != "test" {
		t.Fatalf("a connector's note %+v", note)
	}

	refreshGrant := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}
	next := token(refreshGrant, http.StatusOK)
	token(refreshGrant, http.StatusBadRequest) // a refresh token works once
	e.do("GET", "/api/me", access, nil, http.StatusUnauthorized, nil)
	e.do("GET", "/api/me", next["access_token"].(string), nil, http.StatusOK, nil)

	// Deleting the key cuts off every token it approved.
	if err := e.q.DeleteAPIKey(context.Background(), e.keyID); err != nil {
		t.Fatal(err)
	}
	e.do("GET", "/api/me", next["access_token"].(string), nil, http.StatusUnauthorized, nil)
}

func TestInviteFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	const redirect = "https://claude.example/callback"
	var reg struct {
		ClientID string `json:"client_id"`
	}
	e.do("POST", "/oauth/register", "", map[string]any{"redirect_uris": []string{redirect}}, http.StatusCreated, &reg)

	invite := func(note string, expires time.Duration) string {
		t.Helper()
		raw, _ := auth.RandToken(auth.InvitePrefix)
		arg := dbgen.CreateInviteParams{ID: xid.New().String(), CodeHash: auth.Hash(raw), Note: note}
		if expires != 0 {
			arg.ExpiresAt = pgtype.Timestamptz{Time: time.Now().Add(expires), Valid: true}
		}
		if _, err := e.q.CreateInvite(ctx, arg); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	verifier, challenge := pkce()
	// authorize pastes credential on the authorize page and returns the
	// status and, on success, the code.
	authorize := func(credential string) (int, string) {
		t.Helper()
		form := url.Values{
			"response_type": {"code"}, "client_id": {reg.ClientID}, "redirect_uri": {redirect},
			"code_challenge": {challenge}, "code_challenge_method": {"S256"},
			"api_key": {credential},
		}
		resp, err := e.client.PostForm(e.url+"/oauth/authorize", form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		loc, _ := url.Parse(resp.Header.Get("Location"))
		return resp.StatusCode, loc.Query().Get("code")
	}

	raw := invite("ana", 0)
	status, code := authorize(raw)
	if status != http.StatusSeeOther || code == "" {
		t.Fatalf("redeem invite: %d", status)
	}
	if status, _ = authorize(raw); status != http.StatusBadRequest {
		t.Fatalf("an invite works once: %d", status)
	}
	if status, _ = authorize(auth.InvitePrefix + "not-an-invite"); status != http.StatusBadRequest {
		t.Fatalf("unknown invite: %d", status)
	}
	if status, _ = authorize(invite("late", -time.Minute)); status != http.StatusBadRequest {
		t.Fatalf("expired invite: %d", status)
	}

	resp, err := e.client.PostForm(e.url+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"code_verifier": {verifier}, "client_id": {reg.ClientID}, "redirect_uri": {redirect}})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("token: %v %v", resp.StatusCode, err)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&tok)
	resp.Body.Close()

	// The invite minted a key named after its note, and the token acts as it.
	var me struct{ KeyID, Name string }
	e.do("GET", "/api/me", tok.AccessToken, nil, http.StatusOK, &me)
	if me.Name != "ana" {
		t.Fatalf("redeeming the invite minted key %+v, want one named ana", me)
	}
	// The newcomer's key is a key like any other: deleting it cuts them off.
	if err := e.q.DeleteAPIKey(ctx, me.KeyID); err != nil {
		t.Fatal(err)
	}
	e.do("GET", "/api/me", tok.AccessToken, nil, http.StatusUnauthorized, nil)
}

func TestWebPages(t *testing.T) {
	e := newEnv(t)
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: e.client.CheckRedirect}

	page := func(path string) string {
		t.Helper()
		resp, err := browser.Get(e.url + path)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %v %v", path, resp.StatusCode, err)
		}
		if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'none'") {
			t.Fatalf("GET %s: no CSP", path)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(b)
	}
	post := func(path string, form url.Values) (int, string) {
		t.Helper()
		resp, err := browser.PostForm(e.url+path, form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode, resp.Header.Get("Location")
	}
	login := func(key string) {
		t.Helper()
		if status, loc := post("/login", url.Values{"api_key": {key}, "next": {"//evil.example"}}); status != http.StatusSeeOther || loc != "/" {
			t.Fatalf("login: %d %s (next must stay on this site)", status, loc)
		}
	}

	// Signed out, even reading sends you to sign in: the content is unreleased.
	resp, _ := browser.Get(e.url + "/")
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Fatalf("signed-out index: %d", resp.StatusCode)
	}
	if status, _ := post("/characters", url.Values{"name": {"x"}}); status != http.StatusUnauthorized {
		t.Fatalf("signed-out create: %d", status)
	}
	if status, _ := post("/login", url.Values{"api_key": {"wrong"}, "next": {"/"}}); status != http.StatusUnauthorized {
		t.Fatalf("bad login: %d", status)
	}

	login(e.key)
	status, loc := post("/characters", url.Values{"name": {"Aria"}, "bible": {"Wants her brother back."}})
	if status != http.StatusSeeOther || !strings.HasPrefix(loc, "/characters/") {
		t.Fatalf("create character: %d %s", status, loc)
	}
	ariaID := strings.TrimPrefix(strings.Split(loc, "?")[0], "/characters/")
	status, loc = post("/conversations", url.Values{"title": {"Homecoming"}})
	if status != http.StatusSeeOther {
		t.Fatalf("create conversation: %d", status)
	}
	convPath, _, _ := strings.Cut(loc, "?")
	if _, loc = post(convPath+"/nodes", url.Values{"speaker_id": {ariaID}, "text": {"You came back."}}); !strings.Contains(loc, "#node-") {
		t.Fatalf("create node: %s", loc)
	}
	nodeID := loc[strings.Index(loc, "#node-")+len("#node-"):]
	if _, loc = post("/nodes/"+nodeID+"/choices", url.Values{"text": {"Leave."}, "to_node_id": {""}, "sort_order": {"0"}}); strings.Contains(loc, "err=") {
		t.Fatalf("create choice: %s", loc)
	}
	if _, loc = post(convPath+"/nodes", url.Values{"text": {"  "}}); !strings.Contains(loc, "err=") {
		t.Fatalf("an empty line should come back as a message: %s", loc)
	}
	if _, loc = post("/notes", url.Values{"target_kind": {"node"}, "target_id": {nodeID}, "kind": {"question"}, "body": {"Is she angry or relieved?"}, "back": {convPath}}); strings.Contains(loc, "err=") {
		t.Fatalf("create note: %s", loc)
	}

	body := page(convPath)
	for _, want := range []string{"Homecoming", "Aria", "You came back.", "Leave.", "ends the conversation", "Is she angry or relieved?", "Add a line", "Sign out"} {
		if !strings.Contains(body, want) {
			t.Errorf("conversation page lacks %q", want)
		}
	}
	if index := page("/"); !strings.Contains(index, "Homecoming") || !strings.Contains(index, "builds") {
		t.Error("index lacks the conversation or the build status")
	}
	if b := page("/build"); !strings.Contains(b, "The content builds") {
		t.Error("build page does not say it builds")
	}
	if n := page("/notes"); !strings.Contains(n, "Is she angry or relieved?") {
		t.Error("notes page lacks the note")
	}
	resp, _ = browser.Get(e.url + "/nodes/" + nodeID)
	if resp.StatusCode != http.StatusSeeOther || !strings.HasSuffix(resp.Header.Get("Location"), convPath+"#node-"+nodeID) {
		t.Fatalf("node redirect: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	// A cross-site form post is refused even with the cookie.
	req, _ := http.NewRequest("POST", e.url+"/nodes/"+nodeID+"/delete", nil)
	req.Header.Set("Origin", "https://evil.example")
	if resp, _ = browser.Do(req); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin delete: %d", resp.StatusCode)
	}

	post("/logout", nil)

	// Signed in with an agent key, the pages read the same but offer no
	// editing, and an edit posted anyway is refused.
	login(e.agent)
	body = page(convPath)
	if !strings.Contains(body, "agent key") || !strings.Contains(body, "You came back.") {
		t.Error("agent's conversation page lacks the banner or the content")
	}
	for _, hidden := range []string{"Add a line", "Edit line", "Delete"} {
		if strings.Contains(body, hidden) {
			t.Errorf("agent's conversation page shows %q", hidden)
		}
	}
	if _, loc = post("/nodes/"+nodeID, url.Values{"text": {"A line Claude wrote."}}); !strings.Contains(loc, "err=") {
		t.Fatalf("agent edit: %s", loc)
	}
	if _, loc = post("/notes", url.Values{"target_kind": {"node"}, "target_id": {nodeID}, "kind": {"gap"}, "body": {"No mention of the letter."}, "back": {convPath}}); strings.Contains(loc, "err=") {
		t.Fatalf("agent note: %s", loc)
	}
	var g conversations.Graph
	e.do("GET", strings.Replace(convPath, "/conversations/", "/api/conversations/", 1), e.key, nil, http.StatusOK, &g)
	if g.Nodes[0].Text != "You came back." {
		t.Fatalf("an agent's page edit got through: %q", g.Nodes[0].Text)
	}
}

// TestMechanics builds a small game over the API (a flag, a skill, a quest,
// a conversation whose choices carry conditions, effects and a skill
// check), checks what the build says, then plays it through the
// playtest pages.
func TestMechanics(t *testing.T) {
	e := newEnv(t)

	var flag world.Flag
	e.do("POST", "/api/flags", e.key, map[string]any{"key": "met_mayor", "description": "The player has met the mayor."}, http.StatusCreated, &flag)
	e.do("POST", "/api/flags", e.key, map[string]any{"key": "met_mayor"}, http.StatusUnprocessableEntity, nil)
	e.do("POST", "/api/flags", e.key, map[string]any{"key": "Bad Key"}, http.StatusUnprocessableEntity, nil)
	var skill world.Skill
	e.do("POST", "/api/skills", e.key, map[string]any{"name": "Persuasion", "initialLevel": 2}, http.StatusCreated, &skill)
	if skill.Key != "persuasion" {
		t.Fatalf("skill key %q", skill.Key)
	}
	var quest world.Quest
	e.do("POST", "/api/quests", e.key, map[string]any{"name": "Find Her Brother", "key": "find_brother"}, http.StatusCreated, &quest)
	e.do("POST", "/api/quests/"+quest.ID+"/stages", e.key, map[string]any{"number": 1, "journal": "Aria has a brother in the Citadel."}, http.StatusCreated, &quest)
	e.do("POST", "/api/quests/"+quest.ID+"/stages", e.key, map[string]any{"number": 1, "journal": "Twice."}, http.StatusUnprocessableEntity, nil)
	if len(quest.Stages) != 1 {
		t.Fatalf("quest %+v", quest)
	}
	var aria characters.Character
	e.do("POST", "/api/characters", e.key, map[string]any{"name": "Aria Venn", "companion": true}, http.StatusCreated, &aria)
	if aria.Key != "aria_venn" || !aria.Companion {
		t.Fatalf("character %+v", aria)
	}

	// An agent may not touch the world.
	e.do("POST", "/api/flags", e.agent, map[string]any{"key": "x"}, http.StatusForbidden, nil)
	e.do("POST", "/api/quests/"+quest.ID+"/stages", e.agent, map[string]any{"number": 2, "journal": "A line Claude wrote."}, http.StatusForbidden, nil)

	var g conversations.Graph
	e.do("POST", "/api/conversations", e.key, map[string]any{"title": "Docks", "key": "docks"}, http.StatusCreated, &g)
	if !g.Conversation.Opening {
		t.Fatal("the first conversation does not open the game")
	}
	conv := g.Conversation.ID
	var n1, n2, n3 conversations.Node
	e.do("POST", "/api/conversations/"+conv+"/nodes", e.key, map[string]any{"speakerId": aria.ID, "text": "You came back."}, http.StatusCreated, &n1)
	e.do("POST", "/api/conversations/"+conv+"/nodes", e.key, map[string]any{"speakerId": aria.ID, "text": "He is in the Citadel.", "effects": "quest find_brother = 1"}, http.StatusCreated, &n2)
	e.do("POST", "/api/conversations/"+conv+"/nodes", e.key, map[string]any{"speakerId": aria.ID, "text": "Forget it."}, http.StatusCreated, &n3)
	if n2.Effects != "quest find_brother = 1" {
		t.Fatalf("node effects %q", n2.Effects)
	}

	// Scripts are checked as they are saved.
	e.do("POST", "/api/nodes/"+n1.ID+"/choices", e.key, map[string]any{"text": "x", "condition": "met_mayr"}, http.StatusUnprocessableEntity, nil)
	e.do("POST", "/api/nodes/"+n1.ID+"/choices", e.key, map[string]any{"text": "x", "effects": "quest find_brother += 1"}, http.StatusUnprocessableEntity, nil)
	e.do("POST", "/api/nodes/"+n1.ID+"/choices", e.key, map[string]any{"text": "x", "checkSkillId": skill.ID}, http.StatusUnprocessableEntity, nil)

	var ask conversations.Choice
	e.do("POST", "/api/nodes/"+n1.ID+"/choices", e.key, map[string]any{
		"text": "Tell me about your brother.", "toNodeId": n2.ID, "effects": "approval aria_venn += 5",
		"checkSkillId": skill.ID, "checkDifficulty": 2, "failNodeId": n3.ID,
	}, http.StatusCreated, &ask)
	if ask.CheckSkillName != "Persuasion" || ask.CheckDifficulty != 2 || ask.FailNodeID != n3.ID || ask.Effects != "approval aria_venn += 5" {
		t.Fatalf("check choice %+v", ask)
	}
	var stranger conversations.Choice
	e.do("POST", "/api/nodes/"+n1.ID+"/choices", e.key, map[string]any{"text": "I'm new here.", "toNodeId": n3.ID, "condition": "not met_mayor", "sortOrder": 1}, http.StatusCreated, &stranger)

	// Renaming a flag shows in every script that names it.
	e.do("PATCH", "/api/flags/"+flag.ID, e.key, map[string]any{"key": "knows_mayor"}, http.StatusOK, nil)
	e.do("GET", "/api/conversations/"+conv, e.key, nil, http.StatusOK, &g)
	if got := g.Nodes[0].Choices[1].Condition; got != "not knows_mayor" {
		t.Fatalf("condition after renaming the flag: %q", got)
	}
	// A PATCH can drop a check.
	var plain conversations.Choice
	e.do("PATCH", "/api/choices/"+stranger.ID, e.key, map[string]any{"checkSkillId": ""}, http.StatusOK, &plain)
	if plain.CheckSkillID != "" || plain.Condition != "not knows_mayor" {
		t.Fatalf("choice after dropping its check %+v", plain)
	}

	// The build: the flag is read but nothing sets it.
	var report build.Report
	e.do("GET", "/api/build", e.agent, nil, http.StatusOK, &report)
	if !report.OK || len(report.Warnings) != 1 || report.Warnings[0].Code != "flag_never_set" || report.Stats.Quests != 1 {
		t.Fatalf("report %+v", report)
	}
	// Deleting the skill breaks the check, and the build says so.
	e.do("DELETE", "/api/skills/"+skill.ID, e.key, nil, http.StatusNoContent, nil)
	e.do("GET", "/api/build", e.key, nil, http.StatusOK, &report)
	if report.OK || report.Errors[0].Code != "bad_check" {
		t.Fatalf("report after deleting the skill %+v", report)
	}
	var restored world.Skill
	e.do("POST", "/api/skills", e.key, map[string]any{"name": "Persuasion", "initialLevel": 2}, http.StatusCreated, &restored)
	e.do("PATCH", "/api/choices/"+ask.ID, e.key, map[string]any{"checkSkillId": restored.ID}, http.StatusOK, &ask)
	if ask.CheckDifficulty != 2 || ask.FailNodeID != n3.ID {
		t.Fatalf("a check keeps the parts not sent: %+v", ask)
	}

	// Play it. Difficulty 2 with Persuasion 2 cannot fail.
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: e.client.CheckRedirect}
	if resp, _ := browser.PostForm(e.url+"/login", url.Values{"api_key": {e.key}, "next": {"/"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	get := func(path string) string {
		t.Helper()
		resp, err := browser.Get(e.url + path)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %v %v", path, resp.StatusCode, err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return html.UnescapeString(string(b)) // as a reader sees it
	}
	post := func(path string, form url.Values) string {
		t.Helper()
		resp, err := browser.PostForm(e.url+path, form)
		if err != nil || resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("POST %s: %v %v", path, resp.StatusCode, err)
		}
		resp.Body.Close()
		loc, _, _ := strings.Cut(resp.Header.Get("Location"), "#")
		return loc
	}
	if !strings.Contains(get("/play"), "Begin") {
		t.Fatal("the playtest page offers no new game")
	}
	savePath, _, _ := strings.Cut(post("/play/new", url.Values{"name": {"first run"}}), "?")
	page := get(savePath)
	for _, want := range []string{"ARIA VENN", "You came back.", "Tell me about your brother.", "[Persuasion 2 · 2 · 100%]", "I'm new here."} {
		if !strings.Contains(page, want) {
			t.Errorf("play page lacks %q", want)
		}
	}
	post(savePath+"/choose", url.Values{"choice": {ask.ID}})
	page = get(savePath)
	for _, want := range []string{"[Persuasion: Success]", "He is in the Citadel.", "Aria Venn approves (+5)", "Journal updated: Find Her Brother.", "The End"} {
		if !strings.Contains(page, want) {
			t.Errorf("play page after the check lacks %q", want)
		}
	}

	// A playtest can set the world up and start anywhere.
	savePath, _, _ = strings.Cut(post("/play/new", url.Values{"name": {"setup"}, "node": {n1.ID}, "setup": {"knows_mayor"}}), "?")
	if page = get(savePath); strings.Contains(page, "I'm new here.") {
		t.Error("the setup did not hide the choice")
	}
	if loc := post("/play/new", url.Values{"setup": {"nonsense ="}}); !strings.Contains(loc, "msg=") {
		t.Errorf("a bad setup should come back as a message: %s", loc)
	}

	// Saves are per key: the agent cannot open the person's.
	e.do("GET", "/api/me", e.agent, nil, http.StatusOK, nil)
	agentJar, _ := cookiejar.New(nil)
	agentBrowser := &http.Client{Jar: agentJar, CheckRedirect: e.client.CheckRedirect}
	agentBrowser.PostForm(e.url+"/login", url.Values{"api_key": {e.agent}, "next": {"/"}}) //nolint:errcheck
	if resp, _ := agentBrowser.Get(e.url + savePath); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("another key's save: %d", resp.StatusCode)
	}
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// bearer adds an Authorization header to every request.
type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

func TestMCPTools(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// Content for Claude to read, written by a person over the API.
	var aria characters.Character
	e.do("POST", "/api/characters", e.key, map[string]any{"name": "Aria", "bible": "Wants her brother back."}, http.StatusCreated, &aria)
	var g conversations.Graph
	e.do("POST", "/api/conversations", e.key, map[string]any{"title": "Homecoming"}, http.StatusCreated, &g)
	var n1, n2 conversations.Node
	e.do("POST", "/api/conversations/"+g.Conversation.ID+"/nodes", e.key, map[string]any{"speakerId": aria.ID, "text": "You came back."}, http.StatusCreated, &n1)
	e.do("POST", "/api/conversations/"+g.Conversation.ID+"/nodes", e.key, map[string]any{"text": "She looks away."}, http.StatusCreated, &n2)
	e.do("POST", "/api/nodes/"+n1.ID+"/choices", e.key, map[string]any{"text": "Ask about her brother.", "toNodeId": n2.ID}, http.StatusCreated, nil)

	// Claude connects with an agent key, as Claude Code would.
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:   e.url + "/mcp/rpc",
		HTTPClient: &http.Client{Transport: bearer{e.agent, http.DefaultTransport}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	// The tool list is exactly appmcp.Tools: nothing that writes content.
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := slices.Clone(appmcp.Tools)
	sort.Strings(want)
	if !slices.Equal(names, want) {
		t.Fatalf("tools %v, want %v", names, want)
	}

	call := func(name string, args map[string]any, out any) {
		t.Helper()
		res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.IsError {
			t.Fatalf("%s: tool error %+v", name, res.Content)
		}
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	var who struct {
		Me struct {
			KeyID string
			Human bool
		}
	}
	call("whoami", nil, &who)
	if who.Me.KeyID != e.agentID || who.Me.Human {
		t.Fatalf("whoami %+v", who)
	}
	var conv struct{ Conversation conversations.Graph }
	call("get_conversation", map[string]any{"id": g.Conversation.ID}, &conv)
	if len(conv.Conversation.Nodes) != 2 || conv.Conversation.Nodes[0].SpeakerName != "Aria" {
		t.Fatalf("get_conversation %+v", conv)
	}
	var char struct{ Character characters.Character }
	call("get_character", map[string]any{"id": aria.ID}, &char)
	if char.Character.Bible != "Wants her brother back." {
		t.Fatalf("get_character %+v", char)
	}
	var path struct{ Path conversations.Path }
	call("get_path_to", map[string]any{"id": n2.ID}, &path)
	if !path.Path.Reachable || len(path.Path.Steps) != 2 {
		t.Fatalf("get_path_to %+v", path)
	}
	var report struct{ Report build.Report }
	call("check_build", nil, &report)
	if !report.Report.OK {
		t.Fatalf("check_build %+v", report)
	}

	var added struct{ Note notes.Note }
	call("add_note", map[string]any{"target_kind": "node", "target_id": n2.ID, "kind": "continuity", "body": "In node X she said she never had a brother."}, &added)
	if added.Note.ByHuman || added.Note.Kind != "continuity" {
		t.Fatalf("add_note %+v", added)
	}
	var reply struct{ Note notes.Note }
	call("reply_to_note", map[string]any{"note_id": added.Note.ID, "body": "Checked again: node X is in a cut conversation."}, &reply)
	if reply.Note.ParentID != added.Note.ID || reply.Note.TargetID != n2.ID {
		t.Fatalf("reply_to_note %+v", reply)
	}
	var list struct{ Notes []notes.Note }
	call("list_notes", map[string]any{"target_kind": "node", "target_id": n2.ID}, &list)
	if len(list.Notes) != 2 {
		t.Fatalf("list_notes %+v", list)
	}

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "get_note", Arguments: map[string]any{"id": "nope"}})
	if err != nil || !res.IsError {
		t.Fatalf("a missing note should come back as a tool error: %v %+v", err, res)
	}

	// Without a credential, MCP is a 401 pointing at OAuth discovery.
	resp, err := http.Post(e.url+"/mcp/rpc", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("anonymous MCP: %d", resp.StatusCode)
	}
}
