package conversations

import (
	"context"

	apigen "github.com/KevinMcHugh/rpg/internal/api/gen"
	"github.com/KevinMcHugh/rpg/internal/app"
	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
)

// Path is the shortest route a player can take from a conversation's start
// to a node: what they read and what they chose on the way. It is what a
// reviewer needs before commenting on a line, since a line only makes sense
// after the ones that lead to it.
type Path struct {
	NodeID         string     `json:"nodeId"`
	ConversationID string     `json:"conversationId"`
	Reachable      bool       `json:"reachable"`
	Steps          []PathStep `json:"steps"`
}

// PathStep is one line on the way, and the choice taken after it (empty on
// the last step).
type PathStep struct {
	NodeID      string `json:"nodeId"`
	SpeakerName string `json:"speakerName"`
	Text        string `json:"text"`
	ChoiceID    string `json:"choiceId"`
	ChoiceText  string `json:"choiceText"`
}

type PathStore interface {
	GraphStore
	GetNode(ctx context.Context, id string) (dbgen.Node, error)
}

// PathEndpoint finds the shortest route to a node.
type PathEndpoint struct{ Store PathStore }

type pathModel struct {
	Target dbgen.Node
	Graph  graphModel
}

func (e PathEndpoint) Interact(ctx context.Context, req apigen.GetNodePathRequestObject) (pathModel, error) {
	n, err := e.Store.GetNode(ctx, req.NodeId)
	if err != nil {
		return pathModel{}, app.NotFound(err, "node", req.NodeId)
	}
	g, err := loadGraph(ctx, e.Store, n.ConversationID)
	return pathModel{Target: n, Graph: g}, err
}

func (PathEndpoint) Build(m pathModel) Path {
	out := Path{NodeID: m.Target.ID, ConversationID: m.Target.ConversationID, Steps: []PathStep{}}
	start := deref(m.Graph.Conversation.StartNodeID)
	nodes := map[string]dbgen.Node{}
	for _, n := range m.Graph.Nodes {
		nodes[n.ID] = n
	}
	if _, ok := nodes[start]; !ok {
		return out
	}
	byNode := map[string][]dbgen.Choice{}
	for _, c := range m.Graph.Choices {
		byNode[c.FromNodeID] = append(byNode[c.FromNodeID], c)
	}

	// Breadth first, remembering the choice that first reached each node.
	via := map[string]dbgen.Choice{}
	seen := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 && !seen[m.Target.ID] {
		id := queue[0]
		queue = queue[1:]
		for _, c := range byNode[id] {
			to := deref(c.ToNodeID)
			if _, live := nodes[to]; !live || seen[to] {
				continue
			}
			seen[to], via[to] = true, c
			queue = append(queue, to)
		}
	}
	if !seen[m.Target.ID] {
		return out
	}

	var rev []PathStep
	for id, choice := m.Target.ID, (dbgen.Choice{}); ; {
		n := nodes[id]
		rev = append(rev, PathStep{
			NodeID:      n.ID,
			SpeakerName: speakerName(m.Graph.World.Speakers, n.SpeakerID),
			Text:        n.Text,
			ChoiceID:    choice.ID,
			ChoiceText:  choice.Text,
		})
		if id == start {
			break
		}
		choice = via[id]
		id = choice.FromNodeID
	}
	for i := len(rev) - 1; i >= 0; i-- {
		out.Steps = append(out.Steps, rev[i])
	}
	out.Reachable = true
	return out
}

func (PathEndpoint) Render(vm Path) apigen.GetNodePathResponseObject {
	steps := make([]apigen.PathStep, len(vm.Steps))
	for i, s := range vm.Steps {
		steps[i] = apigen.PathStep{NodeId: s.NodeID, SpeakerName: s.SpeakerName, Text: s.Text, ChoiceId: s.ChoiceID, ChoiceText: s.ChoiceText}
	}
	return apigen.GetNodePath200JSONResponse{NodeId: vm.NodeID, ConversationId: vm.ConversationID, Reachable: vm.Reachable, Steps: steps}
}
