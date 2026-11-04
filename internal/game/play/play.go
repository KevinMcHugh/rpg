// Package play is the game's web interface: a list of saved games, and a
// page that shows the transcript and the choices, Disco Elysium style. It
// runs no script; every choice is a form that posts and redirects.
//
// The same Handler serves the authoring tool's playtests (mounted at /play,
// playing the content as it stands, with saves per api key) and the game
// binary (mounted at /, playing content.json). See docs/mechanics.md.
package play

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/rs/xid"

	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
	"github.com/KevinMcHugh/rpg/internal/game"
	"github.com/KevinMcHugh/rpg/internal/script"
)

//go:embed templates/*.html
var templateFS embed.FS

// Shown is how much of the transcript the page shows.
const Shown = 120

// Store is where saves live.
type Store interface {
	CreateSave(ctx context.Context, arg dbgen.CreateSaveParams) (dbgen.Save, error)
	GetSave(ctx context.Context, arg dbgen.GetSaveParams) (dbgen.Save, error)
	ListSaves(ctx context.Context, arg dbgen.ListSavesParams) ([]dbgen.Save, error)
	UpdateSave(ctx context.Context, arg dbgen.UpdateSaveParams) (dbgen.Save, error)
	DeleteSave(ctx context.Context, arg dbgen.DeleteSaveParams) (int64, error)
}

// Handler serves the game.
type Handler struct {
	// Title is the page title: the game's, or "Playtest".
	Title string
	// Base is the path the handler is mounted at ("/play", or "" at the
	// root), for the links and forms it writes.
	Base string
	// Load is the game to play: in the authoring tool, the game with this
	// id; in the game binary, which plays one bundle, the id is empty.
	// Notice, if not empty, is shown above it: a playtest says when the
	// content has build errors.
	Load func(ctx context.Context, gameID string) (g *game.Game, notice string, err error)
	// Store keeps the saves.
	Store Store
	// Owner is whose saves a request sees: the playtester's api key, or nil
	// in the game.
	Owner func(ctx context.Context) *string
	// Setup, when set, offers a playtest's extra options when starting: a
	// node to start at, and effects to run first (script.ParseEffects). The
	// game leaves it nil.
	Setup func(ctx context.Context, gameID, effects string) ([]script.Effect, error)
	// Back, when set, is a link out of the game ("← Authoring").
	Back string
	// DefaultGame, when set, is the game a request that names none plays
	// (the authoring tool's first game). The game binary leaves it nil.
	DefaultGame func(ctx context.Context) (string, error)

	pages map[string]*template.Template
}

// Routes mounts the handler on r.
func (h *Handler) Routes(r chi.Router) {
	h.pages = map[string]*template.Template{}
	for _, name := range []string{"saves", "play"} {
		h.pages[name] = template.Must(template.New("layout.html").Funcs(funcs).
			ParseFS(templateFS, "templates/layout.html", "templates/"+name+".html"))
	}
	r.Get(h.Base+"/", h.saves)
	if h.Base != "" {
		r.Get(h.Base, h.saves)
	}
	r.Post(h.Base+"/new", h.newGame)
	r.Get(h.Base+"/s/{id}", h.play)
	r.Post(h.Base+"/s/{id}/choose", h.choose)
	r.Post(h.Base+"/s/{id}/begin", h.begin)
	r.Post(h.Base+"/s/{id}/delete", h.delete)
}

// Headers forbids every script, as the authoring pages do. The game binary
// wraps the handler in it.
func Headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

var funcs = template.FuncMap{
	"add": func(a, b int) int { return a + b },
	"sum": func(xs []int) int {
		n := 0
		for _, x := range xs {
			n += x
		}
		return n
	},
	"upper": strings.ToUpper,
	"neg":   func(n int) int { return -n },
	// degree names a check's outcome. Saves from before degrees only say
	// whether it succeeded.
	"degree": func(e game.Entry) string {
		switch e.Degree {
		case "critical_success":
			return "Critical success"
		case "critical_failure":
			return "Critical failure"
		}
		if e.Success {
			return "Success"
		}
		return "Failure"
	},
	"signed": func(n int) string {
		if n > 0 {
			return "+" + itoa(n)
		}
		return itoa(n)
	},
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	data["Title"] = h.Title
	data["Base"] = h.Base
	data["Back"] = h.Back
	data["Flash"] = r.URL.Query().Get("msg")
	var buf bytes.Buffer
	if err := h.pages[name].Execute(&buf, data); err != nil {
		slog.Error("play render", "page", name, "err", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "no such saved game", http.StatusNotFound)
		return
	}
	slog.Error("play", "path", r.URL.Path, "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (h *Handler) to(w http.ResponseWriter, r *http.Request, path, msg string) {
	if msg != "" {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path += sep + "msg=" + url.QueryEscape(msg)
	}
	http.Redirect(w, r, h.Base+path, http.StatusSeeOther)
}

// ---- saves ------------------------------------------------------------------

type saveRow struct {
	ID, Name, Updated string
}

// gameParam is the game a request names (?game= or a form field), or nil.
func gameParam(r *http.Request) *string {
	if id := strings.TrimSpace(r.FormValue("game")); id != "" {
		return &id
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (h *Handler) saves(w http.ResponseWriter, r *http.Request) {
	game := gameParam(r)
	if game == nil && h.DefaultGame != nil {
		id, err := h.DefaultGame(r.Context())
		if err != nil {
			h.fail(w, r, err)
			return
		}
		q := r.URL.Query()
		q.Set("game", id)
		http.Redirect(w, r, h.Base+"/?"+q.Encode(), http.StatusSeeOther)
		return
	}
	g, notice, err := h.Load(r.Context(), deref(game))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rows, err := h.Store.ListSaves(r.Context(), dbgen.ListSavesParams{CreatedBy: h.Owner(r.Context()), GameID: game})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	saves := make([]saveRow, len(rows))
	for i, s := range rows {
		saves[i] = saveRow{ID: s.ID, Name: s.Name, Updated: s.UpdatedAt.Time.Format("Jan 2 15:04")}
	}
	h.render(w, r, "saves", map[string]any{
		"Saves": saves, "Notice": notice, "Playtest": h.Setup != nil, "Game": deref(game),
		"Node": r.URL.Query().Get("node"), "HasOpening": g.Bundle().Opening != "", "Credits": g.Bundle().Credits,
	})
}

func (h *Handler) newGame(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	game := gameParam(r)
	back := "/"
	if game != nil {
		back = "/?game=" + url.QueryEscape(*game)
	}
	g, _, err := h.Load(ctx, deref(game))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s := g.Blank(rand.Uint64())
	node := ""
	if h.Setup != nil {
		// A playtest can set the world up first, and start at any line in
		// place of the opening.
		effects, err := h.Setup(ctx, deref(game), r.FormValue("setup"))
		if err != nil {
			h.to(w, r, back, err.Error())
			return
		}
		g.Apply(s, effects)
		s.Log = s.Log[:0] // the setup is not part of the story
		node = strings.TrimSpace(r.FormValue("node"))
	}
	if node == "" {
		g.Open(s)
	} else if err := g.StartAt(s, node); err != nil {
		h.to(w, r, back, "There is no line "+node+" in the content as it stands.")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = "Untitled"
	}
	state, err := json.Marshal(s)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	row, err := h.Store.CreateSave(ctx, dbgen.CreateSaveParams{ID: xid.New().String(), GameID: game, Name: name, State: state, CreatedBy: h.Owner(ctx)})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.to(w, r, "/s/"+row.ID, "")
}

// ---- playing ----------------------------------------------------------------

// load is a save, its State, and the game it is played in.
func (h *Handler) load(r *http.Request) (*game.Game, string, dbgen.Save, *game.State, error) {
	ctx := r.Context()
	row, err := h.Store.GetSave(ctx, dbgen.GetSaveParams{ID: chi.URLParam(r, "id"), CreatedBy: h.Owner(ctx)})
	if err != nil {
		return nil, "", dbgen.Save{}, nil, err
	}
	g, notice, err := h.Load(ctx, deref(row.GameID))
	if err != nil {
		return nil, "", dbgen.Save{}, nil, err
	}
	var s game.State
	if err := json.Unmarshal(row.State, &s); err != nil {
		return nil, "", dbgen.Save{}, nil, err
	}
	return g, notice, row, &s, nil
}

func (h *Handler) save(r *http.Request, row dbgen.Save, s *game.State) error {
	state, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = h.Store.UpdateSave(r.Context(), dbgen.UpdateSaveParams{ID: row.ID, State: state, CreatedBy: h.Owner(r.Context())})
	return err
}

func (h *Handler) play(w http.ResponseWriter, r *http.Request) {
	g, notice, row, s, err := h.load(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	log := s.Log
	if len(log) > Shown {
		log = log[len(log)-Shown:]
	}
	h.render(w, r, "play", map[string]any{
		"Game": deref(row.GameID), "Save": row, "Notice": notice, "Log": log, "Options": g.Options(s), "Available": g.Available(s),
		"Over": g.Over(s), "Journal": g.Journal(s), "Party": g.Party(s), "Abilities": g.Abilities(s),
	})
}

// act runs one step of play on a save and saves it, recovering a save the
// content has moved under.
func (h *Handler) act(w http.ResponseWriter, r *http.Request, step func(g *game.Game, s *game.State) error) {
	g, _, row, s, err := h.load(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	msg := ""
	switch err := step(g, s); {
	case errors.Is(err, game.ErrStale):
		g.Recover(s)
		msg = "The story changed under this save, so it picks up between conversations."
	case errors.Is(err, game.ErrUnavailable):
		msg = "That is not available any more."
	case err != nil:
		h.fail(w, r, err)
		return
	}
	if err := h.save(r, row, s); err != nil {
		h.fail(w, r, err)
		return
	}
	path := "/s/" + row.ID
	if msg != "" {
		path += "?msg=" + url.QueryEscape(msg)
	}
	http.Redirect(w, r, h.Base+path+"#now", http.StatusSeeOther)
}

func (h *Handler) choose(w http.ResponseWriter, r *http.Request) {
	h.act(w, r, func(g *game.Game, s *game.State) error { return g.Choose(s, r.FormValue("choice")) })
}

func (h *Handler) begin(w http.ResponseWriter, r *http.Request) {
	h.act(w, r, func(g *game.Game, s *game.State) error { return g.Begin(s, r.FormValue("conversation")) })
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	row, err := h.Store.GetSave(ctx, dbgen.GetSaveParams{ID: chi.URLParam(r, "id"), CreatedBy: h.Owner(ctx)})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if _, err := h.Store.DeleteSave(ctx, dbgen.DeleteSaveParams{ID: row.ID, CreatedBy: h.Owner(ctx)}); err != nil {
		h.fail(w, r, err)
		return
	}
	back := "/"
	if row.GameID != nil {
		back = "/?game=" + url.QueryEscape(*row.GameID)
	}
	h.to(w, r, back, "Deleted.")
}
