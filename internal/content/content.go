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
	"maps"
	"slices"
	"strings"

	"github.com/KevinMcHugh/rpg/internal/app"
	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
	"github.com/KevinMcHugh/rpg/internal/rules"
	"github.com/KevinMcHugh/rpg/internal/script"
)

// Content is everything the build reads for one game: its every live row,
// and the revisions the rows point at.
type Content struct {
	Game          dbgen.Game
	Characters    []dbgen.Character
	Conversations []dbgen.Conversation
	Nodes         []dbgen.Node
	Choices       []dbgen.Choice
	Flags         []dbgen.Flag
	Skills        []dbgen.Skill
	Quests        []dbgen.Quest
	Stages        []dbgen.QuestStage
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
	Flags         int `json:"flags"`
	Skills        int `json:"skills"`
	Quests        int `json:"quests"`
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
	CodeBadReference   = "bad_reference"   // a script naming something deleted, or a quest stage that does not exist
	CodeBadCheck       = "bad_check"       // a skill check on a deleted skill, or failing to a node elsewhere
	CodeNoOpening      = "no_opening"      // conversations, but none opens the game
	CodeNeverSet       = "flag_never_set"  // a flag read, starting at 0, that nothing sets: the check is always false
	CodeNeverRead      = "flag_never_read" // a flag set that nothing reads
	CodeNoStages       = "no_stages"       // a quest with no stages
	CodeOrphan         = "orphan"          // a conversation with no label that does not open the game: nothing starts it
	CodeRuleset        = "ruleset"         // a game on a ruleset there is no such ruleset
	CodeCritUnused     = "crit_unused"     // a critical branch under a ruleset with no critical degrees
	CodeNoAbility      = "no_ability"      // a skill checked under a d20 ruleset that names no ability
)

// Bundle is what the game runs on: player-facing text and the shape of the
// graph, and nothing authoring-only (no bibles, titles, summaries or
// notes).
type Bundle struct {
	Ruleset       string         `json:"ruleset"` // how checks resolve (package rules)
	Opening       string         `json:"opening"` // the conversation the game opens with
	Characters    []Character    `json:"characters"`
	Conversations []Conversation `json:"conversations"`
	Flags         []Flag         `json:"flags"`
	Skills        []Skill        `json:"skills"`
	Quests        []Quest        `json:"quests"`
	Credits       []Credit       `json:"credits"` // notices for the licensed text it ships
}

type Character struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Companion bool   `json:"companion,omitempty"`
}

type Conversation struct {
	ID         string       `json:"id"`
	Label      string       `json:"label,omitempty"` // what the player picks to start it; empty when nothing lists it
	Condition  *script.Cond `json:"condition,omitempty"`
	Repeatable bool         `json:"repeatable,omitempty"`
	Start      string       `json:"start"`
	Nodes      []Node       `json:"nodes"`
}

type Node struct {
	ID      string          `json:"id"`
	Speaker string          `json:"speaker,omitempty"` // a character id; empty for the narrator
	Text    string          `json:"text"`
	Effects []script.Effect `json:"effects,omitempty"`
	Choices []Choice        `json:"choices"`
}

type Choice struct {
	ID        string          `json:"id"`
	Text      string          `json:"text"`
	To        string          `json:"to,omitempty"` // empty ends the conversation
	Condition *script.Cond    `json:"condition,omitempty"`
	Effects   []script.Effect `json:"effects,omitempty"`
	Check     *Check          `json:"check,omitempty"`
}

// Check is a skill check, resolved by the game's ruleset (package rules).
// Success goes to the choice's To and failure to Fail (empty ends the
// conversation). Under a ruleset with critical degrees (pf2e), a critical
// success goes to CritSuccess and a critical failure to CritFail, each
// falling back to the ordinary destination when empty.
type Check struct {
	Skill       string `json:"skill"`
	Difficulty  int    `json:"difficulty"`
	Fail        string `json:"fail,omitempty"`
	CritSuccess string `json:"critSuccess,omitempty"`
	CritFail    string `json:"critFail,omitempty"`
}

// Flag is a number in the world state; 0 is false.
type Flag struct {
	ID      string `json:"id"`
	Initial int    `json:"initial,omitempty"`
}

type Skill struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Initial int    `json:"initial,omitempty"`
	Ability string `json:"ability,omitempty"` // the ability a d20 ruleset adds
}

// Credit is a license notice the game must show, for licensed text it
// ships.
type Credit struct {
	Source string `json:"source"`
	Notice string `json:"notice"`
}

type Quest struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Stages []Stage `json:"stages"`
}

type Stage struct {
	Number    int    `json:"number"`
	Journal   string `json:"journal"`
	Completes bool   `json:"completes,omitempty"`
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
	// points at. Text a human key wrote is fine; so is licensed text,
	// whose notice the bundle then carries.
	credits := map[string]string{} // source → notice
	attest := func(kind, id, field, text, revID string) {
		rev, ok := c.Revisions[revID]
		switch {
		case !ok:
			errorf(CodeProvenance, kind, id, "%s %s's %s has no text revision, so nothing records who wrote it", kind, id, field)
		case rev.TargetKind != kind || rev.TargetID != id || rev.Field != field:
			errorf(CodeProvenance, kind, id, "%s %s points at a revision of %s %s's %s", kind, id, rev.TargetKind, rev.TargetID, rev.Field)
		case rev.Source != "":
			if rev.Body != text {
				errorf(CodeProvenance, kind, id, "%s %s's %s differs from the licensed text it was imported as", kind, id, field)
			}
			credits[rev.Source] = rev.LicenseNotice
		case rev.AuthorKind != "human":
			errorf(CodeProvenance, kind, id, "%s %s's %s was written by %s, which is not a human key", kind, id, field, rev.AuthorName)
		case rev.Body != text:
			errorf(CodeProvenance, kind, id, "%s %s's %s differs from its latest revision: it was changed without one", kind, id, field)
		}
	}
	ruleset := c.Game.Ruleset
	if ruleset == "" {
		ruleset = "classic"
	}
	rs, err := rules.For(ruleset)
	if err != nil {
		errorf(CodeRuleset, "game", "", "%v", err)
		rs, _ = rules.For("classic")
	}
	crits := len(rs.Degrees()) > 2
	d20 := rs.ID() != "classic"

	b := Bundle{Ruleset: ruleset, Characters: []Character{}, Conversations: []Conversation{}, Flags: []Flag{}, Skills: []Skill{}, Quests: []Quest{}, Credits: []Credit{}}
	speakers := map[string]bool{}
	for _, ch := range c.Characters {
		attest("character", ch.ID, "name", ch.Name, ch.NameRevisionID)
		speakers[ch.ID] = true
		b.Characters = append(b.Characters, Character{ID: ch.ID, Name: ch.Name, Companion: ch.Companion})
	}

	// The world: what scripts may name.
	live := map[string]map[string]bool{
		script.KindFlag: {}, script.KindSkill: {}, script.KindCharacter: speakers,
		script.KindQuest: {}, script.KindConversation: {},
	}
	flagInitial := map[string]int{}
	for _, f := range c.Flags {
		live[script.KindFlag][f.ID] = true
		flagInitial[f.ID] = int(f.InitialValue)
		b.Flags = append(b.Flags, Flag{ID: f.ID, Initial: int(f.InitialValue)})
	}
	skillAbility := map[string]string{}
	for _, sk := range c.Skills {
		attest("skill", sk.ID, "name", sk.Name, sk.NameRevisionID)
		live[script.KindSkill][sk.ID] = true
		skillAbility[sk.ID] = deref(sk.Ability)
		b.Skills = append(b.Skills, Skill{ID: sk.ID, Name: sk.Name, Initial: int(sk.InitialLevel), Ability: deref(sk.Ability)})
	}
	noAbility := map[string]bool{} // skills already warned about
	stagesByQuest := map[string][]dbgen.QuestStage{}
	for _, st := range c.Stages {
		stagesByQuest[st.QuestID] = append(stagesByQuest[st.QuestID], st)
	}
	hasStage := map[script.QuestStage]bool{}
	for _, q := range c.Quests {
		attest("quest", q.ID, "name", q.Name, q.NameRevisionID)
		live[script.KindQuest][q.ID] = true
		quest := Quest{ID: q.ID, Name: q.Name, Stages: []Stage{}}
		for _, st := range stagesByQuest[q.ID] {
			attest("quest_stage", st.ID, "journal", st.Journal, st.JournalRevisionID)
			hasStage[script.QuestStage{Quest: q.ID, Stage: int(st.Number)}] = true
			quest.Stages = append(quest.Stages, Stage{Number: int(st.Number), Journal: st.Journal, Completes: st.Completes})
		}
		if len(quest.Stages) == 0 {
			warnf(CodeNoStages, "quest", q.ID, "quest %q has no stages", q.Name)
		}
		b.Quests = append(b.Quests, quest)
	}
	for _, conv := range c.Conversations {
		live[script.KindConversation][conv.ID] = true
	}

	// Scripts: every name must be live, and every stage must exist. Flags
	// read and set are tallied for the warnings below.
	read, set := map[string]bool{}, map[string]bool{}
	cond := func(kind, id, what string, raw []byte) *script.Cond {
		cd := app.DecodeCond(raw)
		if cd == nil {
			return nil
		}
		for _, ref := range cd.Refs() {
			if !live[ref.Kind][ref.ID] {
				errorf(CodeBadReference, kind, id, "%s %s's %s names a %s that was deleted (%s)", kind, id, what, ref.Kind, ref.ID)
			}
			if ref.Kind == script.KindFlag {
				read[ref.ID] = true
			}
		}
		for _, qs := range cd.QuestStages() {
			if live[script.KindQuest][qs.Quest] && !hasStage[qs] {
				errorf(CodeBadReference, kind, id, "%s %s's %s compares a quest with stage %d, which it does not have", kind, id, what, qs.Stage)
			}
		}
		return cd
	}
	effects := func(kind, id string, raw []byte) []script.Effect {
		es := app.DecodeEffects(raw)
		for _, e := range es {
			ref := e.Ref()
			if ref.Kind == "" {
				continue // an ability or the level: no content to check
			}
			if !live[ref.Kind][ref.ID] {
				errorf(CodeBadReference, kind, id, "%s %s's effects change a %s that was deleted (%s)", kind, id, ref.Kind, ref.ID)
				continue
			}
			if ref.Kind == script.KindFlag {
				set[ref.ID] = true
			}
			if ref.Kind == script.KindQuest && e.Value > 0 && !hasStage[script.QuestStage{Quest: e.Quest, Stage: e.Value}] {
				errorf(CodeBadReference, kind, id, "%s %s's effects move a quest to stage %d, which it does not have", kind, id, e.Value)
			}
		}
		return es
	}
	openings := 0

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
		out := Conversation{ID: conv.ID, Label: conv.Label, Repeatable: conv.Repeatable, Nodes: []Node{}}
		out.Condition = cond("conversation", conv.ID, "condition", conv.Condition)
		if conv.Label != "" {
			attest("conversation", conv.ID, "label", conv.Label, deref(conv.LabelRevisionID))
		}
		if conv.Opening {
			openings++
			b.Opening = conv.ID
		} else if conv.Label == "" {
			warnf(CodeOrphan, "conversation", conv.ID, "conversation %q has no label and does not open the game, so nothing starts it", conv.Title)
		}
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
			node := Node{ID: n.ID, Speaker: deref(n.SpeakerID), Text: n.Text, Effects: effects("node", n.ID, n.Effects), Choices: []Choice{}}
			for _, ch := range choicesByNode[n.ID] {
				attest("choice", ch.ID, "text", ch.Text, ch.TextRevisionID)
				if to := deref(ch.ToNodeID); to != "" && nodeConv[to] != conv.ID {
					errorf(CodeDanglingChoice, "choice", ch.ID, "choice %q leads to node %s, which was deleted or is in another conversation", ch.Text, to)
				}
				choice := Choice{
					ID: ch.ID, Text: ch.Text, To: deref(ch.ToNodeID),
					Condition: cond("choice", ch.ID, "condition", ch.Condition),
					Effects:   effects("choice", ch.ID, ch.Effects),
				}
				if skill := deref(ch.CheckSkillID); skill != "" {
					if !live[script.KindSkill][skill] {
						errorf(CodeBadCheck, "choice", ch.ID, "choice %q checks a skill that was deleted (%s)", ch.Text, skill)
					}
					fail, critS, critF := deref(ch.FailNodeID), deref(ch.CritSuccessNodeID), deref(ch.CritFailNodeID)
					for _, branch := range []struct{ what, node string }{{"fails", fail}, {"critically succeeds", critS}, {"critically fails", critF}} {
						if branch.node != "" && nodeConv[branch.node] != conv.ID {
							errorf(CodeBadCheck, "choice", ch.ID, "choice %q %s to node %s, which was deleted or is in another conversation", ch.Text, branch.what, branch.node)
						}
					}
					if !crits && (critS != "" || critF != "") {
						warnf(CodeCritUnused, "choice", ch.ID, "choice %q has a critical branch, but the %s ruleset has no critical successes or failures", ch.Text, rs.ID())
					}
					if d20 && live[script.KindSkill][skill] && skillAbility[skill] == "" && !noAbility[skill] {
						noAbility[skill] = true
						warnf(CodeNoAbility, "skill", skill, "skill %s is checked, but names no ability for the %s ruleset to add", skill, rs.ID())
					}
					choice.Check = &Check{Skill: skill, Difficulty: int(ch.CheckDifficulty), Fail: fail, CritSuccess: critS, CritFail: critF}
				}
				r.Stats.Words += len(strings.Fields(ch.Text))
				r.Stats.Choices++
				node.Choices = append(node.Choices, choice)
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

	if openings == 0 && len(c.Conversations) > 0 {
		errorf(CodeNoOpening, "game", "", "no conversation opens the game; mark one as the opening")
	}
	for _, f := range c.Flags {
		switch {
		case read[f.ID] && !set[f.ID] && flagInitial[f.ID] == 0:
			warnf(CodeNeverSet, "flag", f.ID, "flag %s is read but never set, and starts at 0", f.Key)
		case set[f.ID] && !read[f.ID]:
			warnf(CodeNeverRead, "flag", f.ID, "flag %s is set but never read", f.Key)
		}
	}

	for _, src := range slices.Sorted(maps.Keys(credits)) {
		b.Credits = append(b.Credits, Credit{Source: src, Notice: credits[src]})
	}

	r.Stats.Characters = len(b.Characters)
	r.Stats.Conversations = len(b.Conversations)
	r.Stats.Flags = len(b.Flags)
	r.Stats.Skills = len(b.Skills)
	r.Stats.Quests = len(b.Quests)
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
			next := []string{ch.To}
			if ch.Check != nil {
				next = append(next, ch.Check.Fail, ch.Check.CritSuccess, ch.Check.CritFail)
			}
			for _, to := range next {
				if _, ok := byID[to]; ok && !seen[to] {
					seen[to] = true
					stack = append(stack, to)
				}
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
	app.GameStore
	ListFlags(ctx context.Context, gameID string) ([]dbgen.Flag, error)
	ListSkills(ctx context.Context, gameID string) ([]dbgen.Skill, error)
	ListQuests(ctx context.Context, gameID string) ([]dbgen.Quest, error)
	ListAllQuestStages(ctx context.Context, gameID string) ([]dbgen.QuestStage, error)
	ListCharacters(ctx context.Context, gameID string) ([]dbgen.Character, error)
	ListConversations(ctx context.Context, gameID string) ([]dbgen.Conversation, error)
	ListAllNodes(ctx context.Context, gameID string) ([]dbgen.Node, error)
	ListAllChoices(ctx context.Context, gameID string) ([]dbgen.Choice, error)
	ListTextRevisionsByIDs(ctx context.Context, ids []string) ([]dbgen.ListTextRevisionsByIDsRow, error)
	CountOpenNotes(ctx context.Context, gameID string) (int64, error)
}

// Load reads everything Compile needs for a game.
func Load(ctx context.Context, s LoadStore, gameID string) (Content, error) {
	var c Content
	var err error
	if c.Game, err = app.Game(ctx, s, gameID); err != nil {
		return Content{}, err
	}
	if c.Characters, err = s.ListCharacters(ctx, gameID); err != nil {
		return Content{}, err
	}
	if c.Conversations, err = s.ListConversations(ctx, gameID); err != nil {
		return Content{}, err
	}
	if c.Nodes, err = s.ListAllNodes(ctx, gameID); err != nil {
		return Content{}, err
	}
	if c.Choices, err = s.ListAllChoices(ctx, gameID); err != nil {
		return Content{}, err
	}
	if c.Flags, err = s.ListFlags(ctx, gameID); err != nil {
		return Content{}, err
	}
	if c.Skills, err = s.ListSkills(ctx, gameID); err != nil {
		return Content{}, err
	}
	if c.Quests, err = s.ListQuests(ctx, gameID); err != nil {
		return Content{}, err
	}
	if c.Stages, err = s.ListAllQuestStages(ctx, gameID); err != nil {
		return Content{}, err
	}
	var ids []string
	for _, conv := range c.Conversations {
		if conv.LabelRevisionID != nil {
			ids = append(ids, *conv.LabelRevisionID)
		}
	}
	for _, sk := range c.Skills {
		ids = append(ids, sk.NameRevisionID)
	}
	for _, q := range c.Quests {
		ids = append(ids, q.NameRevisionID)
	}
	for _, st := range c.Stages {
		ids = append(ids, st.JournalRevisionID)
	}
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
	open, err := s.CountOpenNotes(ctx, gameID)
	if err != nil {
		return Content{}, err
	}
	c.OpenNotes = int(open)
	return c, nil
}
