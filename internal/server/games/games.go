// Package games holds the endpoints for games. A game holds its own
// characters, conversations, world and notes, and has a ruleset that says
// how its checks resolve (package rules). A game's name and key are
// authoring labels; nothing about the game itself ships but its ruleset.
package games

import (
	"context"
	"fmt"
	"slices"
	"strings"
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
	URL     string `json:"url"`
}

// ToViewModel is the view of a stored game.
func ToViewModel(row dbgen.Game, links app.Links) Game {
	return Game{ID: row.ID, Key: row.Key, Name: row.Name, Ruleset: row.Ruleset, URL: links.Game(row.ID)}
}

func toAPI(g Game) apigen.Game {
	return apigen.Game{Id: g.ID, Key: g.Key, Name: g.Name, Ruleset: apigen.Ruleset(g.Ruleset), Url: g.URL}
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
	ListGames(ctx context.Context) ([]dbgen.Game, error)
}

// ListEndpoint is every game, oldest first.
type ListEndpoint struct {
	Store ListStore
	Links app.Links
}

func (e ListEndpoint) Interact(ctx context.Context, _ apigen.ListGamesRequestObject) ([]dbgen.Game, error) {
	return e.Store.ListGames(ctx)
}

func (e ListEndpoint) Build(rows []dbgen.Game) []Game {
	out := make([]Game, len(rows))
	for i, r := range rows {
		out[i] = ToViewModel(r, e.Links)
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

func (e GetEndpoint) Interact(ctx context.Context, req apigen.GetGameRequestObject) (dbgen.Game, error) {
	return app.Game(ctx, e.Store, req.GameId)
}

func (e GetEndpoint) Build(row dbgen.Game) Game { return ToViewModel(row, e.Links) }

func (GetEndpoint) Render(vm Game) apigen.GetGameResponseObject {
	return apigen.GetGame200JSONResponse(toAPI(vm))
}

// -----------------------------------------------------------------------------
// Create, update, delete
// -----------------------------------------------------------------------------

type CreateStore interface {
	CreateGame(ctx context.Context, arg dbgen.CreateGameParams) (dbgen.Game, error)
}

// CreateEndpoint adds an empty game. People only.
type CreateEndpoint struct {
	Store CreateStore
	Links app.Links
}

func (e CreateEndpoint) Interact(ctx context.Context, req apigen.CreateGameRequestObject) (dbgen.Game, error) {
	if err := app.RequireHuman(ctx); err != nil {
		return dbgen.Game{}, err
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
	row, err := e.Store.CreateGame(ctx, dbgen.CreateGameParams{
		ID: xid.New().String(), Key: key, Name: name, Ruleset: ruleset, CreatedBy: auth.KeyIDPtr(ctx),
	})
	return row, app.KeyTaken(err, "game", key)
}

func (e CreateEndpoint) Build(row dbgen.Game) Game { return ToViewModel(row, e.Links) }

func (CreateEndpoint) Render(vm Game) apigen.CreateGameResponseObject {
	return apigen.CreateGame201JSONResponse(toAPI(vm))
}

type UpdateStore interface {
	UpdateGame(ctx context.Context, arg dbgen.UpdateGameParams) (dbgen.Game, error)
}

// UpdateEndpoint changes a game's name, key or ruleset. Changing the
// ruleset changes how every check in it resolves; the build reports checks
// the new ruleset cannot honour. People only.
type UpdateEndpoint struct {
	Store UpdateStore
	Links app.Links
}

func (e UpdateEndpoint) Interact(ctx context.Context, req apigen.UpdateGameRequestObject) (dbgen.Game, error) {
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
	row, err := e.Store.UpdateGame(ctx, arg)
	if arg.Key != nil {
		err = app.KeyTaken(err, "game", *arg.Key)
	}
	return row, app.NotFound(err, "game", req.GameId)
}

func (e UpdateEndpoint) Build(row dbgen.Game) Game { return ToViewModel(row, e.Links) }

func (UpdateEndpoint) Render(vm Game) apigen.UpdateGameResponseObject {
	return apigen.UpdateGame200JSONResponse(toAPI(vm))
}

type DeleteStore interface {
	DeleteGame(ctx context.Context, id string) (int64, error)
}

// DeleteEndpoint soft-deletes a game. People only.
type DeleteEndpoint struct{ Store DeleteStore }

func (e DeleteEndpoint) Interact(ctx context.Context, req apigen.DeleteGameRequestObject) (bool, error) {
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
