// Package conversations holds the endpoints for the dialogue graph: a
// conversation, its nodes (lines of dialogue) and the choices between them.
// A node's line and a choice's text are player-facing, so only a person may
// write them (app.Revise); a conversation's title and summary are
// authoring labels that never ship.
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
	Title       string    `json:"title"`
	Summary     string    `json:"summary"`
	StartNodeID string    `json:"startNodeId"`
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
	Choices        []Choice `json:"choices"`
	URL            string   `json:"url"`
}

// Choice is something the player can say at a node.
type Choice struct {
	ID         string `json:"id"`
	FromNodeID string `json:"fromNodeId"`
	ToNodeID   string `json:"toNodeId"`
	Text       string `json:"text"`
	SortOrder  int    `json:"sortOrder"`
}

// Graph is a conversation with every node, in the order they were written.
type Graph struct {
	Conversation Conversation `json:"conversation"`
	Nodes        []Node       `json:"nodes"`
}

// ToViewModel is the view of a stored conversation.
func ToViewModel(row dbgen.Conversation, links app.Links) Conversation {
	return Conversation{
		ID:          row.ID,
		Title:       row.Title,
		Summary:     row.Summary,
		StartNodeID: deref(row.StartNodeID),
		CreatedAt:   row.CreatedAt.Time,
		UpdatedAt:   row.UpdatedAt.Time,
		URL:         links.Conversation(row.ID),
	}
}

func choiceView(row dbgen.Choice) Choice {
	return Choice{
		ID:         row.ID,
		FromNodeID: row.FromNodeID,
		ToNodeID:   deref(row.ToNodeID),
		Text:       row.Text,
		SortOrder:  int(row.SortOrder),
	}
}

// nodeModel is a stored node with what its view needs: its choices and its
// speaker's name.
type nodeModel struct {
	Node        dbgen.Node
	Choices     []dbgen.Choice
	SpeakerName string
}

func nodeView(m nodeModel, links app.Links) Node {
	choices := make([]Choice, len(m.Choices))
	for i, c := range m.Choices {
		choices[i] = choiceView(c)
	}
	return Node{
		ID:             m.Node.ID,
		ConversationID: m.Node.ConversationID,
		SpeakerID:      deref(m.Node.SpeakerID),
		SpeakerName:    m.SpeakerName,
		Text:           m.Node.Text,
		Choices:        choices,
		URL:            links.Node(m.Node.ConversationID, m.Node.ID),
	}
}

// graphModel is everything a Graph is built from.
type graphModel struct {
	Conversation dbgen.Conversation
	Nodes        []dbgen.Node
	Choices      []dbgen.Choice
	Speakers     map[string]string // character id → name
}

func graphView(m graphModel, links app.Links) Graph {
	byNode := map[string][]dbgen.Choice{}
	for _, c := range m.Choices {
		byNode[c.FromNodeID] = append(byNode[c.FromNodeID], c)
	}
	nodes := make([]Node, len(m.Nodes))
	for i, n := range m.Nodes {
		nodes[i] = nodeView(nodeModel{Node: n, Choices: byNode[n.ID], SpeakerName: speakerName(m.Speakers, n.SpeakerID)}, links)
	}
	return Graph{Conversation: ToViewModel(m.Conversation, links), Nodes: nodes}
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
		Title:       c.Title,
		Summary:     c.Summary,
		StartNodeId: c.StartNodeID,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
		Url:         c.URL,
	}
}

func toAPIChoice(c Choice) apigen.Choice {
	return apigen.Choice{Id: c.ID, FromNodeId: c.FromNodeID, ToNodeId: c.ToNodeID, Text: c.Text, SortOrder: c.SortOrder}
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
	GetConversation(ctx context.Context, id string) (dbgen.Conversation, error)
	ListNodesByConversation(ctx context.Context, conversationID string) ([]dbgen.Node, error)
	ListChoicesByConversation(ctx context.Context, conversationID string) ([]dbgen.Choice, error)
	ListCharacters(ctx context.Context) ([]dbgen.Character, error)
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
	speakers, err := speakerNames(ctx, s)
	if err != nil {
		return graphModel{}, err
	}
	return graphModel{Conversation: conv, Nodes: nodes, Choices: choices, Speakers: speakers}, nil
}

func speakerNames(ctx context.Context, s interface {
	ListCharacters(ctx context.Context) ([]dbgen.Character, error)
}) (map[string]string, error) {
	chars, err := s.ListCharacters(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(chars))
	for _, c := range chars {
		out[c.ID] = c.Name
	}
	return out, nil
}

// -----------------------------------------------------------------------------
// List conversations
// -----------------------------------------------------------------------------

type ListStore interface {
	ListConversations(ctx context.Context) ([]dbgen.Conversation, error)
}

// ListEndpoint is every conversation, newest first, without nodes.
type ListEndpoint struct {
	Store ListStore
	Links app.Links
}

func (e ListEndpoint) Interact(ctx context.Context, _ apigen.ListConversationsRequestObject) ([]dbgen.Conversation, error) {
	return e.Store.ListConversations(ctx)
}

func (e ListEndpoint) Build(rows []dbgen.Conversation) []Conversation {
	out := make([]Conversation, len(rows))
	for i, r := range rows {
		out[i] = ToViewModel(r, e.Links)
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
	CreateConversation(ctx context.Context, arg dbgen.CreateConversationParams) (dbgen.Conversation, error)
}

// CreateEndpoint saves a new, empty conversation. People only: an agent
// suggests a conversation in a note.
type CreateEndpoint struct {
	Store CreateStore
	Links app.Links
}

func (e CreateEndpoint) Interact(ctx context.Context, req apigen.CreateConversationRequestObject) (graphModel, error) {
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
	row, err := e.Store.CreateConversation(ctx, dbgen.CreateConversationParams{
		ID:        xid.New().String(),
		Title:     title,
		Summary:   summary,
		CreatedBy: auth.KeyIDPtr(ctx),
	})
	return graphModel{Conversation: row}, err
}

func (e CreateEndpoint) Build(m graphModel) Graph { return graphView(m, e.Links) }

func (e CreateEndpoint) Render(vm Graph) apigen.CreateConversationResponseObject {
	return apigen.CreateConversation201JSONResponse(toAPIGraph(vm))
}

// -----------------------------------------------------------------------------
// Update conversation
// -----------------------------------------------------------------------------

type UpdateStore interface {
	GraphStore
	GetNode(ctx context.Context, id string) (dbgen.Node, error)
	UpdateConversation(ctx context.Context, arg dbgen.UpdateConversationParams) (dbgen.Conversation, error)
}

// UpdateEndpoint changes a conversation's title, summary or start node.
// People only.
type UpdateEndpoint struct {
	Store UpdateStore
	Links app.Links
}

func (e UpdateEndpoint) Interact(ctx context.Context, req apigen.UpdateConversationRequestObject) (graphModel, error) {
	if err := app.RequireHuman(ctx); err != nil {
		return graphModel{}, err
	}
	arg := dbgen.UpdateConversationParams{ID: req.ConversationId}
	if t := req.Body.Title; t != nil {
		title, err := clean("title", *t, MaxTitle, true)
		if err != nil {
			return graphModel{}, err
		}
		arg.Title = &title
	}
	if s := req.Body.Summary; s != nil {
		summary, err := clean("summary", *s, MaxSummary, false)
		if err != nil {
			return graphModel{}, err
		}
		arg.Summary = &summary
	}
	if s := req.Body.StartNodeId; s != nil {
		arg.SetStart = true
		if id := strings.TrimSpace(*s); id != "" {
			if err := inConversation(ctx, e.Store, id, req.ConversationId); err != nil {
				return graphModel{}, err
			}
			arg.StartNodeID = &id
		}
	}
	if _, err := e.Store.UpdateConversation(ctx, arg); err != nil {
		return graphModel{}, app.NotFound(err, "conversation", req.ConversationId)
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
	DeleteConversation(ctx context.Context, id string) (int64, error)
}

// DeleteEndpoint soft-deletes a conversation; its nodes no longer ship.
// People only.
type DeleteEndpoint struct{ Store DeleteStore }

func (e DeleteEndpoint) Interact(ctx context.Context, req apigen.DeleteConversationRequestObject) (bool, error) {
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
