// Package conversations holds the endpoints for the dialogue graph: a
// conversation, its nodes (lines of dialogue) and the choices between them.
// A node's line, a choice's text and a conversation's label are
// player-facing, so only a person may write them (app.Revise); a
// conversation's title and summary are authoring labels that never ship.
// Conditions and effects are scripts (package script): authors send text,
// it is stored by id, and it comes back formatted with today's keys.
package conversations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/rs/xid"

	apigen "github.com/KevinMcHugh/rpg/internal/api/gen"
	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/auth"
	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
	"github.com/KevinMcHugh/rpg/internal/script"
)

// Limits, in characters.
const (
	MaxTitle   = 200
	MaxSummary = 20000
	MaxLine    = 5000
	MaxChoice  = 500
)

// -----------------------------------------------------------------------------
// View models
// -----------------------------------------------------------------------------

// Conversation is a conversation without its nodes.
type Conversation struct {
	ID          string    `json:"id"`
	GameID      string    `json:"gameId"`
	Key         string    `json:"key"`
	Title       string    `json:"title"`
	Summary     string    `json:"summary"`
	StartNodeID string    `json:"startNodeId"`
	Label       string    `json:"label"`
	Condition   string    `json:"condition"`
	Repeatable  bool      `json:"repeatable"`
	Opening     bool      `json:"opening"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	URL         string    `json:"url"`
}

// Node is a line of dialogue with the choices it offers.
type Node struct {
	ID             string   `json:"id"`
	ConversationID string   `json:"conversationId"`
	SpeakerID      string   `json:"speakerId"`
	SpeakerName    string   `json:"speakerName"`
	Text           string   `json:"text"`
	Effects        string   `json:"effects"`
	Choices        []Choice `json:"choices"`
	URL            string   `json:"url"`
}

// Choice is something the player can say at a node.
type Choice struct {
	ID              string `json:"id"`
	FromNodeID      string `json:"fromNodeId"`
	ToNodeID        string `json:"toNodeId"`
	Text            string `json:"text"`
	SortOrder       int    `json:"sortOrder"`
	Condition       string `json:"condition"`
	Effects         string `json:"effects"`
	CheckSkillID    string `json:"checkSkillId"`
	CheckSkillName  string `json:"checkSkillName"`
	CheckDifficulty int    `json:"checkDifficulty"`
	FailNodeID      string `json:"failNodeId"`
	CritSuccessID   string `json:"critSuccessNodeId"`
	CritFailID      string `json:"critFailNodeId"`
}

// Graph is a conversation with every node, in the order they were written.
type Graph struct {
	Conversation Conversation `json:"conversation"`
	Nodes        []Node       `json:"nodes"`
}

// ToViewModel is the view of a stored conversation.
func ToViewModel(row dbgen.Conversation, w world, links app.Links) Conversation {
	return Conversation{
		ID:          row.ID,
		GameID:      row.GameID,
		Key:         row.Key,
		Title:       row.Title,
		Summary:     row.Summary,
		StartNodeID: deref(row.StartNodeID),
		Label:       row.Label,
		Condition:   app.FormatCond(row.Condition, w.Names),
		Repeatable:  row.Repeatable,
		Opening:     row.Opening,
		CreatedAt:   row.CreatedAt.Time,
		UpdatedAt:   row.UpdatedAt.Time,
		URL:         links.Conversation(row.ID),
	}
}

func choiceView(row dbgen.Choice, w world) Choice {
	c := Choice{
		ID:              row.ID,
		FromNodeID:      row.FromNodeID,
		ToNodeID:        deref(row.ToNodeID),
		Text:            row.Text,
		SortOrder:       int(row.SortOrder),
		Condition:       app.FormatCond(row.Condition, w.Names),
		Effects:         app.FormatEffects(row.Effects, w.Names),
		CheckSkillID:    deref(row.CheckSkillID),
		CheckDifficulty: int(row.CheckDifficulty),
		FailNodeID:      deref(row.FailNodeID),
		CritSuccessID:   deref(row.CritSuccessNodeID),
		CritFailID:      deref(row.CritFailNodeID),
	}
	if c.CheckSkillID != "" {
		c.CheckSkillName = w.Skills[c.CheckSkillID]
		if c.CheckSkillName == "" {
			c.CheckSkillName = "(deleted skill)"
		}
	}
	return c
}

// world is what views need to show scripts and names: every key, and the
// names of characters (speakers) and skills (checks).
type world struct {
	Names    *script.Names
	Speakers map[string]string // character id → name
	Skills   map[string]string // skill id → name
}

// WorldStore is what loading a game's world reads.
type WorldStore interface {
	app.NamesStore
}

func loadWorld(ctx context.Context, s WorldStore, gameID string) (world, error) {
	names, err := app.LoadNames(ctx, s, gameID)
	if err != nil {
		return world{}, err
	}
	chars, err := s.ListCharacters(ctx, gameID)
	if err != nil {
		return world{}, err
	}
	skills, err := s.ListSkills(ctx, gameID)
	if err != nil {
		return world{}, err
	}
	w := world{Names: names, Speakers: map[string]string{}, Skills: map[string]string{}}
	for _, c := range chars {
		w.Speakers[c.ID] = c.Name
	}
	for _, sk := range skills {
		w.Skills[sk.ID] = sk.Name
	}
	return w, nil
}

// nodeModel is a stored node with what its view needs.
type nodeModel struct {
	Node    dbgen.Node
	Choices []dbgen.Choice
	World   world
}

func nodeView(m nodeModel, links app.Links) Node {
	choices := make([]Choice, len(m.Choices))
	for i, c := range m.Choices {
		choices[i] = choiceView(c, m.World)
	}
	return Node{
		ID:             m.Node.ID,
		ConversationID: m.Node.ConversationID,
		SpeakerID:      deref(m.Node.SpeakerID),
		SpeakerName:    speakerName(m.World.Speakers, m.Node.SpeakerID),
		Text:           m.Node.Text,
		Effects:        app.FormatEffects(m.Node.Effects, m.World.Names),
		Choices:        choices,
		URL:            links.Node(m.Node.ConversationID, m.Node.ID),
	}
}

// graphModel is everything a Graph is built from.
type graphModel struct {
	Conversation dbgen.Conversation
	Nodes        []dbgen.Node
	Choices      []dbgen.Choice
	World        world
}

func graphView(m graphModel, links app.Links) Graph {
	byNode := map[string][]dbgen.Choice{}
	for _, c := range m.Choices {
		byNode[c.FromNodeID] = append(byNode[c.FromNodeID], c)
	}
	nodes := make([]Node, len(m.Nodes))
	for i, n := range m.Nodes {
		nodes[i] = nodeView(nodeModel{Node: n, Choices: byNode[n.ID], World: m.World}, links)
	}
	return Graph{Conversation: ToViewModel(m.Conversation, m.World, links), Nodes: nodes}
}

func speakerName(speakers map[string]string, id *string) string {
	if id == nil {
		return ""
	}
	if name, ok := speakers[*id]; ok {
		return name
	}
	return "(deleted character)"
}

func toAPIConversation(c Conversation) apigen.Conversation {
	return apigen.Conversation{
		Id:          c.ID,
		GameId:      c.GameID,
		Key:         c.Key,
		Title:       c.Title,
		Summary:     c.Summary,
		StartNodeId: c.StartNodeID,
		Label:       c.Label,
		Condition:   c.Condition,
		Repeatable:  c.Repeatable,
		Opening:     c.Opening,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
		Url:         c.URL,
	}
}

func toAPIChoice(c Choice) apigen.Choice {
	return apigen.Choice{
		Id: c.ID, FromNodeId: c.FromNodeID, ToNodeId: c.ToNodeID, Text: c.Text, SortOrder: c.SortOrder,
		Condition: c.Condition, Effects: c.Effects,
		CheckSkillId: c.CheckSkillID, CheckSkillName: c.CheckSkillName, CheckDifficulty: c.CheckDifficulty, FailNodeId: c.FailNodeID,
		CritSuccessNodeId: c.CritSuccessID, CritFailNodeId: c.CritFailID,
	}
}

func toAPINode(n Node) apigen.Node {
	choices := make([]apigen.Choice, len(n.Choices))
	for i, c := range n.Choices {
		choices[i] = toAPIChoice(c)
	}
	return apigen.Node{
		Id:             n.ID,
		ConversationId: n.ConversationID,
		SpeakerId:      n.SpeakerID,
		SpeakerName:    n.SpeakerName,
		Text:           n.Text,
		Effects:        n.Effects,
		Choices:        choices,
		Url:            n.URL,
	}
}

func toAPIGraph(g Graph) apigen.ConversationGraph {
	nodes := make([]apigen.Node, len(g.Nodes))
	for i, n := range g.Nodes {
		nodes[i] = toAPINode(n)
	}
	return apigen.ConversationGraph{Conversation: toAPIConversation(g.Conversation), Nodes: nodes}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// -----------------------------------------------------------------------------
// Validation
// -----------------------------------------------------------------------------

// clean trims s and checks it against limit; a required field may not be
// empty.
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

// -----------------------------------------------------------------------------
// Loading
// -----------------------------------------------------------------------------

// GraphStore is what loading a whole conversation reads.
type GraphStore interface {
	app.AccessStore
	WorldStore
	GetConversation(ctx context.Context, id string) (dbgen.Conversation, error)
	ListNodesByConversation(ctx context.Context, conversationID string) ([]dbgen.Node, error)
	ListChoicesByConversation(ctx context.Context, conversationID string) ([]dbgen.Choice, error)
}

func loadGraph(ctx context.Context, s GraphStore, id string) (graphModel, error) {
	conv, err := s.GetConversation(ctx, id)
	if err != nil {
		return graphModel{}, app.NotFound(err, "conversation", id)
	}
	nodes, err := s.ListNodesByConversation(ctx, id)
	if err != nil {
		return graphModel{}, err
	}
	choices, err := s.ListChoicesByConversation(ctx, id)
	if err != nil {
		return graphModel{}, err
	}
	w, err := loadWorld(ctx, s, conv.GameID)
	if err != nil {
		return graphModel{}, err
	}
	return graphModel{Conversation: conv, Nodes: nodes, Choices: choices, World: w}, nil
}

// -----------------------------------------------------------------------------
// List conversations
// -----------------------------------------------------------------------------

type ListStore interface {
	WorldStore
	app.GameStore
}

// ListEndpoint is every conversation in a game, newest first, without
// nodes.
type ListEndpoint struct {
	Store ListStore
	Links app.Links
}

type listModel struct {
	Rows  []dbgen.Conversation
	World world
}

func (e ListEndpoint) Interact(ctx context.Context, req apigen.ListConversationsRequestObject) (listModel, error) {
	if _, err := app.Game(ctx, e.Store, req.GameId); err != nil {
		return listModel{}, err
	}
	rows, err := e.Store.ListConversations(ctx, req.GameId)
	if err != nil {
		return listModel{}, err
	}
	w, err := loadWorld(ctx, e.Store, req.GameId)
	return listModel{Rows: rows, World: w}, err
}

func (e ListEndpoint) Build(m listModel) []Conversation {
	out := make([]Conversation, len(m.Rows))
	for i, r := range m.Rows {
		out[i] = ToViewModel(r, m.World, e.Links)
	}
	return out
}

func (e ListEndpoint) Render(vms []Conversation) apigen.ListConversationsResponseObject {
	out := make([]apigen.Conversation, len(vms))
	for i, c := range vms {
		out[i] = toAPIConversation(c)
	}
	return apigen.ListConversations200JSONResponse{Conversations: out}
}

// -----------------------------------------------------------------------------
// Get conversation
// -----------------------------------------------------------------------------

// GetEndpoint is one conversation with every node and choice.
type GetEndpoint struct {
	Store GraphStore
	Links app.Links
}

func (e GetEndpoint) Interact(ctx context.Context, req apigen.GetConversationRequestObject) (graphModel, error) {
	if _, err := app.Access(ctx, e.Store, "conversation", req.ConversationId); err != nil {
		return graphModel{}, err
	}
	return loadGraph(ctx, e.Store, req.ConversationId)
}

func (e GetEndpoint) Build(m graphModel) Graph { return graphView(m, e.Links) }

func (e GetEndpoint) Render(vm Graph) apigen.GetConversationResponseObject {
	return apigen.GetConversation200JSONResponse(toAPIGraph(vm))
}

// -----------------------------------------------------------------------------
// Create conversation
// -----------------------------------------------------------------------------

type CreateStore interface {
	WorldStore
	app.GameStore
	CreateConversation(ctx context.Context, arg dbgen.CreateConversationParams) (dbgen.Conversation, error)
}

// CreateEndpoint saves a new, empty conversation in a game; the game's
// first one opens it. People only: an agent suggests a conversation in a note.
type CreateEndpoint struct {
	Store CreateStore
	Links app.Links
}

func (e CreateEndpoint) Interact(ctx context.Context, req apigen.CreateConversationRequestObject) (graphModel, error) {
	if _, err := app.Access(ctx, e.Store, "game", req.GameId); err != nil {
		return graphModel{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return graphModel{}, err
	}
	title, err := clean("title", req.Body.Title, MaxTitle, true)
	if err != nil {
		return graphModel{}, err
	}
	summary, err := clean("summary", req.Body.Summary, MaxSummary, false)
	if err != nil {
		return graphModel{}, err
	}
	key, err := app.CleanKey(req.Body.Key, title)
	if err != nil {
		return graphModel{}, err
	}
	if _, err := app.Game(ctx, e.Store, req.GameId); err != nil {
		return graphModel{}, err
	}
	row, err := e.Store.CreateConversation(ctx, dbgen.CreateConversationParams{
		ID:        xid.New().String(),
		GameID:    req.GameId,
		Key:       key,
		Title:     title,
		Summary:   summary,
		CreatedBy: auth.KeyIDPtr(ctx),
	})
	if err != nil {
		return graphModel{}, app.KeyTaken(err, "conversation", key)
	}
	w, err := loadWorld(ctx, e.Store, row.GameID)
	return graphModel{Conversation: row, World: w}, err
}

func (e CreateEndpoint) Build(m graphModel) Graph { return graphView(m, e.Links) }

func (e CreateEndpoint) Render(vm Graph) apigen.CreateConversationResponseObject {
	return apigen.CreateConversation201JSONResponse(toAPIGraph(vm))
}

// -----------------------------------------------------------------------------
// Update conversation
// -----------------------------------------------------------------------------

type UpdateStore interface {
	app.AccessStore
	GraphStore
	TxStore
}

// UpdateEndpoint changes a conversation's title, summary, start node, key,
// label, condition, or whether it repeats or opens the game. People only.
type UpdateEndpoint struct {
	Store UpdateStore
	Links app.Links
}

func (e UpdateEndpoint) Interact(ctx context.Context, req apigen.UpdateConversationRequestObject) (graphModel, error) {
	if _, err := app.Access(ctx, e.Store, "conversation", req.ConversationId); err != nil {
		return graphModel{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return graphModel{}, err
	}
	b := req.Body
	arg := dbgen.UpdateConversationParams{ID: req.ConversationId, Repeatable: b.Repeatable, Opening: b.Opening}
	if t := b.Title; t != nil {
		title, err := clean("title", *t, MaxTitle, true)
		if err != nil {
			return graphModel{}, err
		}
		arg.Title = &title
	}
	if s := b.Summary; s != nil {
		summary, err := clean("summary", *s, MaxSummary, false)
		if err != nil {
			return graphModel{}, err
		}
		arg.Summary = &summary
	}
	if k := b.Key; k != nil {
		key, err := app.CleanKey(*k, "")
		if err != nil {
			return graphModel{}, err
		}
		arg.Key = &key
	}
	var label string
	if l := b.Label; l != nil {
		var err error
		if label, err = clean("label", *l, MaxChoice, false); err != nil {
			return graphModel{}, err
		}
	}
	err := e.Store.InTx(ctx, func(q *dbgen.Queries) error {
		cur, err := q.GetConversation(ctx, req.ConversationId)
		if err != nil {
			return app.NotFound(err, "conversation", req.ConversationId)
		}
		if s := b.StartNodeId; s != nil {
			arg.SetStart = true
			if id := strings.TrimSpace(*s); id != "" {
				if err := inConversation(ctx, q, id, cur.ID); err != nil {
					return err
				}
				arg.StartNodeID = &id
			}
		}
		if c := b.Condition; c != nil {
			names, err := app.LoadNames(ctx, q, cur.GameID)
			if err != nil {
				return err
			}
			if arg.Condition, err = app.ParseCond("condition", *c, names); err != nil {
				return err
			}
			arg.SetCondition = true
		}
		// A changed label needs a new revision; an empty one needs none.
		if b.Label != nil && label != cur.Label {
			arg.Label = &label
			if label != "" {
				rev, err := app.Revise(ctx, q, "conversation", cur.ID, "label", label)
				if err != nil {
					return err
				}
				arg.LabelRevisionID = &rev
			}
		}
		if b.Opening != nil && *b.Opening {
			if err := q.ClearOpening(ctx, dbgen.ClearOpeningParams{ID: cur.ID, GameID: cur.GameID}); err != nil {
				return err
			}
		}
		_, err = q.UpdateConversation(ctx, arg)
		if arg.Key != nil {
			err = app.KeyTaken(err, "conversation", *arg.Key)
		}
		return err
	})
	if err != nil {
		return graphModel{}, err
	}
	return loadGraph(ctx, e.Store, req.ConversationId)
}

func (e UpdateEndpoint) Build(m graphModel) Graph { return graphView(m, e.Links) }

func (e UpdateEndpoint) Render(vm Graph) apigen.UpdateConversationResponseObject {
	return apigen.UpdateConversation200JSONResponse(toAPIGraph(vm))
}

// inConversation checks that node id is a live node of conversation
// conversationID. Choices and start nodes stay within one conversation.
func inConversation(ctx context.Context, s interface {
	GetNode(ctx context.Context, id string) (dbgen.Node, error)
}, id, conversationID string) error {
	n, err := s.GetNode(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: no node %s", app.ErrBadInput, id)
		}
		return err
	}
	if n.ConversationID != conversationID {
		return fmt.Errorf("%w: node %s is in another conversation", app.ErrBadInput, id)
	}
	return nil
}

// -----------------------------------------------------------------------------
// Delete conversation
// -----------------------------------------------------------------------------

type DeleteStore interface {
	app.AccessStore
	DeleteConversation(ctx context.Context, id string) (int64, error)
}

// DeleteEndpoint soft-deletes a conversation; its nodes no longer ship.
// People only.
type DeleteEndpoint struct{ Store DeleteStore }

func (e DeleteEndpoint) Interact(ctx context.Context, req apigen.DeleteConversationRequestObject) (bool, error) {
	if _, err := app.Access(ctx, e.Store, "conversation", req.ConversationId); err != nil {
		return false, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return false, err
	}
	n, err := e.Store.DeleteConversation(ctx, req.ConversationId)
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, fmt.Errorf("%w: conversation %s", app.ErrNotFound, req.ConversationId)
	}
	return true, nil
}

func (DeleteEndpoint) Build(ok bool) bool { return ok }

func (DeleteEndpoint) Render(bool) apigen.DeleteConversationResponseObject {
	return apigen.DeleteConversation204Response{}
}
