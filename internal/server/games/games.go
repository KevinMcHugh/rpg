// Package games holds the endpoints for games. A game holds its own
// characters, conversations, world and notes, and has a ruleset that says
// how its checks resolve (package rules). A game's name and key are
// authoring labels; nothing about the game itself ships but its ruleset.
//
// A game belongs to its members (package members): whoever creates one owns
// it, and only its owner may change or delete it.
package games

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rs/xid"

	apigen "github.com/KevinMcHugh/rpg/internal/api/gen"
	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/auth"
	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
)

// MaxName is the limit on a game's name, in characters.
const MaxName = 100

// Rulesets are the rulesets a game may use (the spec's Ruleset enum).
var Rulesets = []string{"classic", "srd52", "srd35", "pf2e"}

// Game is a game as every surface shows it.
type Game struct {
	ID      string `json:"id"`
	Key     string `json:"key"`
	Name    string `json:"name"`
	Ruleset string `json:"ruleset"`
	// Role is the caller's: owner or writer.
	Role      string    `json:"role"`
	UpdatedAt time.Time `json:"updatedAt"`
	URL       string    `json:"url"`
}

// Model is a stored game and the caller's role in it.
type Model struct {
	Game dbgen.Game
	Role string
}

// ToViewModel is the view of a stored game, for a caller with role.
func ToViewModel(row dbgen.Game, role string, links app.Links) Game {
	return Game{ID: row.ID, Key: row.Key, Name: row.Name, Ruleset: row.Ruleset, Role: role, UpdatedAt: row.UpdatedAt.Time, URL: links.Game(row.ID)}
}

func toAPI(g Game) apigen.Game {
	return apigen.Game{Id: g.ID, Key: g.Key, Name: g.Name, Ruleset: apigen.Ruleset(g.Ruleset), Role: apigen.Role(g.Role), UpdatedAt: g.UpdatedAt, Url: g.URL}
}

func cleanName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("%w: name is required", app.ErrBadInput)
	}
	if n := utf8.RuneCountInString(s); n > MaxName {
		return "", fmt.Errorf("%w: name is %d characters; the limit is %d", app.ErrBadInput, n, MaxName)
	}
	return s, nil
}

func cleanRuleset(r string) (string, error) {
	if !slices.Contains(Rulesets, r) {
		return "", fmt.Errorf("%w: ruleset %q is not one of %s", app.ErrBadInput, r, strings.Join(Rulesets, ", "))
	}
	return r, nil
}

// -----------------------------------------------------------------------------
// List and get
// -----------------------------------------------------------------------------

type ListStore interface {
	ListGamesForUser(ctx context.Context, userID string) ([]dbgen.ListGamesForUserRow, error)
}

// ListEndpoint is every game the caller's user is a member of, oldest
// first.
type ListEndpoint struct {
	Store ListStore
	Links app.Links
}

func (e ListEndpoint) Interact(ctx context.Context, _ apigen.ListGamesRequestObject) ([]Model, error) {
	user := auth.UserID(ctx)
	if user == "" {
		return nil, nil
	}
	rows, err := e.Store.ListGamesForUser(ctx, user)
	out := make([]Model, len(rows))
	for i, r := range rows {
		out[i] = Model{Game: dbgen.Game{ID: r.ID, Key: r.Key, Name: r.Name, Ruleset: r.Ruleset, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}, Role: r.Role}
	}
	return out, err
}

func (e ListEndpoint) Build(rows []Model) []Game {
	out := make([]Game, len(rows))
	for i, r := range rows {
		out[i] = ToViewModel(r.Game, r.Role, e.Links)
	}
	return out
}

func (ListEndpoint) Render(vms []Game) apigen.ListGamesResponseObject {
	out := make([]apigen.Game, len(vms))
	for i, g := range vms {
		out[i] = toAPI(g)
	}
	return apigen.ListGames200JSONResponse{Games: out}
}

// GetEndpoint is one game.
type GetEndpoint struct {
	Store app.GameStore
	Links app.Links
}

func (e GetEndpoint) Interact(ctx context.Context, req apigen.GetGameRequestObject) (Model, error) {
	m, err := app.Access(ctx, e.Store, "game", req.GameId)
	if err != nil {
		return Model{}, err
	}
	g, err := app.Game(ctx, e.Store, req.GameId)
	return Model{Game: g, Role: m.Role}, err
}

func (e GetEndpoint) Build(m Model) Game { return ToViewModel(m.Game, m.Role, e.Links) }

func (GetEndpoint) Render(vm Game) apigen.GetGameResponseObject {
	return apigen.GetGame200JSONResponse(toAPI(vm))
}

// -----------------------------------------------------------------------------
// Create, update, delete
// -----------------------------------------------------------------------------

type CreateStore interface {
	InTx(ctx context.Context, fn func(q *dbgen.Queries) error) error
}

// CreateEndpoint adds an empty game, owned by the caller. People only.
type CreateEndpoint struct {
	Store CreateStore
	Links app.Links
}

func (e CreateEndpoint) Interact(ctx context.Context, req apigen.CreateGameRequestObject) (dbgen.Game, error) {
	if err := app.RequireHuman(ctx); err != nil {
		return dbgen.Game{}, err
	}
	user := auth.UserID(ctx)
	if user == "" {
		return dbgen.Game{}, fmt.Errorf("%w: this key belongs to no user, so a game made with it would have no owner; give it one with server keys", app.ErrForbidden)
	}
	name, err := cleanName(req.Body.Name)
	if err != nil {
		return dbgen.Game{}, err
	}
	key, err := app.CleanKey(req.Body.Key, name)
	if err != nil {
		return dbgen.Game{}, err
	}
	ruleset := string(req.Body.Ruleset)
	if ruleset == "" {
		ruleset = "classic"
	}
	if ruleset, err = cleanRuleset(ruleset); err != nil {
		return dbgen.Game{}, err
	}
	var row dbgen.Game
	err = e.Store.InTx(ctx, func(q *dbgen.Queries) error {
		var err error
		row, err = q.CreateGame(ctx, dbgen.CreateGameParams{
			ID: xid.New().String(), Key: key, Name: name, Ruleset: ruleset, CreatedBy: auth.KeyIDPtr(ctx),
		})
		if err != nil {
			return err
		}
		return q.AddGameMember(ctx, dbgen.AddGameMemberParams{GameID: row.ID, UserID: user, Role: app.RoleOwner})
	})
	return row, err
}

func (e CreateEndpoint) Build(row dbgen.Game) Game { return ToViewModel(row, app.RoleOwner, e.Links) }

func (CreateEndpoint) Render(vm Game) apigen.CreateGameResponseObject {
	return apigen.CreateGame201JSONResponse(toAPI(vm))
}

type UpdateStore interface {
	app.AccessStore
	InTx(ctx context.Context, fn func(q *dbgen.Queries) error) error
}

// UpdateEndpoint changes a game's name, key or ruleset. Changing the
// ruleset changes how every check in it resolves; the build reports checks
// the new ruleset cannot honour. Its owner only.
type UpdateEndpoint struct {
	Store UpdateStore
	Links app.Links
}

func (e UpdateEndpoint) Interact(ctx context.Context, req apigen.UpdateGameRequestObject) (dbgen.Game, error) {
	if _, err := app.RequireOwner(ctx, e.Store, req.GameId, "change the game"); err != nil {
		return dbgen.Game{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return dbgen.Game{}, err
	}
	arg := dbgen.UpdateGameParams{ID: req.GameId}
	if n := req.Body.Name; n != nil {
		name, err := cleanName(*n)
		if err != nil {
			return dbgen.Game{}, err
		}
		arg.Name = &name
	}
	if k := req.Body.Key; k != nil {
		key, err := app.CleanKey(*k, "")
		if err != nil {
			return dbgen.Game{}, err
		}
		arg.Key = &key
	}
	if r := req.Body.Ruleset; r != nil {
		ruleset, err := cleanRuleset(string(*r))
		if err != nil {
			return dbgen.Game{}, err
		}
		arg.Ruleset = &ruleset
	}
	var row dbgen.Game
	err := e.Store.InTx(ctx, func(q *dbgen.Queries) error {
		var err error
		row, err = q.UpdateGame(ctx, arg)
		return app.NotFound(err, "game", req.GameId)
	})
	return row, err
}

// Guard refuses a change over someone else's newer one.
func (UpdateEndpoint) Guard(req apigen.UpdateGameRequestObject) app.Guard {
	return app.Guard{Kind: "game", ID: req.GameId, Seen: req.Body.UpdatedAt}
}

func (e UpdateEndpoint) Build(row dbgen.Game) Game { return ToViewModel(row, app.RoleOwner, e.Links) }

func (UpdateEndpoint) Render(vm Game) apigen.UpdateGameResponseObject {
	return apigen.UpdateGame200JSONResponse(toAPI(vm))
}

type DeleteStore interface {
	app.AccessStore
	DeleteGame(ctx context.Context, id string) (int64, error)
}

// DeleteEndpoint soft-deletes a game. Its owner only.
type DeleteEndpoint struct{ Store DeleteStore }

func (e DeleteEndpoint) Interact(ctx context.Context, req apigen.DeleteGameRequestObject) (bool, error) {
	if _, err := app.RequireOwner(ctx, e.Store, req.GameId, "delete the game"); err != nil {
		return false, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return false, err
	}
	n, err := e.Store.DeleteGame(ctx, req.GameId)
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, fmt.Errorf("%w: game %s", app.ErrNotFound, req.GameId)
	}
	return true, nil
}

func (DeleteEndpoint) Build(ok bool) bool { return ok }

func (DeleteEndpoint) Render(bool) apigen.DeleteGameResponseObject {
	return apigen.DeleteGame204Response{}
}
