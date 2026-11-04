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

// MaxDifficulty bounds a skill check's difficulty. 2d6 plus a skill rarely
// tops 20.
const MaxDifficulty = 30

// choiceModel is a stored choice and the world its view needs.
type choiceModel struct {
	Choice dbgen.Choice
	World  world
}

// check is a skill check as a choice stores it.
type check struct {
	skill                 *string
	difficulty            int32
	fail, critS, critFail *string
}

// branches are where a check can go besides the choice's target.
type branches struct{ fail, critSuccess, critFail string }

// cleanCheck validates a skill check on a choice from node from, in game
// gameID. No skill is no check; a check needs a difficulty and a skill in
// the same game, and its failure node (optional: none ends the
// conversation) must be in the same conversation.
func cleanCheck(ctx context.Context, q *dbgen.Queries, from dbgen.Node, gameID, skillID string, difficulty int, to branches) (check, error) {
	skillID = strings.TrimSpace(skillID)
	if skillID == "" {
		if to != (branches{}) {
			return check{}, fmt.Errorf("%w: a failure or critical node needs a skill check", app.ErrBadInput)
		}
		return check{}, nil
	}
	sk, err := q.GetSkill(ctx, skillID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return check{}, fmt.Errorf("%w: no skill %s", app.ErrBadInput, skillID)
		}
		return check{}, err
	}
	if err := app.SameGame("skill "+skillID, sk.GameID, gameID); err != nil {
		return check{}, err
	}
	if difficulty < 1 || difficulty > MaxDifficulty {
		return check{}, fmt.Errorf("%w: a skill check's difficulty is 1 to %d, not %d", app.ErrBadInput, MaxDifficulty, difficulty)
	}
	c := check{skill: &skillID, difficulty: int32(difficulty)}
	for _, b := range []struct {
		id   string
		into **string
	}{{to.fail, &c.fail}, {to.critSuccess, &c.critS}, {to.critFail, &c.critFail}} {
		if id := strings.TrimSpace(b.id); id != "" {
			if err := inConversation(ctx, q, id, from.ConversationID); err != nil {
				return check{}, err
			}
			*b.into = &id
		}
	}
	return c, nil
}

// -----------------------------------------------------------------------------
// Create choice
// -----------------------------------------------------------------------------

// CreateChoiceEndpoint adds a choice at a node, with a revision recording
// who wrote it. Its target must be in the same conversation; no target ends
// the conversation. People only.
type CreateChoiceEndpoint struct{ Store TxStore }

func (e CreateChoiceEndpoint) Interact(ctx context.Context, req apigen.CreateChoiceRequestObject) (choiceModel, error) {
	if _, err := app.Access(ctx, e.Store, "node", req.NodeId); err != nil {
		return choiceModel{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return choiceModel{}, err
	}
	b := req.Body
	text, err := clean("text", b.Text, MaxChoice, true)
	if err != nil {
		return choiceModel{}, err
	}
	var m choiceModel
	err = e.Store.InTx(ctx, func(q *dbgen.Queries) error {
		from, err := q.GetNode(ctx, req.NodeId)
		if err != nil {
			return app.NotFound(err, "node", req.NodeId)
		}
		var to *string
		if id := strings.TrimSpace(b.ToNodeId); id != "" {
			if err := inConversation(ctx, q, id, from.ConversationID); err != nil {
				return err
			}
			to = &id
		}
		game, err := gameOf(ctx, q, from)
		if err != nil {
			return err
		}
		chk, err := cleanCheck(ctx, q, from, game, b.CheckSkillId, b.CheckDifficulty, branches{b.FailNodeId, b.CritSuccessNodeId, b.CritFailNodeId})
		if err != nil {
			return err
		}
		w, err := loadWorld(ctx, q, game)
		if err != nil {
			return err
		}
		cond, err := app.ParseCond("condition", b.Condition, w.Names)
		if err != nil {
			return err
		}
		effects, err := app.ParseEffects("effects", b.Effects, w.Names)
		if err != nil {
			return err
		}
		id := xid.New().String()
		rev, err := app.Revise(ctx, q, "choice", id, "text", text)
		if err != nil {
			return err
		}
		row, err := q.CreateChoice(ctx, dbgen.CreateChoiceParams{
			ID:                id,
			FromNodeID:        from.ID,
			ToNodeID:          to,
			Text:              text,
			TextRevisionID:    rev,
			SortOrder:         int32(b.SortOrder),
			Condition:         cond,
			Effects:           effects,
			CheckSkillID:      chk.skill,
			CheckDifficulty:   chk.difficulty,
			FailNodeID:        chk.fail,
			CritSuccessNodeID: chk.critS,
			CritFailNodeID:    chk.critFail,
			CreatedBy:         auth.KeyIDPtr(ctx),
		})
		m = choiceModel{Choice: row, World: w}
		return err
	})
	return m, err
}

func (CreateChoiceEndpoint) Build(m choiceModel) Choice { return choiceView(m.Choice, m.World) }

func (CreateChoiceEndpoint) Render(vm Choice) apigen.CreateChoiceResponseObject {
	return apigen.CreateChoice201JSONResponse(toAPIChoice(vm))
}

// -----------------------------------------------------------------------------
// Update choice
// -----------------------------------------------------------------------------

// UpdateChoiceEndpoint changes a choice's text, target, order, condition,
// effects or skill check. People only.
type UpdateChoiceEndpoint struct{ Store TxStore }

func (e UpdateChoiceEndpoint) Interact(ctx context.Context, req apigen.UpdateChoiceRequestObject) (choiceModel, error) {
	if _, err := app.Access(ctx, e.Store, "choice", req.ChoiceId); err != nil {
		return choiceModel{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return choiceModel{}, err
	}
	b := req.Body
	var text string
	if t := b.Text; t != nil {
		var err error
		if text, err = clean("text", *t, MaxChoice, true); err != nil {
			return choiceModel{}, err
		}
	}
	var m choiceModel
	err := e.Store.InTx(ctx, func(q *dbgen.Queries) error {
		cur, err := q.GetChoice(ctx, req.ChoiceId)
		if err != nil {
			return app.NotFound(err, "choice", req.ChoiceId)
		}
		from, err := q.GetNode(ctx, cur.FromNodeID)
		if err != nil {
			return fmt.Errorf("choice %s: its node: %w", cur.ID, err)
		}
		game, err := gameOf(ctx, q, from)
		if err != nil {
			return err
		}
		w, err := loadWorld(ctx, q, game)
		if err != nil {
			return err
		}
		arg := dbgen.UpdateChoiceParams{ID: cur.ID}
		if s := b.SortOrder; s != nil {
			order := int32(*s)
			arg.SortOrder = &order
		}
		if t := b.ToNodeId; t != nil {
			arg.SetTarget = true
			if id := strings.TrimSpace(*t); id != "" {
				if err := inConversation(ctx, q, id, from.ConversationID); err != nil {
					return err
				}
				arg.ToNodeID = &id
			}
		}
		if c := b.Condition; c != nil {
			arg.SetCondition = true
			if arg.Condition, err = app.ParseCond("condition", *c, w.Names); err != nil {
				return err
			}
		}
		if ef := b.Effects; ef != nil {
			if arg.Effects, err = app.ParseEffects("effects", *ef, w.Names); err != nil {
				return err
			}
		}
		// Any part of the check sent replaces the check, keeping the parts
		// not sent.
		if b.CheckSkillId != nil || b.CheckDifficulty != nil || b.FailNodeId != nil || b.CritSuccessNodeId != nil || b.CritFailNodeId != nil {
			skill, difficulty := deref(cur.CheckSkillID), int(cur.CheckDifficulty)
			to := branches{deref(cur.FailNodeID), deref(cur.CritSuccessNodeID), deref(cur.CritFailNodeID)}
			if b.CheckSkillId != nil {
				skill = *b.CheckSkillId
			}
			if b.CheckDifficulty != nil {
				difficulty = *b.CheckDifficulty
			}
			for _, f := range []struct {
				sent *string
				into *string
			}{{b.FailNodeId, &to.fail}, {b.CritSuccessNodeId, &to.critSuccess}, {b.CritFailNodeId, &to.critFail}} {
				if f.sent != nil {
					*f.into = *f.sent
				}
			}
			if strings.TrimSpace(skill) == "" {
				to = branches{} // no check, so nowhere else to go
			}
			chk, err := cleanCheck(ctx, q, from, game, skill, difficulty, to)
			if err != nil {
				return err
			}
			arg.SetCheck, arg.CheckSkillID, arg.CheckDifficulty = true, chk.skill, chk.difficulty
			arg.FailNodeID, arg.CritSuccessNodeID, arg.CritFailNodeID = chk.fail, chk.critS, chk.critFail
		}
		// Unchanged text needs no new revision.
		if text != "" && text != cur.Text {
			rev, err := app.Revise(ctx, q, "choice", cur.ID, "text", text)
			if err != nil {
				return err
			}
			arg.Text, arg.TextRevisionID = &text, &rev
		}
		row, err := q.UpdateChoice(ctx, arg)
		m = choiceModel{Choice: row, World: w}
		return app.NotFound(err, "choice", req.ChoiceId)
	})
	return m, err
}

// Guard refuses a change over someone else's newer one.
func (UpdateChoiceEndpoint) Guard(req apigen.UpdateChoiceRequestObject) app.Guard {
	return app.Guard{Kind: "choice", ID: req.ChoiceId, Seen: req.Body.UpdatedAt}
}

func (UpdateChoiceEndpoint) Build(m choiceModel) Choice { return choiceView(m.Choice, m.World) }

func (UpdateChoiceEndpoint) Render(vm Choice) apigen.UpdateChoiceResponseObject {
	return apigen.UpdateChoice200JSONResponse(toAPIChoice(vm))
}

// -----------------------------------------------------------------------------
// Delete choice
// -----------------------------------------------------------------------------

type DeleteChoiceStore interface {
	app.AccessStore
	DeleteChoice(ctx context.Context, id string) (int64, error)
}

// DeleteChoiceEndpoint soft-deletes a choice. People only.
type DeleteChoiceEndpoint struct{ Store DeleteChoiceStore }

func (e DeleteChoiceEndpoint) Interact(ctx context.Context, req apigen.DeleteChoiceRequestObject) (bool, error) {
	if _, err := app.Access(ctx, e.Store, "choice", req.ChoiceId); err != nil {
		return false, err
	}
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
