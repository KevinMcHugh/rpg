// Package characters holds the endpoints for the game's characters. A
// character's name is player-facing text, so only a person may set it
// (app.Revise); the bible is authoring material that never ships.
package characters

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rs/xid"

	apigen "github.com/KevinMcHugh/rpg/internal/api/gen"
	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/auth"
	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
)

// Limits, in characters.
const (
	MaxName  = 100
	MaxBible = 50000
)

// -----------------------------------------------------------------------------
// View model
// -----------------------------------------------------------------------------

// Character is a character as every surface shows it.
type Character struct {
	ID        string    `json:"id"`
	GameID    string    `json:"gameId"`
	Key       string    `json:"key"`
	Companion bool      `json:"companion"`
	Name      string    `json:"name"`
	Bible     string    `json:"bible"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	URL       string    `json:"url"`
}

// ToViewModel is the view of a stored character.
func ToViewModel(row dbgen.Character, links app.Links) Character {
	return Character{
		ID:        row.ID,
		GameID:    row.GameID,
		Key:       row.Key,
		Companion: row.Companion,
		Name:      row.Name,
		Bible:     row.Bible,
		CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
		URL:       links.Character(row.ID),
	}
}

func toAPI(c Character) apigen.Character {
	return apigen.Character{
		Id:        c.ID,
		GameId:    c.GameID,
		Key:       c.Key,
		Companion: c.Companion,
		Name:      c.Name,
		Bible:     c.Bible,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
		Url:       c.URL,
	}
}

// -----------------------------------------------------------------------------
// Validation
// -----------------------------------------------------------------------------

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

func cleanBible(s string) (string, error) {
	s = strings.TrimSpace(s)
	if n := utf8.RuneCountInString(s); n > MaxBible {
		return "", fmt.Errorf("%w: bible is %d characters; the limit is %d", app.ErrBadInput, n, MaxBible)
	}
	return s, nil
}

// -----------------------------------------------------------------------------
// List characters
// -----------------------------------------------------------------------------

type ListStore interface {
	app.GameStore
	PageCharacters(ctx context.Context, gameID string, p app.PageRequest) (app.Page[dbgen.Character], error)
}

// ListEndpoint is a page of the characters in a game, by name.
type ListEndpoint struct {
	Links app.Links
}

// Page is the page the caller asked for.
func (ListEndpoint) Page(req apigen.ListCharactersRequestObject) app.PageRequest {
	return app.PageRequest{List: "character", Limit: req.Params.Limit, After: req.Params.After}
}

func (e ListEndpoint) Interact(ctx context.Context, store ListStore, req apigen.ListCharactersRequestObject) (app.Page[dbgen.Character], error) {
	if _, err := app.Game(ctx, store, req.GameId); err != nil {
		return app.Page[dbgen.Character]{}, err
	}
	return store.PageCharacters(ctx, req.GameId, e.Page(req))
}

func (e ListEndpoint) Build(p app.Page[dbgen.Character]) app.Page[Character] {
	return app.MapPage(p, func(r dbgen.Character) Character { return ToViewModel(r, e.Links) })
}

func (e ListEndpoint) Render(p app.Page[Character]) apigen.ListCharactersResponseObject {
	return apigen.ListCharacters200JSONResponse{Characters: app.MapPage(p, toAPI).Items, Next: p.Next}
}

// -----------------------------------------------------------------------------
// Get character
// -----------------------------------------------------------------------------

type GetStore interface {
	app.AccessStore
	GetCharacter(ctx context.Context, id string) (dbgen.Character, error)
}

// GetEndpoint is one character.
type GetEndpoint struct {
	Links app.Links
}

func (e GetEndpoint) Interact(ctx context.Context, store GetStore, req apigen.GetCharacterRequestObject) (dbgen.Character, error) {
	if _, err := app.Access(ctx, store, "character", req.CharacterId); err != nil {
		return dbgen.Character{}, err
	}
	row, err := store.GetCharacter(ctx, req.CharacterId)
	return row, app.NotFound(err, "character", req.CharacterId)
}

func (e GetEndpoint) Build(row dbgen.Character) Character { return ToViewModel(row, e.Links) }

func (e GetEndpoint) Render(vm Character) apigen.GetCharacterResponseObject {
	return apigen.GetCharacter200JSONResponse(toAPI(vm))
}

// -----------------------------------------------------------------------------
// Create character
// -----------------------------------------------------------------------------

type TxStore interface {
	app.AccessStore
	InTx(ctx context.Context, fn func(q *dbgen.Queries) error) error
}

// CreateEndpoint saves a new character in a game, with a revision
// recording who wrote the name. People only.
type CreateEndpoint struct {
	Links app.Links
}

func (e CreateEndpoint) Interact(ctx context.Context, store TxStore, req apigen.CreateCharacterRequestObject) (dbgen.Character, error) {
	if _, err := app.Access(ctx, store, "game", req.GameId); err != nil {
		return dbgen.Character{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return dbgen.Character{}, err
	}
	name, err := cleanName(req.Body.Name)
	if err != nil {
		return dbgen.Character{}, err
	}
	bible, err := cleanBible(req.Body.Bible)
	if err != nil {
		return dbgen.Character{}, err
	}
	key, err := app.CleanKey(req.Body.Key, name)
	if err != nil {
		return dbgen.Character{}, err
	}
	var row dbgen.Character
	err = store.InTx(ctx, func(q *dbgen.Queries) error {
		if _, err := app.Game(ctx, q, req.GameId); err != nil {
			return err
		}
		id := xid.New().String()
		rev, err := app.Revise(ctx, q, "character", id, "name", name)
		if err != nil {
			return err
		}
		row, err = q.CreateCharacter(ctx, dbgen.CreateCharacterParams{
			ID:             id,
			GameID:         req.GameId,
			Key:            key,
			Name:           name,
			NameRevisionID: rev,
			Bible:          bible,
			Companion:      req.Body.Companion,
			CreatedBy:      auth.KeyIDPtr(ctx),
		})
		return app.KeyTaken(err, "character", key)
	})
	return row, err
}

func (e CreateEndpoint) Build(row dbgen.Character) Character { return ToViewModel(row, e.Links) }

func (e CreateEndpoint) Render(vm Character) apigen.CreateCharacterResponseObject {
	return apigen.CreateCharacter201JSONResponse(toAPI(vm))
}

// -----------------------------------------------------------------------------
// Update character
// -----------------------------------------------------------------------------

// UpdateEndpoint changes a character's name, bible, or both. People only.
type UpdateEndpoint struct {
	Links app.Links
}

func (e UpdateEndpoint) Interact(ctx context.Context, store TxStore, req apigen.UpdateCharacterRequestObject) (dbgen.Character, error) {
	if _, err := app.Access(ctx, store, "character", req.CharacterId); err != nil {
		return dbgen.Character{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return dbgen.Character{}, err
	}
	arg := dbgen.UpdateCharacterParams{ID: req.CharacterId, Companion: req.Body.Companion}
	if k := req.Body.Key; k != nil {
		key, err := app.CleanKey(*k, "")
		if err != nil {
			return dbgen.Character{}, err
		}
		arg.Key = &key
	}
	if b := req.Body.Bible; b != nil {
		bible, err := cleanBible(*b)
		if err != nil {
			return dbgen.Character{}, err
		}
		arg.Bible = &bible
	}
	var name string
	if n := req.Body.Name; n != nil {
		var err error
		if name, err = cleanName(*n); err != nil {
			return dbgen.Character{}, err
		}
	}
	var row dbgen.Character
	err := store.InTx(ctx, func(q *dbgen.Queries) error {
		if err := app.Claim(ctx, q, e.Guard(req)); err != nil {
			return err
		}
		cur, err := q.GetCharacter(ctx, req.CharacterId)
		if err != nil {
			return app.NotFound(err, "character", req.CharacterId)
		}
		// An unchanged name needs no new revision.
		if name != "" && name != cur.Name {
			rev, err := app.Revise(ctx, q, "character", cur.ID, "name", name)
			if err != nil {
				return err
			}
			arg.Name, arg.NameRevisionID = &name, &rev
		}
		row, err = q.UpdateCharacter(ctx, arg)
		if arg.Key != nil {
			err = app.KeyTaken(err, "character", *arg.Key)
		}
		return app.NotFound(err, "character", req.CharacterId)
	})
	return row, err
}

// Guard refuses a change over someone else's newer one.
func (UpdateEndpoint) Guard(req apigen.UpdateCharacterRequestObject) app.Guard {
	return app.Guard{Kind: "character", ID: req.CharacterId, Seen: req.Body.UpdatedAt}
}

func (e UpdateEndpoint) Build(row dbgen.Character) Character { return ToViewModel(row, e.Links) }

func (e UpdateEndpoint) Render(vm Character) apigen.UpdateCharacterResponseObject {
	return apigen.UpdateCharacter200JSONResponse(toAPI(vm))
}

// -----------------------------------------------------------------------------
// Delete character
// -----------------------------------------------------------------------------

type DeleteStore interface {
	app.AccessStore
	DeleteCharacter(ctx context.Context, id string) (int64, error)
}

// DeleteEndpoint soft-deletes a character. Lines they spoke stay, and the
// build reports them until they get a new speaker. People only.
type DeleteEndpoint struct{}

func (e DeleteEndpoint) Interact(ctx context.Context, store DeleteStore, req apigen.DeleteCharacterRequestObject) (bool, error) {
	if _, err := app.Access(ctx, store, "character", req.CharacterId); err != nil {
		return false, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return false, err
	}
	n, err := store.DeleteCharacter(ctx, req.CharacterId)
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, fmt.Errorf("%w: character %s", app.ErrNotFound, req.CharacterId)
	}
	return true, nil
}

func (DeleteEndpoint) Build(ok bool) bool { return ok }

func (DeleteEndpoint) Render(bool) apigen.DeleteCharacterResponseObject {
	return apigen.DeleteCharacter204Response{}
}
