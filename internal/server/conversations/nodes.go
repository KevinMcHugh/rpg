package conversations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/rs/xid"

	apigen "github.com/KevinMcHugh/rpg/internal/api/gen"
	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/auth"
	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
)

// TxStore runs the writes that record a text revision beside the row.
type TxStore interface {
	app.AccessStore
	InTx(ctx context.Context, fn func(q *dbgen.Queries) error) error
}

// speaker checks that a speaker id names a live character in the game. An
// empty id is the narrator.
func speaker(ctx context.Context, q *dbgen.Queries, id, gameID string) (*string, error) {
	if id == "" {
		return nil, nil
	}
	c, err := q.GetCharacter(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: no character %s", app.ErrBadInput, id)
		}
		return nil, err
	}
	if err := app.SameGame("character "+id, c.GameID, gameID); err != nil {
		return nil, err
	}
	return &c.ID, nil
}

// gameOf is the game a node is in, through its conversation.
func gameOf(ctx context.Context, q *dbgen.Queries, n dbgen.Node) (string, error) {
	c, err := q.GetConversation(ctx, n.ConversationID)
	if err != nil {
		return "", fmt.Errorf("node %s: its conversation: %w", n.ID, err)
	}
	return c.GameID, nil
}

// -----------------------------------------------------------------------------
// Create node
// -----------------------------------------------------------------------------

// CreateNodeEndpoint adds a line to a conversation, with a revision
// recording who wrote it. The first line becomes the start if there is
// none. People only.
type CreateNodeEndpoint struct {
	Store TxStore
	Links app.Links
}

func (e CreateNodeEndpoint) Interact(ctx context.Context, req apigen.CreateNodeRequestObject) (nodeModel, error) {
	if _, err := app.Access(ctx, e.Store, "conversation", req.ConversationId); err != nil {
		return nodeModel{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return nodeModel{}, err
	}
	text, err := clean("text", req.Body.Text, MaxLine, true)
	if err != nil {
		return nodeModel{}, err
	}
	var m nodeModel
	err = e.Store.InTx(ctx, func(q *dbgen.Queries) error {
		conv, err := q.GetConversation(ctx, req.ConversationId)
		if err != nil {
			return app.NotFound(err, "conversation", req.ConversationId)
		}
		speakerID, err := speaker(ctx, q, strings.TrimSpace(req.Body.SpeakerId), conv.GameID)
		if err != nil {
			return err
		}
		w, err := loadWorld(ctx, q, conv.GameID)
		if err != nil {
			return err
		}
		effects, err := app.ParseEffects("effects", req.Body.Effects, w.Names)
		if err != nil {
			return err
		}
		id := xid.New().String()
		rev, err := app.Revise(ctx, q, "node", id, "text", text)
		if err != nil {
			return err
		}
		row, err := q.CreateNode(ctx, dbgen.CreateNodeParams{
			ID:             id,
			ConversationID: conv.ID,
			SpeakerID:      speakerID,
			Text:           text,
			TextRevisionID: rev,
			Effects:        effects,
			CreatedBy:      auth.KeyIDPtr(ctx),
		})
		if err != nil {
			return err
		}
		if conv.StartNodeID == nil {
			if _, err := q.UpdateConversation(ctx, dbgen.UpdateConversationParams{ID: conv.ID, SetStart: true, StartNodeID: &row.ID}); err != nil {
				return err
			}
		}
		m = nodeModel{Node: row, World: w}
		return nil
	})
	return m, err
}

func (e CreateNodeEndpoint) Build(m nodeModel) Node { return nodeView(m, e.Links) }

func (e CreateNodeEndpoint) Render(vm Node) apigen.CreateNodeResponseObject {
	return apigen.CreateNode201JSONResponse(toAPINode(vm))
}

// -----------------------------------------------------------------------------
// Update node
// -----------------------------------------------------------------------------

// UpdateNodeEndpoint changes a node's speaker, text or effects. People only.
type UpdateNodeEndpoint struct {
	Store TxStore
	Links app.Links
}

func (e UpdateNodeEndpoint) Interact(ctx context.Context, req apigen.UpdateNodeRequestObject) (nodeModel, error) {
	if _, err := app.Access(ctx, e.Store, "node", req.NodeId); err != nil {
		return nodeModel{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return nodeModel{}, err
	}
	var text string
	if t := req.Body.Text; t != nil {
		var err error
		if text, err = clean("text", *t, MaxLine, true); err != nil {
			return nodeModel{}, err
		}
	}
	var m nodeModel
	err := e.Store.InTx(ctx, func(q *dbgen.Queries) error {
		cur, err := q.GetNode(ctx, req.NodeId)
		if err != nil {
			return app.NotFound(err, "node", req.NodeId)
		}
		game, err := gameOf(ctx, q, cur)
		if err != nil {
			return err
		}
		arg := dbgen.UpdateNodeParams{ID: cur.ID}
		if s := req.Body.SpeakerId; s != nil {
			arg.SetSpeaker = true
			if arg.SpeakerID, err = speaker(ctx, q, strings.TrimSpace(*s), game); err != nil {
				return err
			}
		}
		if ef := req.Body.Effects; ef != nil {
			names, err := app.LoadNames(ctx, q, game)
			if err != nil {
				return err
			}
			if arg.Effects, err = app.ParseEffects("effects", *ef, names); err != nil {
				return err
			}
		}
		// An unchanged line needs no new revision.
		if text != "" && text != cur.Text {
			rev, err := app.Revise(ctx, q, "node", cur.ID, "text", text)
			if err != nil {
				return err
			}
			arg.Text, arg.TextRevisionID = &text, &rev
		}
		row, err := q.UpdateNode(ctx, arg)
		if err != nil {
			return app.NotFound(err, "node", req.NodeId)
		}
		m, err = loadNode(ctx, q, row, game)
		return err
	})
	return m, err
}

func (e UpdateNodeEndpoint) Build(m nodeModel) Node { return nodeView(m, e.Links) }

func (e UpdateNodeEndpoint) Render(vm Node) apigen.UpdateNodeResponseObject {
	return apigen.UpdateNode200JSONResponse(toAPINode(vm))
}

// loadNode fills in a node's choices and the world of its game.
func loadNode(ctx context.Context, q *dbgen.Queries, row dbgen.Node, gameID string) (nodeModel, error) {
	all, err := q.ListChoicesByConversation(ctx, row.ConversationID)
	if err != nil {
		return nodeModel{}, err
	}
	var choices []dbgen.Choice
	for _, c := range all {
		if c.FromNodeID == row.ID {
			choices = append(choices, c)
		}
	}
	w, err := loadWorld(ctx, q, gameID)
	if err != nil {
		return nodeModel{}, err
	}
	return nodeModel{Node: row, Choices: choices, World: w}, nil
}

// -----------------------------------------------------------------------------
// Delete node
// -----------------------------------------------------------------------------

// DeleteNodeEndpoint soft-deletes a node and the choices it offers. Choices
// elsewhere that led to it stay, and the build reports them until they are
// pointed somewhere else. People only.
type DeleteNodeEndpoint struct{ Store TxStore }

func (e DeleteNodeEndpoint) Interact(ctx context.Context, req apigen.DeleteNodeRequestObject) (bool, error) {
	if _, err := app.Access(ctx, e.Store, "node", req.NodeId); err != nil {
		return false, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return false, err
	}
	err := e.Store.InTx(ctx, func(q *dbgen.Queries) error {
		n, err := q.DeleteNode(ctx, req.NodeId)
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: node %s", app.ErrNotFound, req.NodeId)
		}
		return q.DeleteChoicesFromNode(ctx, req.NodeId)
	})
	return err == nil, err
}

func (DeleteNodeEndpoint) Build(ok bool) bool { return ok }

func (DeleteNodeEndpoint) Render(bool) apigen.DeleteNodeResponseObject {
	return apigen.DeleteNode204Response{}
}
