// Package world holds the endpoints for the game's state and rules:
// flags, skills, and quests with their stages. A skill's name, a quest's
// name and a stage's journal entry are player-facing, so only a person may
// write them (app.Revise). Flags and keys are authoring-only. See
// docs/mechanics.md.
package world

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
	"github.com/KevinMcHugh/rpg/internal/rules"
)

// Limits, in characters.
const (
	MaxName        = 100
	MaxDescription = 5000
	MaxJournal     = 5000
)

func clean(field, s string, limit int, required bool) (string, error) {
	s = strings.TrimSpace(s)
	if required && s == "" {
		return "", fmt.Errorf("%w: %s is required", app.ErrBadInput, field)
	}
	if n := utf8.RuneCountInString(s); n > limit {
		return "", fmt.Errorf("%w: %s is %d characters; the limit is %d", app.ErrBadInput, field, n, limit)
	}
	return s, nil
}

// cleanAbility checks a skill's ability: one of the six, or empty for none.
func cleanAbility(a string) (*string, error) {
	a = strings.TrimSpace(a)
	if a == "" {
		return nil, nil
	}
	if !slices.Contains(rules.Abilities, a) {
		return nil, fmt.Errorf("%w: ability %q is not one of %s", app.ErrBadInput, a, strings.Join(rules.Abilities, ", "))
	}
	return &a, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// TxStore runs the writes that record a text revision beside the row.
type TxStore interface {
	app.AccessStore
	InTx(ctx context.Context, fn func(q *dbgen.Queries) error) error
}

// -----------------------------------------------------------------------------
// Flags
// -----------------------------------------------------------------------------

// Flag is a named number in the world state; 0 is false.
type Flag struct {
	ID           string    `json:"id"`
	GameID       string    `json:"gameId"`
	Key          string    `json:"key"`
	Description  string    `json:"description"`
	InitialValue int       `json:"initialValue"`
	UpdatedAt    time.Time `json:"updatedAt"`
	URL          string    `json:"url"`
}

func flagView(row dbgen.Flag, links app.Links) Flag {
	return Flag{ID: row.ID, GameID: row.GameID, Key: row.Key, Description: row.Description, InitialValue: int(row.InitialValue), UpdatedAt: row.UpdatedAt.Time, URL: links.World(row.GameID) + "#flag-" + row.ID}
}

func flagAPI(f Flag) apigen.Flag {
	return apigen.Flag{Id: f.ID, GameId: f.GameID, Key: f.Key, Description: f.Description, InitialValue: f.InitialValue, UpdatedAt: f.UpdatedAt, Url: f.URL}
}

type ListFlagsStore interface {
	app.GameStore
	PageFlags(ctx context.Context, gameID string, p app.PageRequest) (app.Page[dbgen.Flag], error)
}

// ListFlagsEndpoint is a page of the flags in a game, by key.
type ListFlagsEndpoint struct {
	Links app.Links
}

// Page is the page the caller asked for.
func (ListFlagsEndpoint) Page(req apigen.ListFlagsRequestObject) app.PageRequest {
	return app.PageRequest{List: "flag", Limit: req.Params.Limit, After: req.Params.After}
}

func (e ListFlagsEndpoint) Interact(ctx context.Context, store ListFlagsStore, req apigen.ListFlagsRequestObject) (app.Page[dbgen.Flag], error) {
	if _, err := app.Game(ctx, store, req.GameId); err != nil {
		return app.Page[dbgen.Flag]{}, err
	}
	return store.PageFlags(ctx, req.GameId, e.Page(req))
}

func (e ListFlagsEndpoint) Build(p app.Page[dbgen.Flag]) app.Page[Flag] {
	return app.MapPage(p, func(r dbgen.Flag) Flag { return flagView(r, e.Links) })
}

func (ListFlagsEndpoint) Render(p app.Page[Flag]) apigen.ListFlagsResponseObject {
	return apigen.ListFlags200JSONResponse{Flags: app.MapPage(p, flagAPI).Items, Next: p.Next}
}

type CreateFlagStore interface {
	app.GameStore
	CreateFlag(ctx context.Context, arg dbgen.CreateFlagParams) (dbgen.Flag, error)
}

// CreateFlagEndpoint adds a flag. People only.
type CreateFlagEndpoint struct {
	Links app.Links
}

func (e CreateFlagEndpoint) Interact(ctx context.Context, store CreateFlagStore, req apigen.CreateFlagRequestObject) (dbgen.Flag, error) {
	if _, err := app.Access(ctx, store, "game", req.GameId); err != nil {
		return dbgen.Flag{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return dbgen.Flag{}, err
	}
	key, err := app.CleanKey(req.Body.Key, "")
	if err != nil {
		return dbgen.Flag{}, err
	}
	desc, err := clean("description", req.Body.Description, MaxDescription, false)
	if err != nil {
		return dbgen.Flag{}, err
	}
	if _, err := app.Game(ctx, store, req.GameId); err != nil {
		return dbgen.Flag{}, err
	}
	row, err := store.CreateFlag(ctx, dbgen.CreateFlagParams{
		ID: xid.New().String(), GameID: req.GameId, Key: key, Description: desc, InitialValue: int32(req.Body.InitialValue), CreatedBy: auth.KeyIDPtr(ctx),
	})
	return row, app.KeyTaken(err, "flag", key)
}

func (e CreateFlagEndpoint) Build(row dbgen.Flag) Flag { return flagView(row, e.Links) }

func (CreateFlagEndpoint) Render(vm Flag) apigen.CreateFlagResponseObject {
	return apigen.CreateFlag201JSONResponse(flagAPI(vm))
}

type UpdateFlagStore interface {
	app.AccessStore
	InTx(ctx context.Context, fn func(q *dbgen.Queries) error) error
}

// UpdateFlagEndpoint changes a flag. Scripts refer to it by id, so a new
// key shows in them at once. People only.
type UpdateFlagEndpoint struct {
	Links app.Links
}

func (e UpdateFlagEndpoint) Interact(ctx context.Context, store UpdateFlagStore, req apigen.UpdateFlagRequestObject) (dbgen.Flag, error) {
	if _, err := app.Access(ctx, store, "flag", req.FlagId); err != nil {
		return dbgen.Flag{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return dbgen.Flag{}, err
	}
	arg := dbgen.UpdateFlagParams{ID: req.FlagId}
	if k := req.Body.Key; k != nil {
		key, err := app.CleanKey(*k, "")
		if err != nil {
			return dbgen.Flag{}, err
		}
		arg.Key = &key
	}
	if d := req.Body.Description; d != nil {
		desc, err := clean("description", *d, MaxDescription, false)
		if err != nil {
			return dbgen.Flag{}, err
		}
		arg.Description = &desc
	}
	if v := req.Body.InitialValue; v != nil {
		n := int32(*v)
		arg.InitialValue = &n
	}
	var row dbgen.Flag
	err := store.InTx(ctx, func(q *dbgen.Queries) error {
		if err := app.Claim(ctx, q, e.Guard(req)); err != nil {
			return err
		}
		var err error
		row, err = q.UpdateFlag(ctx, arg)
		if arg.Key != nil {
			err = app.KeyTaken(err, "flag", *arg.Key)
		}
		return app.NotFound(err, "flag", req.FlagId)
	})
	return row, err
}

// Guard refuses a change over someone else's newer one.
func (UpdateFlagEndpoint) Guard(req apigen.UpdateFlagRequestObject) app.Guard {
	return app.Guard{Kind: "flag", ID: req.FlagId, Seen: req.Body.UpdatedAt}
}

func (e UpdateFlagEndpoint) Build(row dbgen.Flag) Flag { return flagView(row, e.Links) }

func (UpdateFlagEndpoint) Render(vm Flag) apigen.UpdateFlagResponseObject {
	return apigen.UpdateFlag200JSONResponse(flagAPI(vm))
}

type DeleteFlagStore interface {
	app.AccessStore
	DeleteFlag(ctx context.Context, id string) (int64, error)
}

// DeleteFlagEndpoint soft-deletes a flag. Scripts that use it stay, and the
// build reports them. People only.
type DeleteFlagEndpoint struct{}

func (e DeleteFlagEndpoint) Interact(ctx context.Context, store DeleteFlagStore, req apigen.DeleteFlagRequestObject) (bool, error) {
	if _, err := app.Access(ctx, store, "flag", req.FlagId); err != nil {
		return false, err
	}
	return deleted(ctx, "flag", req.FlagId, store.DeleteFlag)
}

func (DeleteFlagEndpoint) Build(ok bool) bool { return ok }

func (DeleteFlagEndpoint) Render(bool) apigen.DeleteFlagResponseObject {
	return apigen.DeleteFlag204Response{}
}

// deleted runs a soft delete for a person, as every delete here does.
func deleted(ctx context.Context, kind, id string, del func(context.Context, string) (int64, error)) (bool, error) {
	if err := app.RequireHuman(ctx); err != nil {
		return false, err
	}
	n, err := del(ctx, id)
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, fmt.Errorf("%w: %s %s", app.ErrNotFound, kind, id)
	}
	return true, nil
}

// -----------------------------------------------------------------------------
// Skills
// -----------------------------------------------------------------------------

// Skill is something the player is good at, at a level.
type Skill struct {
	ID           string    `json:"id"`
	GameID       string    `json:"gameId"`
	Key          string    `json:"key"`
	Name         string    `json:"name"`
	InitialLevel int       `json:"initialLevel"`
	Ability      string    `json:"ability"`
	UpdatedAt    time.Time `json:"updatedAt"`
	URL          string    `json:"url"`
}

func skillView(row dbgen.Skill, links app.Links) Skill {
	return Skill{ID: row.ID, GameID: row.GameID, Key: row.Key, Name: row.Name, InitialLevel: int(row.InitialLevel), Ability: deref(row.Ability), UpdatedAt: row.UpdatedAt.Time, URL: links.World(row.GameID) + "#skill-" + row.ID}
}

func skillAPI(s Skill) apigen.Skill {
	return apigen.Skill{Id: s.ID, GameId: s.GameID, Key: s.Key, Name: s.Name, InitialLevel: s.InitialLevel, Ability: s.Ability, UpdatedAt: s.UpdatedAt, Url: s.URL}
}

type ListSkillsStore interface {
	app.GameStore
	PageSkills(ctx context.Context, gameID string, p app.PageRequest) (app.Page[dbgen.Skill], error)
}

// ListSkillsEndpoint is a page of the skills in a game, by name.
type ListSkillsEndpoint struct {
	Links app.Links
}

// Page is the page the caller asked for.
func (ListSkillsEndpoint) Page(req apigen.ListSkillsRequestObject) app.PageRequest {
	return app.PageRequest{List: "skill", Limit: req.Params.Limit, After: req.Params.After}
}

func (e ListSkillsEndpoint) Interact(ctx context.Context, store ListSkillsStore, req apigen.ListSkillsRequestObject) (app.Page[dbgen.Skill], error) {
	if _, err := app.Game(ctx, store, req.GameId); err != nil {
		return app.Page[dbgen.Skill]{}, err
	}
	return store.PageSkills(ctx, req.GameId, e.Page(req))
}

func (e ListSkillsEndpoint) Build(p app.Page[dbgen.Skill]) app.Page[Skill] {
	return app.MapPage(p, func(r dbgen.Skill) Skill { return skillView(r, e.Links) })
}

func (ListSkillsEndpoint) Render(p app.Page[Skill]) apigen.ListSkillsResponseObject {
	return apigen.ListSkills200JSONResponse{Skills: app.MapPage(p, skillAPI).Items, Next: p.Next}
}

// CreateSkillEndpoint adds a skill, with a revision recording who wrote its
// name. People only.
type CreateSkillEndpoint struct {
	Links app.Links
}

func (e CreateSkillEndpoint) Interact(ctx context.Context, store TxStore, req apigen.CreateSkillRequestObject) (dbgen.Skill, error) {
	if _, err := app.Access(ctx, store, "game", req.GameId); err != nil {
		return dbgen.Skill{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return dbgen.Skill{}, err
	}
	name, err := clean("name", req.Body.Name, MaxName, true)
	if err != nil {
		return dbgen.Skill{}, err
	}
	key, err := app.CleanKey(req.Body.Key, name)
	if err != nil {
		return dbgen.Skill{}, err
	}
	ability, err := cleanAbility(string(req.Body.Ability))
	if err != nil {
		return dbgen.Skill{}, err
	}
	var row dbgen.Skill
	err = store.InTx(ctx, func(q *dbgen.Queries) error {
		if _, err := app.Game(ctx, q, req.GameId); err != nil {
			return err
		}
		id := xid.New().String()
		rev, err := app.Revise(ctx, q, "skill", id, "name", name)
		if err != nil {
			return err
		}
		row, err = q.CreateSkill(ctx, dbgen.CreateSkillParams{
			ID: id, GameID: req.GameId, Key: key, Name: name, NameRevisionID: rev, InitialLevel: int32(req.Body.InitialLevel),
			Ability: ability, CreatedBy: auth.KeyIDPtr(ctx),
		})
		return app.KeyTaken(err, "skill", key)
	})
	return row, err
}

func (e CreateSkillEndpoint) Build(row dbgen.Skill) Skill { return skillView(row, e.Links) }

func (CreateSkillEndpoint) Render(vm Skill) apigen.CreateSkillResponseObject {
	return apigen.CreateSkill201JSONResponse(skillAPI(vm))
}

// UpdateSkillEndpoint changes a skill. People only.
type UpdateSkillEndpoint struct {
	Links app.Links
}

func (e UpdateSkillEndpoint) Interact(ctx context.Context, store TxStore, req apigen.UpdateSkillRequestObject) (dbgen.Skill, error) {
	if _, err := app.Access(ctx, store, "skill", req.SkillId); err != nil {
		return dbgen.Skill{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return dbgen.Skill{}, err
	}
	arg := dbgen.UpdateSkillParams{ID: req.SkillId}
	if k := req.Body.Key; k != nil {
		key, err := app.CleanKey(*k, "")
		if err != nil {
			return dbgen.Skill{}, err
		}
		arg.Key = &key
	}
	if l := req.Body.InitialLevel; l != nil {
		n := int32(*l)
		arg.InitialLevel = &n
	}
	if a := req.Body.Ability; a != nil {
		ability, err := cleanAbility(*a)
		if err != nil {
			return dbgen.Skill{}, err
		}
		arg.SetAbility, arg.Ability = true, ability
	}
	var name string
	if n := req.Body.Name; n != nil {
		var err error
		if name, err = clean("name", *n, MaxName, true); err != nil {
			return dbgen.Skill{}, err
		}
	}
	var row dbgen.Skill
	err := store.InTx(ctx, func(q *dbgen.Queries) error {
		if err := app.Claim(ctx, q, e.Guard(req)); err != nil {
			return err
		}
		cur, err := q.GetSkill(ctx, req.SkillId)
		if err != nil {
			return app.NotFound(err, "skill", req.SkillId)
		}
		if name != "" && name != cur.Name {
			rev, err := app.Revise(ctx, q, "skill", cur.ID, "name", name)
			if err != nil {
				return err
			}
			arg.Name, arg.NameRevisionID = &name, &rev
		}
		row, err = q.UpdateSkill(ctx, arg)
		if arg.Key != nil {
			err = app.KeyTaken(err, "skill", *arg.Key)
		}
		return app.NotFound(err, "skill", req.SkillId)
	})
	return row, err
}

// Guard refuses a change over someone else's newer one.
func (UpdateSkillEndpoint) Guard(req apigen.UpdateSkillRequestObject) app.Guard {
	return app.Guard{Kind: "skill", ID: req.SkillId, Seen: req.Body.UpdatedAt}
}

func (e UpdateSkillEndpoint) Build(row dbgen.Skill) Skill { return skillView(row, e.Links) }

func (UpdateSkillEndpoint) Render(vm Skill) apigen.UpdateSkillResponseObject {
	return apigen.UpdateSkill200JSONResponse(skillAPI(vm))
}

type DeleteSkillStore interface {
	app.AccessStore
	DeleteSkill(ctx context.Context, id string) (int64, error)
}

// DeleteSkillEndpoint soft-deletes a skill. People only.
type DeleteSkillEndpoint struct{}

func (e DeleteSkillEndpoint) Interact(ctx context.Context, store DeleteSkillStore, req apigen.DeleteSkillRequestObject) (bool, error) {
	if _, err := app.Access(ctx, store, "skill", req.SkillId); err != nil {
		return false, err
	}
	return deleted(ctx, "skill", req.SkillId, store.DeleteSkill)
}

func (DeleteSkillEndpoint) Build(ok bool) bool { return ok }

func (DeleteSkillEndpoint) Render(bool) apigen.DeleteSkillResponseObject {
	return apigen.DeleteSkill204Response{}
}

// -----------------------------------------------------------------------------
// Quests
// -----------------------------------------------------------------------------

// Quest is a quest with its stages, in order.
type Quest struct {
	ID        string    `json:"id"`
	GameID    string    `json:"gameId"`
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	Summary   string    `json:"summary"`
	Stages    []Stage   `json:"stages"`
	UpdatedAt time.Time `json:"updatedAt"`
	URL       string    `json:"url"`
}

// Stage is a step of a quest, with its journal entry.
type Stage struct {
	ID        string    `json:"id"`
	QuestID   string    `json:"questId"`
	Number    int       `json:"number"`
	Journal   string    `json:"journal"`
	Completes bool      `json:"completes"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type questModel struct {
	Quest  dbgen.Quest
	Stages []dbgen.QuestStage
}

func questView(m questModel, links app.Links) Quest {
	stages := make([]Stage, len(m.Stages))
	for i, s := range m.Stages {
		stages[i] = Stage{ID: s.ID, QuestID: s.QuestID, Number: int(s.Number), Journal: s.Journal, Completes: s.Completes, UpdatedAt: s.UpdatedAt.Time}
	}
	return Quest{ID: m.Quest.ID, GameID: m.Quest.GameID, Key: m.Quest.Key, Name: m.Quest.Name, Summary: m.Quest.Summary, Stages: stages, UpdatedAt: m.Quest.UpdatedAt.Time, URL: links.Quest(m.Quest.ID)}
}

func questAPI(q Quest) apigen.Quest {
	stages := make([]apigen.QuestStage, len(q.Stages))
	for i, s := range q.Stages {
		stages[i] = apigen.QuestStage{Id: s.ID, QuestId: s.QuestID, Number: s.Number, Journal: s.Journal, Completes: s.Completes, UpdatedAt: s.UpdatedAt}
	}
	return apigen.Quest{Id: q.ID, GameId: q.GameID, Key: q.Key, Name: q.Name, Summary: q.Summary, Stages: stages, UpdatedAt: q.UpdatedAt, Url: q.URL}
}

type QuestReadStore interface {
	app.AccessStore
	GetQuest(ctx context.Context, id string) (dbgen.Quest, error)
	ListQuestStages(ctx context.Context, questID string) ([]dbgen.QuestStage, error)
}

func loadQuest(ctx context.Context, s QuestReadStore, id string) (questModel, error) {
	q, err := s.GetQuest(ctx, id)
	if err != nil {
		return questModel{}, app.NotFound(err, "quest", id)
	}
	stages, err := s.ListQuestStages(ctx, id)
	return questModel{Quest: q, Stages: stages}, err
}

type ListQuestsStore interface {
	app.GameStore
	PageQuests(ctx context.Context, gameID string, p app.PageRequest) (app.Page[dbgen.Quest], error)
	ListAllQuestStages(ctx context.Context, gameID string) ([]dbgen.QuestStage, error)
}

// ListQuestsEndpoint is a page of the quests in a game, by name, with
// their stages.
type ListQuestsEndpoint struct {
	Links app.Links
}

// Page is the page the caller asked for.
func (ListQuestsEndpoint) Page(req apigen.ListQuestsRequestObject) app.PageRequest {
	return app.PageRequest{List: "quest", Limit: req.Params.Limit, After: req.Params.After}
}

func (e ListQuestsEndpoint) Interact(ctx context.Context, store ListQuestsStore, req apigen.ListQuestsRequestObject) (app.Page[questModel], error) {
	if _, err := app.Game(ctx, store, req.GameId); err != nil {
		return app.Page[questModel]{}, err
	}
	quests, err := store.PageQuests(ctx, req.GameId, e.Page(req))
	if err != nil {
		return app.Page[questModel]{}, err
	}
	stages, err := store.ListAllQuestStages(ctx, req.GameId)
	if err != nil {
		return app.Page[questModel]{}, err
	}
	byQuest := map[string][]dbgen.QuestStage{}
	for _, s := range stages {
		byQuest[s.QuestID] = append(byQuest[s.QuestID], s)
	}
	return app.MapPage(quests, func(q dbgen.Quest) questModel { return questModel{Quest: q, Stages: byQuest[q.ID]} }), nil
}

func (e ListQuestsEndpoint) Build(p app.Page[questModel]) app.Page[Quest] {
	return app.MapPage(p, func(m questModel) Quest { return questView(m, e.Links) })
}

func (ListQuestsEndpoint) Render(p app.Page[Quest]) apigen.ListQuestsResponseObject {
	return apigen.ListQuests200JSONResponse{Quests: app.MapPage(p, questAPI).Items, Next: p.Next}
}

// GetQuestEndpoint is one quest with its stages.
type GetQuestEndpoint struct {
	Links app.Links
}

func (e GetQuestEndpoint) Interact(ctx context.Context, store QuestReadStore, req apigen.GetQuestRequestObject) (questModel, error) {
	if _, err := app.Access(ctx, store, "quest", req.QuestId); err != nil {
		return questModel{}, err
	}
	return loadQuest(ctx, store, req.QuestId)
}

func (e GetQuestEndpoint) Build(m questModel) Quest { return questView(m, e.Links) }

func (GetQuestEndpoint) Render(vm Quest) apigen.GetQuestResponseObject {
	return apigen.GetQuest200JSONResponse(questAPI(vm))
}

// CreateQuestEndpoint adds a quest with no stages, with a revision
// recording who wrote its name. People only.
type CreateQuestEndpoint struct {
	Links app.Links
}

func (e CreateQuestEndpoint) Interact(ctx context.Context, store TxStore, req apigen.CreateQuestRequestObject) (questModel, error) {
	if _, err := app.Access(ctx, store, "game", req.GameId); err != nil {
		return questModel{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return questModel{}, err
	}
	name, err := clean("name", req.Body.Name, MaxName, true)
	if err != nil {
		return questModel{}, err
	}
	summary, err := clean("summary", req.Body.Summary, MaxDescription, false)
	if err != nil {
		return questModel{}, err
	}
	key, err := app.CleanKey(req.Body.Key, name)
	if err != nil {
		return questModel{}, err
	}
	var row dbgen.Quest
	err = store.InTx(ctx, func(q *dbgen.Queries) error {
		if _, err := app.Game(ctx, q, req.GameId); err != nil {
			return err
		}
		id := xid.New().String()
		rev, err := app.Revise(ctx, q, "quest", id, "name", name)
		if err != nil {
			return err
		}
		row, err = q.CreateQuest(ctx, dbgen.CreateQuestParams{
			ID: id, GameID: req.GameId, Key: key, Name: name, NameRevisionID: rev, Summary: summary, CreatedBy: auth.KeyIDPtr(ctx),
		})
		return app.KeyTaken(err, "quest", key)
	})
	return questModel{Quest: row}, err
}

func (e CreateQuestEndpoint) Build(m questModel) Quest { return questView(m, e.Links) }

func (CreateQuestEndpoint) Render(vm Quest) apigen.CreateQuestResponseObject {
	return apigen.CreateQuest201JSONResponse(questAPI(vm))
}

// UpdateQuestEndpoint changes a quest's key, name or summary. People only.
type UpdateQuestEndpoint struct {
	Links app.Links
}

func (e UpdateQuestEndpoint) Interact(ctx context.Context, store TxStore, req apigen.UpdateQuestRequestObject) (questModel, error) {
	if _, err := app.Access(ctx, store, "quest", req.QuestId); err != nil {
		return questModel{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return questModel{}, err
	}
	arg := dbgen.UpdateQuestParams{ID: req.QuestId}
	if k := req.Body.Key; k != nil {
		key, err := app.CleanKey(*k, "")
		if err != nil {
			return questModel{}, err
		}
		arg.Key = &key
	}
	if s := req.Body.Summary; s != nil {
		summary, err := clean("summary", *s, MaxDescription, false)
		if err != nil {
			return questModel{}, err
		}
		arg.Summary = &summary
	}
	var name string
	if n := req.Body.Name; n != nil {
		var err error
		if name, err = clean("name", *n, MaxName, true); err != nil {
			return questModel{}, err
		}
	}
	var m questModel
	err := store.InTx(ctx, func(q *dbgen.Queries) error {
		if err := app.Claim(ctx, q, e.Guard(req)); err != nil {
			return err
		}
		cur, err := q.GetQuest(ctx, req.QuestId)
		if err != nil {
			return app.NotFound(err, "quest", req.QuestId)
		}
		if name != "" && name != cur.Name {
			rev, err := app.Revise(ctx, q, "quest", cur.ID, "name", name)
			if err != nil {
				return err
			}
			arg.Name, arg.NameRevisionID = &name, &rev
		}
		if _, err = q.UpdateQuest(ctx, arg); err != nil {
			if arg.Key != nil {
				err = app.KeyTaken(err, "quest", *arg.Key)
			}
			return app.NotFound(err, "quest", req.QuestId)
		}
		m, err = loadQuest(ctx, q, cur.ID)
		return err
	})
	return m, err
}

// Guard refuses a change over someone else's newer one.
func (UpdateQuestEndpoint) Guard(req apigen.UpdateQuestRequestObject) app.Guard {
	return app.Guard{Kind: "quest", ID: req.QuestId, Seen: req.Body.UpdatedAt}
}

func (e UpdateQuestEndpoint) Build(m questModel) Quest { return questView(m, e.Links) }

func (UpdateQuestEndpoint) Render(vm Quest) apigen.UpdateQuestResponseObject {
	return apigen.UpdateQuest200JSONResponse(questAPI(vm))
}

// DeleteQuestEndpoint soft-deletes a quest and its stages. People only.
type DeleteQuestEndpoint struct{}

func (e DeleteQuestEndpoint) Interact(ctx context.Context, store TxStore, req apigen.DeleteQuestRequestObject) (bool, error) {
	if _, err := app.Access(ctx, store, "quest", req.QuestId); err != nil {
		return false, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return false, err
	}
	err := store.InTx(ctx, func(q *dbgen.Queries) error {
		n, err := q.DeleteQuest(ctx, req.QuestId)
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: quest %s", app.ErrNotFound, req.QuestId)
		}
		return q.DeleteQuestStages(ctx, req.QuestId)
	})
	return err == nil, err
}

func (DeleteQuestEndpoint) Build(ok bool) bool { return ok }

func (DeleteQuestEndpoint) Render(bool) apigen.DeleteQuestResponseObject {
	return apigen.DeleteQuest204Response{}
}

// -----------------------------------------------------------------------------
// Quest stages
// -----------------------------------------------------------------------------

func stageNumber(n int) (int32, error) {
	if n < 1 || n > 999 {
		return 0, fmt.Errorf("%w: a stage's number is 1 to 999, not %d (0 is not started)", app.ErrBadInput, n)
	}
	return int32(n), nil
}

// stageTaken turns a unique-index violation on (quest, number) into
// ErrBadInput.
func stageTaken(err error, n int32) error {
	if e := app.KeyTaken(err, "stage", ""); e != err {
		return fmt.Errorf("%w: the quest already has a stage %d", app.ErrBadInput, n)
	}
	return err
}

// CreateStageEndpoint adds a stage to a quest, with a revision recording
// who wrote its journal entry. People only.
type CreateStageEndpoint struct {
	Links app.Links
}

func (e CreateStageEndpoint) Interact(ctx context.Context, store TxStore, req apigen.CreateQuestStageRequestObject) (questModel, error) {
	if _, err := app.Access(ctx, store, "quest", req.QuestId); err != nil {
		return questModel{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return questModel{}, err
	}
	number, err := stageNumber(req.Body.Number)
	if err != nil {
		return questModel{}, err
	}
	journal, err := clean("journal", req.Body.Journal, MaxJournal, true)
	if err != nil {
		return questModel{}, err
	}
	var m questModel
	err = store.InTx(ctx, func(q *dbgen.Queries) error {
		quest, err := q.GetQuest(ctx, req.QuestId)
		if err != nil {
			return app.NotFound(err, "quest", req.QuestId)
		}
		id := xid.New().String()
		rev, err := app.Revise(ctx, q, "quest_stage", id, "journal", journal)
		if err != nil {
			return err
		}
		if _, err := q.CreateQuestStage(ctx, dbgen.CreateQuestStageParams{
			ID: id, QuestID: quest.ID, Number: number, Journal: journal, JournalRevisionID: rev,
			Completes: req.Body.Completes, CreatedBy: auth.KeyIDPtr(ctx),
		}); err != nil {
			return stageTaken(err, number)
		}
		m, err = loadQuest(ctx, q, quest.ID)
		return err
	})
	return m, err
}

func (e CreateStageEndpoint) Build(m questModel) Quest { return questView(m, e.Links) }

func (CreateStageEndpoint) Render(vm Quest) apigen.CreateQuestStageResponseObject {
	return apigen.CreateQuestStage201JSONResponse(questAPI(vm))
}

// UpdateStageEndpoint changes a stage. People only.
type UpdateStageEndpoint struct {
	Links app.Links
}

func (e UpdateStageEndpoint) Interact(ctx context.Context, store TxStore, req apigen.UpdateQuestStageRequestObject) (questModel, error) {
	if _, err := app.Access(ctx, store, "quest_stage", req.StageId); err != nil {
		return questModel{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return questModel{}, err
	}
	arg := dbgen.UpdateQuestStageParams{ID: req.StageId, Completes: req.Body.Completes}
	if n := req.Body.Number; n != nil {
		number, err := stageNumber(*n)
		if err != nil {
			return questModel{}, err
		}
		arg.Number = &number
	}
	var journal string
	if j := req.Body.Journal; j != nil {
		var err error
		if journal, err = clean("journal", *j, MaxJournal, true); err != nil {
			return questModel{}, err
		}
	}
	var m questModel
	err := store.InTx(ctx, func(q *dbgen.Queries) error {
		if err := app.Claim(ctx, q, e.Guard(req)); err != nil {
			return err
		}
		cur, err := q.GetQuestStage(ctx, req.StageId)
		if err != nil {
			return app.NotFound(err, "stage", req.StageId)
		}
		if journal != "" && journal != cur.Journal {
			rev, err := app.Revise(ctx, q, "quest_stage", cur.ID, "journal", journal)
			if err != nil {
				return err
			}
			arg.Journal, arg.JournalRevisionID = &journal, &rev
		}
		if _, err := q.UpdateQuestStage(ctx, arg); err != nil {
			if arg.Number != nil {
				err = stageTaken(err, *arg.Number)
			}
			return err
		}
		m, err = loadQuest(ctx, q, cur.QuestID)
		return err
	})
	return m, err
}

// Guard refuses a change over someone else's newer one.
func (UpdateStageEndpoint) Guard(req apigen.UpdateQuestStageRequestObject) app.Guard {
	return app.Guard{Kind: "quest_stage", ID: req.StageId, Seen: req.Body.UpdatedAt}
}

func (e UpdateStageEndpoint) Build(m questModel) Quest { return questView(m, e.Links) }

func (UpdateStageEndpoint) Render(vm Quest) apigen.UpdateQuestStageResponseObject {
	return apigen.UpdateQuestStage200JSONResponse(questAPI(vm))
}

type DeleteStageStore interface {
	app.AccessStore
	DeleteQuestStage(ctx context.Context, id string) (int64, error)
}

// DeleteStageEndpoint soft-deletes a stage. Scripts that move the quest to
// it stay, and the build reports them. People only.
type DeleteStageEndpoint struct{}

func (e DeleteStageEndpoint) Interact(ctx context.Context, store DeleteStageStore, req apigen.DeleteQuestStageRequestObject) (bool, error) {
	if _, err := app.Access(ctx, store, "quest_stage", req.StageId); err != nil {
		return false, err
	}
	return deleted(ctx, "stage", req.StageId, store.DeleteQuestStage)
}

func (DeleteStageEndpoint) Build(ok bool) bool { return ok }

func (DeleteStageEndpoint) Render(bool) apigen.DeleteQuestStageResponseObject {
	return apigen.DeleteQuestStage204Response{}
}
