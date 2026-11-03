// Package notes holds the endpoints for notes: feedback on the game's
// content, from a person or from an agent. Creating a note is the one write
// an agent may make (`x-agent-writable` in the spec). Notes never ship.
package notes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/rs/xid"

	apigen "github.com/KevinMcHugh/rpg/internal/api/gen"
	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/auth"
	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
)

// MaxBody is the limit on a note's text, in characters.
const MaxBody = 10000

// The values the spec's enums allow, in the order the pages offer them.
var (
	TargetKinds = []string{"game", "character", "conversation", "node", "choice", "flag", "skill", "quest"}
	Kinds       = []string{"gap", "continuity", "plot_idea", "pacing", "mechanics", "question", "other"}
	Statuses    = []string{"open", "resolved", "dismissed"}
)

// -----------------------------------------------------------------------------
// View model
// -----------------------------------------------------------------------------

// Note is a note as every surface shows it.
type Note struct {
	ID         string    `json:"id"`
	TargetKind string    `json:"targetKind"`
	TargetID   string    `json:"targetId"`
	Kind       string    `json:"kind"`
	Body       string    `json:"body"`
	Status     string    `json:"status"`
	ParentID   string    `json:"parentId"`
	AuthorName string    `json:"authorName"`
	ByHuman    bool      `json:"byHuman"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	URL        string    `json:"url"`
}

// ToViewModel is the view of a stored note and the name of its author's key.
func ToViewModel(row dbgen.Note, authorName *string, links app.Links) Note {
	return Note{
		ID:         row.ID,
		TargetKind: row.TargetKind,
		TargetID:   deref(row.TargetID),
		Kind:       row.Kind,
		Body:       row.Body,
		Status:     row.Status,
		ParentID:   deref(row.ParentID),
		AuthorName: deref(authorName),
		ByHuman:    row.ByHuman,
		CreatedAt:  row.CreatedAt.Time,
		UpdatedAt:  row.UpdatedAt.Time,
		URL:        links.Target(row.TargetKind, deref(row.TargetID)),
	}
}

func toAPI(n Note) apigen.Note {
	return apigen.Note{
		Id:         n.ID,
		TargetKind: apigen.NoteTargetKind(n.TargetKind),
		TargetId:   n.TargetID,
		Kind:       apigen.NoteKind(n.Kind),
		Body:       n.Body,
		Status:     apigen.NoteStatus(n.Status),
		ParentId:   n.ParentID,
		AuthorName: n.AuthorName,
		ByHuman:    n.ByHuman,
		CreatedAt:  n.CreatedAt,
		UpdatedAt:  n.UpdatedAt,
		Url:        n.URL,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// oneOf checks that v is one of allowed.
func oneOf(field, v string, allowed []string) error {
	if !slices.Contains(allowed, v) {
		return fmt.Errorf("%w: %s %q is not one of %s", app.ErrBadInput, field, v, strings.Join(allowed, ", "))
	}
	return nil
}

func ptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// -----------------------------------------------------------------------------
// List notes
// -----------------------------------------------------------------------------

type ListStore interface {
	ListNotes(ctx context.Context, arg dbgen.ListNotesParams) ([]dbgen.ListNotesRow, error)
}

// ListEndpoint is the notes matching the filters, oldest first.
type ListEndpoint struct {
	Store ListStore
	Links app.Links
}

func (e ListEndpoint) Interact(ctx context.Context, req apigen.ListNotesRequestObject) ([]dbgen.ListNotesRow, error) {
	p := req.Params
	if p.Status != "" {
		if err := oneOf("status", string(p.Status), Statuses); err != nil {
			return nil, err
		}
	}
	if p.TargetKind != "" {
		if err := oneOf("targetKind", string(p.TargetKind), TargetKinds); err != nil {
			return nil, err
		}
	}
	return e.Store.ListNotes(ctx, dbgen.ListNotesParams{
		Status:     ptr(string(p.Status)),
		TargetKind: ptr(string(p.TargetKind)),
		TargetID:   ptr(strings.TrimSpace(p.TargetId)),
	})
}

func (e ListEndpoint) Build(rows []dbgen.ListNotesRow) []Note {
	out := make([]Note, len(rows))
	for i, r := range rows {
		out[i] = ToViewModel(r.Note, r.AuthorName, e.Links)
	}
	return out
}

func (e ListEndpoint) Render(vms []Note) apigen.ListNotesResponseObject {
	out := make([]apigen.Note, len(vms))
	for i, n := range vms {
		out[i] = toAPI(n)
	}
	return apigen.ListNotes200JSONResponse{Notes: out}
}

// -----------------------------------------------------------------------------
// Get note
// -----------------------------------------------------------------------------

type GetStore interface {
	GetNote(ctx context.Context, id string) (dbgen.GetNoteRow, error)
}

// GetEndpoint is one note.
type GetEndpoint struct {
	Store GetStore
	Links app.Links
}

func (e GetEndpoint) Interact(ctx context.Context, req apigen.GetNoteRequestObject) (dbgen.GetNoteRow, error) {
	row, err := e.Store.GetNote(ctx, req.NoteId)
	return row, app.NotFound(err, "note", req.NoteId)
}

func (e GetEndpoint) Build(row dbgen.GetNoteRow) Note {
	return ToViewModel(row.Note, row.AuthorName, e.Links)
}

func (e GetEndpoint) Render(vm Note) apigen.GetNoteResponseObject {
	return apigen.GetNote200JSONResponse(toAPI(vm))
}

// -----------------------------------------------------------------------------
// Create note
// -----------------------------------------------------------------------------

type CreateStore interface {
	GetStore
	CreateNote(ctx context.Context, arg dbgen.CreateNoteParams) (dbgen.Note, error)
	GetCharacter(ctx context.Context, id string) (dbgen.Character, error)
	GetConversation(ctx context.Context, id string) (dbgen.Conversation, error)
	GetNode(ctx context.Context, id string) (dbgen.Node, error)
	GetChoice(ctx context.Context, id string) (dbgen.Choice, error)
	GetFlag(ctx context.Context, id string) (dbgen.Flag, error)
	GetSkill(ctx context.Context, id string) (dbgen.Skill, error)
	GetQuest(ctx context.Context, id string) (dbgen.Quest, error)
}

// CreateEndpoint leaves a note, or replies to one. Anyone may: it is how an
// agent contributes without writing the game's text.
type CreateEndpoint struct {
	Store CreateStore
	Links app.Links
}

func (e CreateEndpoint) Interact(ctx context.Context, req apigen.CreateNoteRequestObject) (dbgen.GetNoteRow, error) {
	b := req.Body
	body := strings.TrimSpace(b.Body)
	if body == "" {
		return dbgen.GetNoteRow{}, fmt.Errorf("%w: body is required", app.ErrBadInput)
	}
	if n := utf8.RuneCountInString(body); n > MaxBody {
		return dbgen.GetNoteRow{}, fmt.Errorf("%w: body is %d characters; the limit is %d", app.ErrBadInput, n, MaxBody)
	}
	kind, targetKind, targetID := string(b.Kind), string(b.TargetKind), strings.TrimSpace(b.TargetId)
	var parentID *string
	if id := strings.TrimSpace(b.ParentId); id != "" {
		parent, err := e.Store.GetNote(ctx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return dbgen.GetNoteRow{}, fmt.Errorf("%w: no note %s to reply to", app.ErrBadInput, id)
			}
			return dbgen.GetNoteRow{}, err
		}
		// A reply joins its parent's thread: same target, and the same kind
		// unless it says otherwise.
		parentID = &parent.Note.ID
		targetKind, targetID = parent.Note.TargetKind, deref(parent.Note.TargetID)
		if kind == "" {
			kind = parent.Note.Kind
		}
	}
	if kind == "" {
		kind = "other"
	}
	if err := oneOf("kind", kind, Kinds); err != nil {
		return dbgen.GetNoteRow{}, err
	}
	if targetKind == "" {
		targetKind = "game"
	}
	if err := oneOf("targetKind", targetKind, TargetKinds); err != nil {
		return dbgen.GetNoteRow{}, err
	}
	if err := e.checkTarget(ctx, targetKind, targetID); err != nil {
		return dbgen.GetNoteRow{}, err
	}
	row, err := e.Store.CreateNote(ctx, dbgen.CreateNoteParams{
		ID:         xid.New().String(),
		TargetKind: targetKind,
		TargetID:   ptr(targetID),
		Kind:       kind,
		Body:       body,
		ParentID:   parentID,
		ByHuman:    auth.IsHuman(ctx),
		CreatedBy:  auth.KeyIDPtr(ctx),
	})
	if err != nil {
		return dbgen.GetNoteRow{}, err
	}
	return e.Store.GetNote(ctx, row.ID)
}

// checkTarget makes sure a note is about something that exists.
func (e CreateEndpoint) checkTarget(ctx context.Context, kind, id string) error {
	if kind == "game" {
		if id != "" {
			return fmt.Errorf("%w: a note on the whole game takes no targetId", app.ErrBadInput)
		}
		return nil
	}
	if id == "" {
		return fmt.Errorf("%w: targetId is required for a note on a %s", app.ErrBadInput, kind)
	}
	var err error
	switch kind {
	case "character":
		_, err = e.Store.GetCharacter(ctx, id)
	case "conversation":
		_, err = e.Store.GetConversation(ctx, id)
	case "node":
		_, err = e.Store.GetNode(ctx, id)
	case "choice":
		_, err = e.Store.GetChoice(ctx, id)
	case "flag":
		_, err = e.Store.GetFlag(ctx, id)
	case "skill":
		_, err = e.Store.GetSkill(ctx, id)
	case "quest":
		_, err = e.Store.GetQuest(ctx, id)
	}
	return app.NotFound(err, kind, id)
}

func (e CreateEndpoint) Build(row dbgen.GetNoteRow) Note {
	return ToViewModel(row.Note, row.AuthorName, e.Links)
}

func (e CreateEndpoint) Render(vm Note) apigen.CreateNoteResponseObject {
	return apigen.CreateNote201JSONResponse(toAPI(vm))
}

// -----------------------------------------------------------------------------
// Update note
// -----------------------------------------------------------------------------

type UpdateStore interface {
	GetStore
	SetNoteStatus(ctx context.Context, arg dbgen.SetNoteStatusParams) (dbgen.Note, error)
}

// UpdateEndpoint resolves, dismisses or reopens a note. People only: the
// author decides what feedback to act on.
type UpdateEndpoint struct {
	Store UpdateStore
	Links app.Links
}

func (e UpdateEndpoint) Interact(ctx context.Context, req apigen.UpdateNoteRequestObject) (dbgen.GetNoteRow, error) {
	if err := app.RequireHuman(ctx); err != nil {
		return dbgen.GetNoteRow{}, err
	}
	if err := oneOf("status", string(req.Body.Status), Statuses); err != nil {
		return dbgen.GetNoteRow{}, err
	}
	if _, err := e.Store.SetNoteStatus(ctx, dbgen.SetNoteStatusParams{ID: req.NoteId, Status: string(req.Body.Status)}); err != nil {
		return dbgen.GetNoteRow{}, app.NotFound(err, "note", req.NoteId)
	}
	row, err := e.Store.GetNote(ctx, req.NoteId)
	return row, app.NotFound(err, "note", req.NoteId)
}

func (e UpdateEndpoint) Build(row dbgen.GetNoteRow) Note {
	return ToViewModel(row.Note, row.AuthorName, e.Links)
}

func (e UpdateEndpoint) Render(vm Note) apigen.UpdateNoteResponseObject {
	return apigen.UpdateNote200JSONResponse(toAPI(vm))
}

// -----------------------------------------------------------------------------
// Delete note
// -----------------------------------------------------------------------------

type DeleteStore interface {
	DeleteNote(ctx context.Context, id string) (int64, error)
}

// DeleteEndpoint soft-deletes a note. People only.
type DeleteEndpoint struct{ Store DeleteStore }

func (e DeleteEndpoint) Interact(ctx context.Context, req apigen.DeleteNoteRequestObject) (bool, error) {
	if err := app.RequireHuman(ctx); err != nil {
		return false, err
	}
	n, err := e.Store.DeleteNote(ctx, req.NoteId)
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, fmt.Errorf("%w: note %s", app.ErrNotFound, req.NoteId)
	}
	return true, nil
}

func (DeleteEndpoint) Build(ok bool) bool { return ok }

func (DeleteEndpoint) Render(bool) apigen.DeleteNoteResponseObject {
	return apigen.DeleteNote204Response{}
}
