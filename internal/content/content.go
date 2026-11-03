// Package content compiles the authored content into the bundle the game
// runs on, and checks it on the way. The authoring database tolerates work
// in progress (a choice pointing at a deleted node, a conversation with no
// start); the bundle must not. Compile is pure, so the checks are tested
// without a database; Load reads what it needs.
//
// The check that matters most is provenance: every player-facing string
// must match its latest text revision, and that revision must have been
// written by a human key. See docs/authorship.md.
package content

import (
	"context"
	"fmt"
	"strings"

	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
)

// Content is everything the build reads: every live row, and the revisions
// the rows point at.
type Content struct {
	Characters    []dbgen.Character
	Conversations []dbgen.Conversation
	Nodes         []dbgen.Node
	Choices       []dbgen.Choice
	Revisions     map[string]dbgen.ListTextRevisionsByIDsRow
	OpenNotes     int
}

// Issue is one problem the build found, about one thing.
type Issue struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	TargetKind string `json:"targetKind"`
	TargetID   string `json:"targetId"`
}

// Stats counts what would ship.
type Stats struct {
	Characters    int `json:"characters"`
	Conversations int `json:"conversations"`
	Nodes         int `json:"nodes"`
	Choices       int `json:"choices"`
	Words         int `json:"words"`
	OpenNotes     int `json:"openNotes"`
}

// Report is the outcome of a build. Errors stop it; warnings do not.
type Report struct {
	Errors   []Issue `json:"errors"`
	Warnings []Issue `json:"warnings"`
	Stats    Stats   `json:"stats"`
}

// OK reports whether the content would build.
func (r Report) OK() bool { return len(r.Errors) == 0 }

// Issue codes.
const (
	CodeProvenance     = "provenance"      // text no human wrote, or that differs from its revision
	CodeNoStart        = "no_start"        // a conversation with no start node
	CodeBadStart       = "bad_start"       // a start node that was deleted or is elsewhere
	CodeMissingSpeaker = "missing_speaker" // a line whose speaker was deleted
	CodeDanglingChoice = "dangling_choice" // a choice leading to a deleted node or another conversation
	CodeUnreachable    = "unreachable"     // a node no route from the start reaches
)

// Bundle is what the game runs on: player-facing text and the shape of the
// graph, and nothing authoring-only (no bibles, titles, summaries or
// notes).
type Bundle struct {
	Characters    []Character    `json:"characters"`
	Conversations []Conversation `json:"conversations"`
}

type Character struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Conversation struct {
	ID    string `json:"id"`
	Start string `json:"start"`
	Nodes []Node `json:"nodes"`
}

type Node struct {
	ID      string   `json:"id"`
	Speaker string   `json:"speaker,omitempty"` // a character id; empty for the narrator
	Text    string   `json:"text"`
	Choices []Choice `json:"choices"`
}

type Choice struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	To   string `json:"to,omitempty"` // empty ends the conversation
}

// Compile checks c and builds its bundle. The bundle is only fit to ship
// when the report is OK.
func Compile(c Content) (Bundle, Report) {
	r := Report{Errors: []Issue{}, Warnings: []Issue{}}
	errorf := func(code, kind, id, format string, args ...any) {
		r.Errors = append(r.Errors, Issue{Code: code, Message: fmt.Sprintf(format, args...), TargetKind: kind, TargetID: id})
	}
	warnf := func(code, kind, id, format string, args ...any) {
		r.Warnings = append(r.Warnings, Issue{Code: code, Message: fmt.Sprintf(format, args...), TargetKind: kind, TargetID: id})
	}

	// attest checks one player-facing string against the revision its row
	// points at.
	attest := func(kind, id, field, text, revID string) {
		rev, ok := c.Revisions[revID]
		switch {
		case !ok:
			errorf(CodeProvenance, kind, id, "%s %s's %s has no text revision, so nothing records who wrote it", kind, id, field)
		case rev.TargetKind != kind || rev.TargetID != id || rev.Field != field:
			errorf(CodeProvenance, kind, id, "%s %s points at a revision of %s %s's %s", kind, id, rev.TargetKind, rev.TargetID, rev.Field)
		case rev.AuthorKind != "human":
			errorf(CodeProvenance, kind, id, "%s %s's %s was written by %s, which is not a human key", kind, id, field, rev.AuthorName)
		case rev.Body != text:
			errorf(CodeProvenance, kind, id, "%s %s's %s differs from its latest revision: it was changed without one", kind, id, field)
		}
	}

	var b Bundle
	speakers := map[string]bool{}
	for _, ch := range c.Characters {
		attest("character", ch.ID, "name", ch.Name, ch.NameRevisionID)
		speakers[ch.ID] = true
		b.Characters = append(b.Characters, Character{ID: ch.ID, Name: ch.Name})
	}

	nodesByConv := map[string][]dbgen.Node{}
	nodeConv := map[string]string{}
	for _, n := range c.Nodes {
		nodesByConv[n.ConversationID] = append(nodesByConv[n.ConversationID], n)
		nodeConv[n.ID] = n.ConversationID
	}
	choicesByNode := map[string][]dbgen.Choice{}
	for _, ch := range c.Choices {
		choicesByNode[ch.FromNodeID] = append(choicesByNode[ch.FromNodeID], ch)
	}

	for _, conv := range c.Conversations {
		out := Conversation{ID: conv.ID, Nodes: []Node{}}
		start := deref(conv.StartNodeID)
		switch {
		case start == "":
			errorf(CodeNoStart, "conversation", conv.ID, "conversation %q has no start node", conv.Title)
		case nodeConv[start] != conv.ID:
			errorf(CodeBadStart, "conversation", conv.ID, "conversation %q starts at node %s, which was deleted or is in another conversation", conv.Title, start)
		default:
			out.Start = start
		}

		for _, n := range nodesByConv[conv.ID] {
			attest("node", n.ID, "text", n.Text, n.TextRevisionID)
			if id := deref(n.SpeakerID); id != "" && !speakers[id] {
				errorf(CodeMissingSpeaker, "node", n.ID, "node %s is spoken by character %s, who was deleted", n.ID, id)
			}
			r.Stats.Words += len(strings.Fields(n.Text))
			node := Node{ID: n.ID, Speaker: deref(n.SpeakerID), Text: n.Text, Choices: []Choice{}}
			for _, ch := range choicesByNode[n.ID] {
				attest("choice", ch.ID, "text", ch.Text, ch.TextRevisionID)
				if to := deref(ch.ToNodeID); to != "" && nodeConv[to] != conv.ID {
					errorf(CodeDanglingChoice, "choice", ch.ID, "choice %q leads to node %s, which was deleted or is in another conversation", ch.Text, to)
				}
				r.Stats.Words += len(strings.Fields(ch.Text))
				r.Stats.Choices++
				node.Choices = append(node.Choices, Choice{ID: ch.ID, Text: ch.Text, To: deref(ch.ToNodeID)})
			}
			out.Nodes = append(out.Nodes, node)
			r.Stats.Nodes++
		}

		if out.Start != "" {
			reached := reachable(out)
			for _, n := range out.Nodes {
				if !reached[n.ID] {
					warnf(CodeUnreachable, "node", n.ID, "node %s in %q cannot be reached from the start", n.ID, conv.Title)
				}
			}
		}
		b.Conversations = append(b.Conversations, out)
	}

	r.Stats.Characters = len(b.Characters)
	r.Stats.Conversations = len(b.Conversations)
	r.Stats.OpenNotes = c.OpenNotes
	return b, r
}

// reachable is the set of nodes some route from the start reaches.
func reachable(conv Conversation) map[string]bool {
	byID := map[string]Node{}
	for _, n := range conv.Nodes {
		byID[n.ID] = n
	}
	seen := map[string]bool{conv.Start: true}
	stack := []string{conv.Start}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, ch := range byID[id].Choices {
			if _, ok := byID[ch.To]; ok && !seen[ch.To] {
				seen[ch.To] = true
				stack = append(stack, ch.To)
			}
		}
	}
	return seen
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// LoadStore is what Load reads.
type LoadStore interface {
	ListCharacters(ctx context.Context) ([]dbgen.Character, error)
	ListConversations(ctx context.Context) ([]dbgen.Conversation, error)
	ListAllNodes(ctx context.Context) ([]dbgen.Node, error)
	ListAllChoices(ctx context.Context) ([]dbgen.Choice, error)
	ListTextRevisionsByIDs(ctx context.Context, ids []string) ([]dbgen.ListTextRevisionsByIDsRow, error)
	CountOpenNotes(ctx context.Context) (int64, error)
}

// Load reads everything Compile needs.
func Load(ctx context.Context, s LoadStore) (Content, error) {
	var c Content
	var err error
	if c.Characters, err = s.ListCharacters(ctx); err != nil {
		return Content{}, err
	}
	if c.Conversations, err = s.ListConversations(ctx); err != nil {
		return Content{}, err
	}
	if c.Nodes, err = s.ListAllNodes(ctx); err != nil {
		return Content{}, err
	}
	if c.Choices, err = s.ListAllChoices(ctx); err != nil {
		return Content{}, err
	}
	var ids []string
	for _, ch := range c.Characters {
		ids = append(ids, ch.NameRevisionID)
	}
	for _, n := range c.Nodes {
		ids = append(ids, n.TextRevisionID)
	}
	for _, ch := range c.Choices {
		ids = append(ids, ch.TextRevisionID)
	}
	revs, err := s.ListTextRevisionsByIDs(ctx, ids)
	if err != nil {
		return Content{}, err
	}
	c.Revisions = make(map[string]dbgen.ListTextRevisionsByIDsRow, len(revs))
	for _, r := range revs {
		c.Revisions[r.ID] = r
	}
	open, err := s.CountOpenNotes(ctx)
	if err != nil {
		return Content{}, err
	}
	c.OpenNotes = int(open)
	return c, nil
}
