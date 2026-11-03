// Package game plays the bundle the build writes (content.Bundle). It is
// pure: a State goes in, a choice is made, a State comes out, so the same
// engine runs the authoring tool's playtests and the game itself, and tests
// need no database. Skill checks roll dice from the State's seed, so a
// saved game replays the same way.
//
// The game opens with the opening conversation. When a conversation ends,
// the player is between conversations and picks the next from those whose
// label is set, whose condition holds, and which they have not finished
// (unless it is repeatable). When none is left, the game is over.
package game

import (
	"errors"
	"fmt"
	"math/rand/v2"

	"github.com/KevinMcHugh/rpg/internal/content"
	"github.com/KevinMcHugh/rpg/internal/script"
)

// MaxLog bounds the transcript a State keeps.
const MaxLog = 400

// ErrStale is a State that refers to content that has since changed: a
// node or choice that no longer exists.
var ErrStale = errors.New("this game refers to content that has changed")

// ErrUnavailable is a choice or conversation the player cannot pick now.
var ErrUnavailable = errors.New("that is not available")

// State is a game in progress. It is what a save stores.
type State struct {
	Conversation string          `json:"conversation,omitempty"` // empty between conversations
	Node         string          `json:"node,omitempty"`
	Flags        map[string]int  `json:"flags"`
	Skills       map[string]int  `json:"skills"`
	Approval     map[string]int  `json:"approval"`
	Quests       map[string]int  `json:"quests"` // quest id → stage number; absent is 0, not started
	Done         map[string]bool `json:"done"`   // conversations finished at least once
	Seed         uint64          `json:"seed"`
	Rolls        uint64          `json:"rolls"`
	Log          []Entry         `json:"log"`
}

// Entry is one line of the transcript.
type Entry struct {
	// Kind is what happened: "line" (someone spoke), "choice" (the player
	// spoke), "check" (a skill check), "journal" (a quest moved on),
	// "approval" (a character's approval changed), "skill" (the player's
	// skill changed), or "end" (a conversation ended).
	Kind    string `json:"kind"`
	Speaker string `json:"speaker,omitempty"` // a line's speaker, a check's skill, whose approval changed
	Text    string `json:"text,omitempty"`    // a line, a choice, a quest's name
	Detail  string `json:"detail,omitempty"`  // a journal entry
	Delta   int    `json:"delta,omitempty"`   // an approval or skill change
	Done    bool   `json:"done,omitempty"`    // a journal entry that completes its quest
	Dice    []int  `json:"dice,omitempty"`    // a check's two dice
	Bonus   int    `json:"bonus,omitempty"`   // a check's skill level
	Target  int    `json:"target,omitempty"`  // a check's difficulty
	Success bool   `json:"success,omitempty"` // a check's outcome
}

// Game is a bundle, indexed for play.
type Game struct {
	b      content.Bundle
	convs  map[string]*content.Conversation
	nodes  map[string]*content.Node
	inConv map[string]string // node id → conversation id
	chars  map[string]content.Character
	skills map[string]content.Skill
	quests map[string]content.Quest
}

// New indexes b for play.
func New(b content.Bundle) *Game {
	g := &Game{
		b:      b,
		convs:  map[string]*content.Conversation{},
		nodes:  map[string]*content.Node{},
		inConv: map[string]string{},
		chars:  map[string]content.Character{},
		skills: map[string]content.Skill{},
		quests: map[string]content.Quest{},
	}
	for i := range b.Conversations {
		c := &b.Conversations[i]
		g.convs[c.ID] = c
		for j := range c.Nodes {
			g.nodes[c.Nodes[j].ID] = &c.Nodes[j]
			g.inConv[c.Nodes[j].ID] = c.ID
		}
	}
	for _, c := range b.Characters {
		g.chars[c.ID] = c
	}
	for _, s := range b.Skills {
		g.skills[s.ID] = s
	}
	for _, q := range b.Quests {
		g.quests[q.ID] = q
	}
	return g
}

// Bundle is what the game plays.
func (g *Game) Bundle() content.Bundle { return g.b }

// NewState starts a game: the world at its initial values, rolling dice
// from seed, in the opening conversation if there is one.
func (g *Game) NewState(seed uint64) *State {
	s := g.Blank(seed)
	g.Open(s)
	return s
}

// Blank is the world at its initial values, between conversations, before
// anything has happened. A playtest sets it up and then starts it.
func (g *Game) Blank(seed uint64) *State {
	s := &State{
		Flags: map[string]int{}, Skills: map[string]int{}, Approval: map[string]int{},
		Quests: map[string]int{}, Done: map[string]bool{}, Seed: seed, Log: []Entry{},
	}
	for _, f := range g.b.Flags {
		if f.Initial != 0 {
			s.Flags[f.ID] = f.Initial
		}
	}
	for _, sk := range g.b.Skills {
		s.Skills[sk.ID] = sk.Initial
	}
	return s
}

// Open starts the opening conversation, if there is one.
func (g *Game) Open(s *State) {
	if c, ok := g.convs[g.b.Opening]; ok {
		_ = g.enter(s, c.ID, c.Start)
	}
}

// ---- the world a script sees ------------------------------------------------

type world struct {
	g *Game
	s *State
}

func (w world) Flag(id string) int            { return w.s.Flags[id] }
func (w world) Skill(id string) int           { return w.s.Skills[id] }
func (w world) Approval(id string) int        { return w.s.Approval[id] }
func (w world) Quest(id string) int           { return w.s.Quests[id] }
func (w world) Done(conversation string) bool { return w.s.Done[conversation] }

func (w world) QuestDone(id string) bool {
	at := w.s.Quests[id]
	for _, st := range w.g.quests[id].Stages {
		if st.Number == at {
			return st.Completes
		}
	}
	return false
}

func (g *Game) holds(s *State, c *script.Cond) bool {
	return c == nil || c.Eval(world{g, s})
}

// ---- between conversations --------------------------------------------------

// Start is a conversation the player can pick between conversations.
type Start struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Available is what the player can start now: nothing while a
// conversation is under way.
func (g *Game) Available(s *State) []Start {
	if s.Conversation != "" {
		return nil
	}
	var out []Start
	for _, c := range g.b.Conversations {
		if c.Label != "" && (c.Repeatable || !s.Done[c.ID]) && g.holds(s, c.Condition) {
			out = append(out, Start{ID: c.ID, Label: c.Label})
		}
	}
	return out
}

// Over reports whether the game has ended: between conversations, with
// none left to start.
func (g *Game) Over(s *State) bool {
	return s.Conversation == "" && len(g.Available(s)) == 0
}

// Begin starts a conversation the player picked.
func (g *Game) Begin(s *State, conversationID string) error {
	for _, a := range g.Available(s) {
		if a.ID == conversationID {
			c := g.convs[conversationID]
			return g.enter(s, c.ID, c.Start)
		}
	}
	return ErrUnavailable
}

// StartAt jumps to a node, for a playtest that begins mid-conversation.
func (g *Game) StartAt(s *State, nodeID string) error {
	conv, ok := g.inConv[nodeID]
	if !ok {
		return ErrStale
	}
	return g.enter(s, conv, nodeID)
}

// Recover takes a State whose conversation no longer exists back to
// between conversations.
func (g *Game) Recover(s *State) {
	s.Conversation, s.Node = "", ""
	s.log(Entry{Kind: "end"})
}

// ---- in a conversation ------------------------------------------------------

// Option is a choice the player can make now.
type Option struct {
	ID    string       `json:"id"`
	Text  string       `json:"text"`
	Check *CheckOption `json:"check,omitempty"`
}

// CheckOption is what the player sees of a skill check before rolling.
type CheckOption struct {
	Skill      string `json:"skill"`
	Level      int    `json:"level"`
	Difficulty int    `json:"difficulty"`
	Chance     int    `json:"chance"` // percent
}

// Speaker is who says the current line: a character's name, or "" for the
// narrator.
func (g *Game) Speaker(id string) string { return g.chars[id].Name }

// Options is the choices the player can make at the current node: those
// whose condition holds.
func (g *Game) Options(s *State) []Option {
	n, ok := g.nodes[s.Node]
	if s.Conversation == "" || !ok {
		return nil
	}
	var out []Option
	for _, c := range n.Choices {
		if !g.holds(s, c.Condition) {
			continue
		}
		o := Option{ID: c.ID, Text: c.Text}
		if c.Check != nil {
			level := s.Skills[c.Check.Skill]
			o.Check = &CheckOption{Skill: g.skills[c.Check.Skill].Name, Level: level, Difficulty: c.Check.Difficulty, Chance: Chance(level, c.Check.Difficulty)}
		}
		out = append(out, o)
	}
	return out
}

// Choose makes a choice at the current node.
func (g *Game) Choose(s *State, choiceID string) error {
	n, ok := g.nodes[s.Node]
	if s.Conversation == "" || !ok {
		return ErrStale
	}
	var choice *content.Choice
	for i := range n.Choices {
		if n.Choices[i].ID == choiceID && g.holds(s, n.Choices[i].Condition) {
			choice = &n.Choices[i]
		}
	}
	if choice == nil {
		return ErrUnavailable
	}
	s.log(Entry{Kind: "choice", Text: choice.Text})
	g.Apply(s, choice.Effects)
	to := choice.To
	if ck := choice.Check; ck != nil {
		dice := s.roll()
		level := s.Skills[ck.Skill]
		success := dice[0]+dice[1]+level >= ck.Difficulty
		s.log(Entry{Kind: "check", Speaker: g.skills[ck.Skill].Name, Dice: dice[:], Bonus: level, Target: ck.Difficulty, Success: success})
		if !success {
			to = ck.Fail
		}
	}
	return g.enter(s, s.Conversation, to)
}

// enter moves to a node, speaking its line and running its effects. An
// empty node, or one with no choice the player can make, ends the
// conversation.
func (g *Game) enter(s *State, conversation, nodeID string) error {
	if nodeID == "" {
		g.end(s, conversation)
		return nil
	}
	n, ok := g.nodes[nodeID]
	if !ok {
		g.end(s, conversation)
		return ErrStale
	}
	s.Conversation, s.Node = conversation, nodeID
	s.log(Entry{Kind: "line", Speaker: g.Speaker(n.Speaker), Text: n.Text})
	g.Apply(s, n.Effects)
	if len(g.Options(s)) == 0 {
		g.end(s, conversation)
	}
	return nil
}

func (g *Game) end(s *State, conversation string) {
	if conversation != "" {
		s.Done[conversation] = true
	}
	s.Conversation, s.Node = "", ""
	s.log(Entry{Kind: "end"})
}

// Apply runs effects. Playtests use it to set up a world before starting.
func (g *Game) Apply(s *State, effects []script.Effect) {
	for _, e := range effects {
		switch {
		case e.Flag != "":
			s.Flags[e.Flag] = e.Apply(s.Flags[e.Flag])
		case e.Skill != "":
			before := s.Skills[e.Skill]
			s.Skills[e.Skill] = e.Apply(before)
			if d := s.Skills[e.Skill] - before; d != 0 {
				s.log(Entry{Kind: "skill", Speaker: g.skills[e.Skill].Name, Delta: d})
			}
		case e.Approval != "":
			before := s.Approval[e.Approval]
			s.Approval[e.Approval] = e.Apply(before)
			if d := s.Approval[e.Approval] - before; d != 0 {
				s.log(Entry{Kind: "approval", Speaker: g.chars[e.Approval].Name, Delta: d})
			}
		case e.Quest != "":
			if s.Quests[e.Quest] == e.Value {
				continue
			}
			s.Quests[e.Quest] = e.Value
			q := g.quests[e.Quest]
			for _, st := range q.Stages {
				if st.Number == e.Value {
					s.log(Entry{Kind: "journal", Text: q.Name, Detail: st.Journal, Done: st.Completes})
				}
			}
		}
	}
}

// roll is two six-sided dice, the next from the State's seed.
func (s *State) roll() [2]int {
	r := rand.New(rand.NewPCG(s.Seed, s.Rolls))
	s.Rolls++
	return [2]int{r.IntN(6) + 1, r.IntN(6) + 1}
}

func (s *State) log(e Entry) {
	s.Log = append(s.Log, e)
	if len(s.Log) > MaxLog {
		s.Log = append([]Entry(nil), s.Log[len(s.Log)-MaxLog:]...)
	}
}

// Chance is the percent chance that 2d6 plus level meets difficulty.
func Chance(level, difficulty int) int {
	hits := 0
	for a := 1; a <= 6; a++ {
		for b := 1; b <= 6; b++ {
			if a+b+level >= difficulty {
				hits++
			}
		}
	}
	return hits * 100 / 36
}

// ---- the journal ------------------------------------------------------------

// QuestEntry is a started quest as the journal shows it.
type QuestEntry struct {
	Name    string `json:"name"`
	Journal string `json:"journal"`
	Done    bool   `json:"done"`
}

// Journal is every started quest, unfinished first.
func (g *Game) Journal(s *State) []QuestEntry {
	var open, done []QuestEntry
	for _, q := range g.b.Quests {
		at := s.Quests[q.ID]
		for _, st := range q.Stages {
			if st.Number == at {
				e := QuestEntry{Name: q.Name, Journal: st.Journal, Done: st.Completes}
				if e.Done {
					done = append(done, e)
				} else {
					open = append(open, e)
				}
			}
		}
	}
	return append(open, done...)
}

// Stat is a name and a number: a companion's approval, or a skill's level.
type Stat struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

// Party is every companion and their approval of the player.
func (g *Game) Party(s *State) []Stat {
	var out []Stat
	for _, c := range g.b.Characters {
		if c.Companion {
			out = append(out, Stat{Name: c.Name, Value: s.Approval[c.ID]})
		}
	}
	return out
}

// Abilities is the player's skills and levels.
func (g *Game) Abilities(s *State) []Stat {
	out := make([]Stat, 0, len(g.b.Skills))
	for _, sk := range g.b.Skills {
		out = append(out, Stat{Name: sk.Name, Value: s.Skills[sk.ID]})
	}
	return out
}

// Describe is a short account of a check for logs and tests.
func (e Entry) Describe() string {
	if e.Kind != "check" || len(e.Dice) != 2 {
		return e.Text
	}
	outcome := "failure"
	if e.Success {
		outcome = "success"
	}
	return fmt.Sprintf("%s %d+%d+%d vs %d: %s", e.Speaker, e.Dice[0], e.Dice[1], e.Bonus, e.Target, outcome)
}
