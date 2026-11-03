package conversations

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/xid"

	apigen "github.com/KevinMcHugh/rpg/internal/api/gen"
	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/auth"
	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
)

// -----------------------------------------------------------------------------
// Create choice
// -----------------------------------------------------------------------------

// CreateChoiceEndpoint adds a choice at a node, with a revision recording
// who wrote it. Its target must be in the same conversation; no target ends
// the conversation. People only.
type CreateChoiceEndpoint struct{ Store TxStore }

func (e CreateChoiceEndpoint) Interact(ctx context.Context, req apigen.CreateChoiceRequestObject) (dbgen.Choice, error) {
	if err := app.RequireHuman(ctx); err != nil {
		return dbgen.Choice{}, err
	}
	text, err := clean("text", req.Body.Text, MaxChoice, true)
	if err != nil {
		return dbgen.Choice{}, err
	}
	var row dbgen.Choice
	err = e.Store.InTx(ctx, func(q *dbgen.Queries) error {
		from, err := q.GetNode(ctx, req.NodeId)
		if err != nil {
			return app.NotFound(err, "node", req.NodeId)
		}
		var to *string
		if id := strings.TrimSpace(req.Body.ToNodeId); id != "" {
			if err := inConversation(ctx, q, id, from.ConversationID); err != nil {
				return err
			}
			to = &id
		}
		id := xid.New().String()
		rev, err := app.Revise(ctx, q, "choice", id, "text", text)
		if err != nil {
			return err
		}
		row, err = q.CreateChoice(ctx, dbgen.CreateChoiceParams{
			ID:             id,
			FromNodeID:     from.ID,
			ToNodeID:       to,
			Text:           text,
			TextRevisionID: rev,
			SortOrder:      int32(req.Body.SortOrder),
			CreatedBy:      auth.KeyIDPtr(ctx),
		})
		return err
	})
	return row, err
}

func (CreateChoiceEndpoint) Build(row dbgen.Choice) Choice { return choiceView(row) }

func (CreateChoiceEndpoint) Render(vm Choice) apigen.CreateChoiceResponseObject {
	return apigen.CreateChoice201JSONResponse(toAPIChoice(vm))
}

// -----------------------------------------------------------------------------
// Update choice
// -----------------------------------------------------------------------------

// UpdateChoiceEndpoint changes a choice's text, target or order. People
// only.
type UpdateChoiceEndpoint struct{ Store TxStore }

func (e UpdateChoiceEndpoint) Interact(ctx context.Context, req apigen.UpdateChoiceRequestObject) (dbgen.Choice, error) {
	if err := app.RequireHuman(ctx); err != nil {
		return dbgen.Choice{}, err
	}
	var text string
	if t := req.Body.Text; t != nil {
		var err error
		if text, err = clean("text", *t, MaxChoice, true); err != nil {
			return dbgen.Choice{}, err
		}
	}
	var row dbgen.Choice
	err := e.Store.InTx(ctx, func(q *dbgen.Queries) error {
		cur, err := q.GetChoice(ctx, req.ChoiceId)
		if err != nil {
			return app.NotFound(err, "choice", req.ChoiceId)
		}
		arg := dbgen.UpdateChoiceParams{ID: cur.ID}
		if s := req.Body.SortOrder; s != nil {
			order := int32(*s)
			arg.SortOrder = &order
		}
		if t := req.Body.ToNodeId; t != nil {
			arg.SetTarget = true
			if id := strings.TrimSpace(*t); id != "" {
				from, err := q.GetNode(ctx, cur.FromNodeID)
				if err != nil {
					return fmt.Errorf("choice %s: its node: %w", cur.ID, err)
				}
				if err := inConversation(ctx, q, id, from.ConversationID); err != nil {
					return err
				}
				arg.ToNodeID = &id
			}
		}
		// Unchanged text needs no new revision.
		if text != "" && text != cur.Text {
			rev, err := app.Revise(ctx, q, "choice", cur.ID, "text", text)
			if err != nil {
				return err
			}
			arg.Text, arg.TextRevisionID = &text, &rev
		}
		row, err = q.UpdateChoice(ctx, arg)
		return app.NotFound(err, "choice", req.ChoiceId)
	})
	return row, err
}

func (UpdateChoiceEndpoint) Build(row dbgen.Choice) Choice { return choiceView(row) }

func (UpdateChoiceEndpoint) Render(vm Choice) apigen.UpdateChoiceResponseObject {
	return apigen.UpdateChoice200JSONResponse(toAPIChoice(vm))
}

// -----------------------------------------------------------------------------
// Delete choice
// -----------------------------------------------------------------------------

type DeleteChoiceStore interface {
	DeleteChoice(ctx context.Context, id string) (int64, error)
}

// DeleteChoiceEndpoint soft-deletes a choice. People only.
type DeleteChoiceEndpoint struct{ Store DeleteChoiceStore }

func (e DeleteChoiceEndpoint) Interact(ctx context.Context, req apigen.DeleteChoiceRequestObject) (bool, error) {
	if err := app.RequireHuman(ctx); err != nil {
		return false, err
	}
	n, err := e.Store.DeleteChoice(ctx, req.ChoiceId)
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, fmt.Errorf("%w: choice %s", app.ErrNotFound, req.ChoiceId)
	}
	return true, nil
}

func (DeleteChoiceEndpoint) Build(ok bool) bool { return ok }

func (DeleteChoiceEndpoint) Render(bool) apigen.DeleteChoiceResponseObject {
	return apigen.DeleteChoice204Response{}
}
