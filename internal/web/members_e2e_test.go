package web_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/KevinMcHugh/rpg/internal/auth"
	"github.com/KevinMcHugh/rpg/internal/server/characters"
	"github.com/KevinMcHugh/rpg/internal/server/games"
	"github.com/KevinMcHugh/rpg/internal/server/members"
)

// session is the session token a response set, or "".
func session(resp *http.Response) string {
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookie && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

// get sends a GET with an optional session and returns the response, its
// body read.
func (e *env) get(path, token string) (*http.Response, string) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.url+path, nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// post sends a form POST with an optional session.
func (e *env) post(path, token string, form url.Values) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest("POST", e.url+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if token != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

// login signs in with an api key and returns the session.
func (e *env) login(key string) string {
	e.t.Helper()
	resp := e.post("/login", "", url.Values{"api_key": {key}, "next": {"/"}})
	if s := session(resp); s != "" {
		return s
	}
	e.t.Fatalf("login: %d", resp.StatusCode)
	return ""
}

// google runs a Google sign-in started at start (with session, if any) as
// the fake Google account behind code, and returns the callback's response.
func (e *env) google(start, token, code string) *http.Response {
	e.t.Helper()
	resp, _ := e.get(start, token)
	if resp.StatusCode != http.StatusFound {
		e.t.Fatalf("%s: %d", start, resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("redirect_uri") != e.url+"/auth/google/callback" {
		e.t.Fatalf("google redirect %s", loc)
	}
	cb, _ := e.get("/auth/google/callback?"+url.Values{"state": {loc.Query().Get("state")}, "code": {code}}.Encode(), "")
	return cb
}

// TestMembersAndInvites covers who sees a game: its owner, writers who
// joined by an invite link (signed in with a key or with Google), their
// agents, and nobody else.
func TestMembersAndInvites(t *testing.T) {
	e := newEnv(t)

	var g games.Game
	e.do("POST", "/api/games", e.key, map[string]any{"name": "Heist"}, http.StatusCreated, &g)
	if g.Role != "owner" {
		t.Fatalf("the creator should own the game: %+v", g)
	}
	var aria characters.Character
	e.do("POST", "/api/games/"+g.ID+"/characters", e.key, map[string]any{"name": "Aria"}, http.StatusCreated, &aria)

	// A stranger cannot tell the game exists.
	bea, beaID := e.person("bea")
	beaAgent, _ := e.mint("bea's claude", auth.KindAgent, beaID)
	var list struct{ Games []games.Game }
	e.do("GET", "/api/games", bea, nil, http.StatusOK, &list)
	if len(list.Games) != 0 {
		t.Fatalf("a stranger sees %+v", list.Games)
	}
	for _, path := range []string{"/api/games/" + g.ID, "/api/games/" + g.ID + "/characters", "/api/characters/" + aria.ID, "/api/games/" + g.ID + "/members", "/api/games/" + g.ID + "/build"} {
		e.do("GET", path, bea, nil, http.StatusNotFound, nil)
		e.do("GET", path, beaAgent, nil, http.StatusNotFound, nil)
	}
	e.do("PATCH", "/api/characters/"+aria.ID, bea, map[string]any{"name": "Mallory"}, http.StatusNotFound, nil)
	e.do("POST", "/api/games/"+g.ID+"/notes", beaAgent, map[string]any{"targetKind": "game", "kind": "gap", "body": "x"}, http.StatusNotFound, nil)
	e.do("POST", "/api/games/"+g.ID+"/invites", bea, map[string]any{}, http.StatusNotFound, nil)
	if resp, _ := e.get("/nodes/whatever", e.login(bea)); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a redirect to a stranger's node: %d", resp.StatusCode)
	}

	// Only the owner, and only a person, makes invites.
	e.do("POST", "/api/games/"+g.ID+"/invites", e.agent, map[string]any{}, http.StatusForbidden, nil)
	e.do("POST", "/api/games/"+g.ID+"/invites", e.key, map[string]any{"expiresInHours": 10000}, http.StatusUnprocessableEntity, nil)
	var inv members.Invite
	e.do("POST", "/api/games/"+g.ID+"/invites", e.key, map[string]any{"note": "for bea"}, http.StatusCreated, &inv)
	code := strings.TrimPrefix(inv.URL, e.url+"/join/")
	if !strings.HasPrefix(code, auth.GameInvitePrefix) {
		t.Fatalf("invite url %q", inv.URL)
	}
	var invites struct{ Invites []members.Invite }
	e.do("GET", "/api/games/"+g.ID+"/invites", e.key, nil, http.StatusOK, &invites)
	if len(invites.Invites) != 1 || invites.Invites[0].URL != "" || invites.Invites[0].Note != "for bea" {
		t.Fatalf("invites %+v (the link is shown only once)", invites.Invites)
	}

	// The link's page shows anyone what it is for, and how to accept.
	resp, body := e.get("/join/"+code, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Heist") || !strings.Contains(body, "/auth/google?invite="+code) {
		t.Fatalf("join page %d: %s", resp.StatusCode, body)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.HasPrefix(csp, "default-src 'none'") {
		t.Fatalf("join page CSP %q", csp)
	}
	beaSession := e.login(bea)
	if _, body := e.get("/join/"+code, beaSession); !strings.Contains(body, "Join as bea") {
		t.Fatalf("join page signed in: %s", body)
	}
	if resp := e.post("/join/"+code, "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("joining signed out: %d", resp.StatusCode)
	}
	if resp := e.post("/join/"+code, beaSession, nil); resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/games/"+g.ID {
		t.Fatalf("join: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ := e.get("/join/"+code, ""); resp.StatusCode != http.StatusGone {
		t.Fatalf("a used link: %d", resp.StatusCode)
	}

	// Bea writes now, and so does her agent see the game; but the game is
	// still the owner's to change.
	e.do("GET", "/api/games", bea, nil, http.StatusOK, &list)
	if len(list.Games) != 1 || list.Games[0].ID != g.ID || list.Games[0].Role != "writer" {
		t.Fatalf("bea's games %+v", list.Games)
	}
	e.do("PATCH", "/api/characters/"+aria.ID, bea, map[string]any{"bible": "Wants out."}, http.StatusOK, nil)
	e.do("GET", "/api/characters/"+aria.ID, beaAgent, nil, http.StatusOK, nil)
	e.do("PATCH", "/api/games/"+g.ID, bea, map[string]any{"name": "Mine"}, http.StatusForbidden, nil)
	e.do("DELETE", "/api/games/"+g.ID, bea, nil, http.StatusForbidden, nil)
	e.do("GET", "/api/games/"+g.ID+"/invites", bea, nil, http.StatusForbidden, nil)
	e.do("POST", "/api/games/"+g.ID+"/invites", bea, map[string]any{}, http.StatusForbidden, nil)

	// A member opening a link goes to the game, and leaves it for whoever
	// it was meant for.
	e.do("POST", "/api/games/"+g.ID+"/invites", e.key, map[string]any{}, http.StatusCreated, &inv)
	code = strings.TrimPrefix(inv.URL, e.url+"/join/")
	if resp := e.post("/join/"+code, beaSession, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("a member joining: %d", resp.StatusCode)
	}

	// A newcomer signs up with Google through that link, and lands in the
	// game.
	cb := e.google("/auth/google?invite="+code, "", "code-cal")
	cal := session(cb)
	if cb.StatusCode != http.StatusSeeOther || cb.Header.Get("Location") != "/games/"+g.ID || cal == "" {
		t.Fatalf("google sign-up: %d %s", cb.StatusCode, cb.Header.Get("Location"))
	}
	var me struct {
		KeyID string
		Human bool
		User  struct{ ID, Name string }
	}
	e.do("GET", "/api/me", cal, nil, http.StatusOK, &me)
	if me.User.Name != "Cal" || !me.Human {
		t.Fatalf("google user %+v", me)
	}
	calID := me.User.ID
	e.do("POST", "/api/games/"+g.ID+"/characters", cal, map[string]any{"name": "Cal's"}, http.StatusCreated, nil)
	// Signing in again needs no invite, and is the same person.
	cb = e.google("/auth/google?next=/games", "", "code-cal")
	if cb.StatusCode != http.StatusSeeOther || cb.Header.Get("Location") != "/games" {
		t.Fatalf("google sign-in: %d %s", cb.StatusCode, cb.Header.Get("Location"))
	}
	e.do("GET", "/api/me", session(cb), nil, http.StatusOK, &me)
	if me.User.ID != calID {
		t.Fatalf("signing in again made a new user: %+v", me)
	}
	// A stranger without an invite gets no account.
	if cb = e.google("/auth/google", "", "code-dee"); cb.StatusCode != http.StatusForbidden || session(cb) != "" {
		t.Fatalf("google stranger: %d", cb.StatusCode)
	}
	// Someone with a key adds Google as a way in.
	if cb = e.google("/auth/google?link=1", beaSession, "code-bea"); cb.StatusCode != http.StatusSeeOther {
		t.Fatalf("link google: %d", cb.StatusCode)
	}
	e.do("GET", "/api/me", session(e.google("/auth/google", "", "code-bea")), nil, http.StatusOK, &me)
	if me.User.ID != beaID {
		t.Fatalf("bea's google sign-in is %+v", me)
	}
	if cb = e.google("/auth/google?link=1", beaSession, "code-cal"); cb.StatusCode != http.StatusForbidden {
		t.Fatalf("linking someone else's google: %d", cb.StatusCode)
	}

	// Members, and taking them out.
	var ms struct{ Members []members.Member }
	e.do("GET", "/api/games/"+g.ID+"/members", cal, nil, http.StatusOK, &ms)
	if len(ms.Members) != 3 || ms.Members[0].Role != "owner" || ms.Members[0].UserID != e.userID {
		t.Fatalf("members %+v", ms.Members)
	}
	e.do("DELETE", "/api/games/"+g.ID+"/members/"+calID, bea, nil, http.StatusForbidden, nil)
	e.do("DELETE", "/api/games/"+g.ID+"/members/"+e.userID, e.key, nil, http.StatusUnprocessableEntity, nil)
	e.do("DELETE", "/api/games/"+g.ID+"/members/"+calID, e.key, nil, http.StatusNoContent, nil)
	e.do("GET", "/api/games/"+g.ID, cal, nil, http.StatusNotFound, nil)
	e.do("DELETE", "/api/games/"+g.ID+"/members/"+beaID, bea, nil, http.StatusNoContent, nil)
	e.do("GET", "/api/characters/"+aria.ID, beaAgent, nil, http.StatusNotFound, nil)

	// A revoked link stops working.
	e.do("POST", "/api/games/"+g.ID+"/invites", e.key, map[string]any{}, http.StatusCreated, &inv)
	e.do("DELETE", "/api/invites/"+inv.ID, bea, nil, http.StatusNotFound, nil)
	e.do("DELETE", "/api/invites/"+inv.ID, e.key, nil, http.StatusNoContent, nil)
	if resp, _ := e.get(strings.TrimPrefix(inv.URL, e.url), ""); resp.StatusCode != http.StatusGone {
		t.Fatalf("a revoked link: %d", resp.StatusCode)
	}
}

// TestConnectWithGoogle connects a claude.ai-style client by signing in
// with Google on the authorize page. The token is the person's agent: it
// sees their games and cannot write content.
func TestConnectWithGoogle(t *testing.T) {
	e := newEnv(t)
	var g games.Game
	e.do("POST", "/api/games", e.key, map[string]any{"name": "Heist"}, http.StatusCreated, &g)
	var inv members.Invite
	e.do("POST", "/api/games/"+g.ID+"/invites", e.key, map[string]any{}, http.StatusCreated, &inv)
	code := strings.TrimPrefix(inv.URL, e.url+"/join/")
	if cb := e.google("/auth/google?invite="+code, "", "code-dee"); cb.StatusCode != http.StatusSeeOther {
		t.Fatalf("sign up: %d", cb.StatusCode)
	}

	const redirect = "https://claude.example/callback"
	var reg struct {
		ClientID string `json:"client_id"`
	}
	e.do("POST", "/oauth/register", "", map[string]any{"redirect_uris": []string{redirect}}, http.StatusCreated, &reg)
	verifier, challenge := pkce()
	authz := url.Values{"response_type": {"code"}, "client_id": {reg.ClientID}, "redirect_uri": {redirect},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"xyz"}}
	_, page := e.get("/oauth/authorize?"+authz.Encode(), "")
	i := strings.Index(page, `href="/auth/google?`)
	if i < 0 {
		t.Fatalf("authorize page has no google link: %s", page)
	}
	start := page[i+len(`href="`):]
	start = strings.ReplaceAll(start[:strings.Index(start, `"`)], "&amp;", "&")
	cb := e.google(start, "", "code-dee")
	loc, _ := url.Parse(cb.Header.Get("Location"))
	if cb.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc.String(), redirect) || loc.Query().Get("state") != "xyz" {
		t.Fatalf("google authorize: %d %s", cb.StatusCode, loc)
	}
	resp, err := e.client.PostForm(e.url+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")},
		"code_verifier": {verifier}, "client_id": {reg.ClientID}, "redirect_uri": {redirect}})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("token: %v %v", resp.StatusCode, err)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&tok)
	resp.Body.Close()

	var list struct{ Games []games.Game }
	e.do("GET", "/api/games", tok.AccessToken, nil, http.StatusOK, &list)
	if len(list.Games) != 1 || list.Games[0].ID != g.ID {
		t.Fatalf("the connector sees %+v", list.Games)
	}
	e.do("POST", "/api/games/"+g.ID+"/characters", tok.AccessToken, map[string]any{"name": "X"}, http.StatusForbidden, nil)
}

// TestEveryOperationIsMembersOnly sends every operation in the spec, about
// real things in a game, as someone not in it, and expects a 404 from each:
// an endpoint that forgets app.Access fails here, including ones added
// after this test was written.
func TestEveryOperationIsMembersOnly(t *testing.T) {
	e := newEnv(t)
	var g games.Game
	e.do("POST", "/api/games", e.key, map[string]any{"name": "Heist"}, http.StatusCreated, &g)
	base := "/api/games/" + g.ID
	var id struct{ ID string }
	ids := map[string]string{"gameId": g.ID, "userId": e.userID}
	create := func(param, path string, body map[string]any, pick func([]byte) string) {
		t.Helper()
		req, _ := json.Marshal(body)
		r, err := http.NewRequest("POST", e.url+path, strings.NewReader(string(req)))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+e.key)
		r.Header.Set("Content-Type", "application/json")
		resp, err := e.client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST %s: %d %s", path, resp.StatusCode, b)
		}
		ids[param] = pick(b)
	}
	top := func(b []byte) string { _ = json.Unmarshal(b, &id); return id.ID }
	create("characterId", base+"/characters", map[string]any{"name": "Aria"}, top)
	create("conversationId", base+"/conversations", map[string]any{"title": "Docks"}, func(b []byte) string {
		var v struct{ Conversation struct{ ID string } }
		_ = json.Unmarshal(b, &v)
		return v.Conversation.ID
	})
	create("nodeId", "/api/conversations/"+ids["conversationId"]+"/nodes", map[string]any{"text": "Hello."}, top)
	create("choiceId", "/api/nodes/"+ids["nodeId"]+"/choices", map[string]any{"text": "Bye."}, top)
	create("flagId", base+"/flags", map[string]any{"key": "met"}, top)
	create("skillId", base+"/skills", map[string]any{"name": "Lockpicking"}, top)
	create("questId", base+"/quests", map[string]any{"name": "The Job"}, top)
	create("stageId", "/api/quests/"+ids["questId"]+"/stages", map[string]any{"number": 1, "journal": "Begun."}, func(b []byte) string {
		var v struct{ Stages []struct{ ID string } }
		_ = json.Unmarshal(b, &v)
		return v.Stages[0].ID
	})
	create("noteId", base+"/notes", map[string]any{"targetKind": "game", "kind": "gap", "body": "x"}, top)
	create("inviteId", base+"/invites", map[string]any{}, top)

	stranger, _ := e.person("stranger")
	spec, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	path, checked := "", 0
	for _, line := range strings.Split(string(spec), "\n") {
		if strings.HasPrefix(line, "  /") && strings.HasSuffix(line, ":") {
			path = strings.TrimSuffix(strings.TrimSpace(line), ":")
			continue
		}
		method := strings.TrimSuffix(strings.TrimSpace(line), ":")
		if !strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "     ") || !slices.Contains([]string{"get", "post", "patch", "delete"}, method) {
			continue
		}
		if path == "/me" || path == "/games" {
			continue // about the caller, not a game
		}
		p := path
		for k, v := range ids {
			p = strings.ReplaceAll(p, "{"+k+"}", v)
		}
		if strings.Contains(p, "{") {
			t.Errorf("%s %s: no id for its parameter; add one above", strings.ToUpper(method), path)
			continue
		}
		e.do(strings.ToUpper(method), "/api"+p, stranger, map[string]any{}, http.StatusNotFound, nil)
		checked++
	}
	if checked < 40 {
		t.Fatalf("checked only %d operations; did the spec's layout change?", checked)
	}
}
