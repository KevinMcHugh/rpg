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
	ListCharacters(ctx context.Context) ([]dbgen.Character, error)
}

// ListEndpoint is every character, by name.
type ListEndpoint struct {
	Store ListStore
	Links app.Links
}

func (e ListEndpoint) Interact(ctx context.Context, _ apigen.ListCharactersRequestObject) ([]dbgen.Character, error) {
	return e.Store.ListCharacters(ctx)
}

func (e ListEndpoint) Build(rows []dbgen.Character) []Character {
	out := make([]Character, len(rows))
	for i, r := range rows {
		out[i] = ToViewModel(r, e.Links)
	}
	return out
}

func (e ListEndpoint) Render(vms []Character) apigen.ListCharactersResponseObject {
	out := make([]apigen.Character, len(vms))
	for i, c := range vms {
		out[i] = toAPI(c)
	}
	return apigen.ListCharacters200JSONResponse{Characters: out}
}

// -----------------------------------------------------------------------------
// Get character
// -----------------------------------------------------------------------------

type GetStore interface {
	GetCharacter(ctx context.Context, id string) (dbgen.Character, error)
}

// GetEndpoint is one character.
type GetEndpoint struct {
	Store GetStore
	Links app.Links
}

func (e GetEndpoint) Interact(ctx context.Context, req apigen.GetCharacterRequestObject) (dbgen.Character, error) {
	row, err := e.Store.GetCharacter(ctx, req.CharacterId)
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
	InTx(ctx context.Context, fn func(q *dbgen.Queries) error) error
}

// CreateEndpoint saves a new character, with a revision recording who
// wrote the name. People only.
type CreateEndpoint struct {
	Store TxStore
	Links app.Links
}

func (e CreateEndpoint) Interact(ctx context.Context, req apigen.CreateCharacterRequestObject) (dbgen.Character, error) {
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
	err = e.Store.InTx(ctx, func(q *dbgen.Queries) error {
		id := xid.New().String()
		rev, err := app.Revise(ctx, q, "character", id, "name", name)
		if err != nil {
			return err
		}
		row, err = q.CreateCharacter(ctx, dbgen.CreateCharacterParams{
			ID:             id,
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
	Store TxStore
	Links app.Links
}

func (e UpdateEndpoint) Interact(ctx context.Context, req apigen.UpdateCharacterRequestObject) (dbgen.Character, error) {
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
	err := e.Store.InTx(ctx, func(q *dbgen.Queries) error {
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

func (e UpdateEndpoint) Build(row dbgen.Character) Character { return ToViewModel(row, e.Links) }

func (e UpdateEndpoint) Render(vm Character) apigen.UpdateCharacterResponseObject {
	return apigen.UpdateCharacter200JSONResponse(toAPI(vm))
}

// -----------------------------------------------------------------------------
// Delete character
// -----------------------------------------------------------------------------

type DeleteStore interface {
	DeleteCharacter(ctx context.Context, id string) (int64, error)
}

// DeleteEndpoint soft-deletes a character. Lines they spoke stay, and the
// build reports them until they get a new speaker. People only.
type DeleteEndpoint struct{ Store DeleteStore }

func (e DeleteEndpoint) Interact(ctx context.Context, req apigen.DeleteCharacterRequestObject) (bool, error) {
	if err := app.RequireHuman(ctx); err != nil {
		return false, err
	}
	n, err := e.Store.DeleteCharacter(ctx, req.CharacterId)
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
