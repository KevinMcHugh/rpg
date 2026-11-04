package game_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/KevinMcHugh/rpg/internal/content"
	"github.com/KevinMcHugh/rpg/internal/game"
	"github.com/KevinMcHugh/rpg/internal/script"
)

// bundle is a small game. It opens at the docks, where Aria greets the
// player. Asking about her brother is a Persuasion check (difficulty 8);
// success starts the quest and earns her approval, failure ends the talk.
// Only someone who never met the mayor can claim to be a stranger. After
// the docks, the player can visit the mayor, once.
func bundle() content.Bundle {
	cond := func(c script.Cond) *script.Cond { return &c }
	return content.Bundle{
		Opening:    "docks",
		Characters: []content.Character{{ID: "aria", Name: "Aria", Companion: true}, {ID: "mayor", Name: "The Mayor"}},
		Flags:      []content.Flag{{ID: "met_mayor"}},
		Skills:     []content.Skill{{ID: "persuasion", Name: "Persuasion", Initial: 2}},
		Quests: []content.Quest{{ID: "brother", Name: "Find Her Brother", Stages: []content.Stage{
			{Number: 1, Journal: "Aria has a brother."},
			{Number: 2, Journal: "He is found.", Completes: true},
		}}},
		Conversations: []content.Conversation{
			{ID: "docks", Start: "d1", Nodes: []content.Node{
				{ID: "d1", Speaker: "aria", Text: "You came back.", Choices: []content.Choice{
					{ID: "ask", Text: "Tell me about your brother.", To: "d2",
						Effects: []script.Effect{{Approval: "aria", Op: "+=", Value: 5}},
						Check:   &content.Check{Skill: "persuasion", Difficulty: 8, Fail: "d3"}},
					{ID: "stranger", Text: "I'm new here.", To: "d3", Condition: cond(script.Cond{Not: &script.Cond{Flag: "met_mayor", Op: "!=", Value: 0}})},
					{ID: "leave", Text: "Goodbye."},
				}},
				{ID: "d2", Speaker: "aria", Text: "He's in the Citadel.", Effects: []script.Effect{{Quest: "brother", Op: "=", Value: 1}}},
				{ID: "d3", Speaker: "aria", Text: "Forget it."},
			}},
			{ID: "mayor", Label: "Visit the mayor", Start: "m1", Nodes: []content.Node{
				{ID: "m1", Speaker: "mayor", Text: "Welcome.", Effects: []script.Effect{{Flag: "met_mayor", Op: "=", Value: 1}}},
			}},
			{ID: "rescue", Label: "Storm the Citadel", Condition: cond(script.Cond{Quest: "brother", Op: ">=", Value: 1}), Start: "r1", Nodes: []content.Node{
				{ID: "r1", Text: "You find him.", Effects: []script.Effect{{Quest: "brother", Op: "=", Value: 2}}},
			}},
		},
	}
}

func lastLine(s *game.State) game.Entry {
	for i := len(s.Log) - 1; i >= 0; i-- {
		if s.Log[i].Kind == "line" {
			return s.Log[i]
		}
	}
	return game.Entry{}
}

func ids(opts []game.Option) string {
	var out []string
	for _, o := range opts {
		out = append(out, o.ID)
	}
	return strings.Join(out, ",")
}

func TestOpeningAndOptions(t *testing.T) {
	g := game.New(bundle())
	s := g.NewState(1)
	if s.Conversation != "docks" || lastLine(s).Speaker != "Aria" || lastLine(s).Text != "You came back." {
		t.Fatalf("opening: %+v", s)
	}
	opts := g.Options(s)
	if ids(opts) != "ask,stranger,leave" {
		t.Fatalf("options %s", ids(opts))
	}
	// Persuasion 2 against 8 needs 6 or more on 2d6: 26 of 36.
	if c := opts[0].Check; c == nil || c.Skill != "Persuasion" || c.Level != 2 || c.Bonus != 2 || c.Chance != 72 {
		t.Fatalf("check %+v", c)
	}
	if g.Available(s) != nil {
		t.Fatal("conversations are available mid-conversation")
	}
	if err := g.Choose(s, "nope"); err != game.ErrUnavailable {
		t.Fatalf("unknown choice: %v", err)
	}
}

// The same seed makes the same rolls, so a check's outcome is fixed by the
// save; across seeds, both outcomes happen.
func TestChecksAreSeeded(t *testing.T) {
	g := game.New(bundle())
	outcomes := map[bool]bool{}
	for seed := uint64(0); seed < 40; seed++ {
		a, b := g.NewState(seed), g.NewState(seed)
		if err := g.Choose(a, "ask"); err != nil {
			t.Fatal(err)
		}
		_ = g.Choose(b, "ask")
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if string(ja) != string(jb) {
			t.Fatalf("seed %d plays two ways", seed)
		}
		var check game.Entry
		for _, e := range a.Log {
			if e.Kind == "check" {
				check = e
			}
		}
		outcomes[check.Success] = true
		want := "Forget it."
		if check.Success {
			want = "He's in the Citadel."
		}
		if lastLine(a).Text != want || check.Success != (check.Dice[0]+check.Dice[1]+2 >= 8) {
			t.Fatalf("seed %d: %s, then %q", seed, check.Describe(), lastLine(a).Text)
		}
		// Approval comes with asking, whatever the dice say.
		if a.Approval["aria"] != 5 {
			t.Fatalf("approval %d", a.Approval["aria"])
		}
	}
	if !outcomes[true] || !outcomes[false] {
		t.Fatalf("40 seeds gave only %v", outcomes)
	}
}

func TestAFullGame(t *testing.T) {
	g := game.New(bundle())
	// Find a seed where the check succeeds.
	var s *game.State
	for seed := uint64(0); ; seed++ {
		s = g.NewState(seed)
		_ = g.Choose(s, "ask")
		if s.Quests["brother"] == 1 {
			break
		}
	}
	// The line had no choices, so the conversation ended.
	if s.Conversation != "" || !s.Done["docks"] {
		t.Fatalf("after the docks: %+v", s)
	}
	if j := g.Journal(s); len(j) != 1 || j[0].Journal != "Aria has a brother." || j[0].Done {
		t.Fatalf("journal %+v", j)
	}
	if p := g.Party(s); len(p) != 1 || p[0].Name != "Aria" || p[0].Value != 5 {
		t.Fatalf("party %+v", p)
	}
	avail := g.Available(s)
	if len(avail) != 2 || avail[0].ID != "mayor" || avail[1].ID != "rescue" {
		t.Fatalf("available %+v", avail)
	}
	if err := g.Begin(s, "docks"); err != game.ErrUnavailable {
		t.Fatalf("the opening has no label, so it cannot be picked: %v", err)
	}
	if err := g.Begin(s, "mayor"); err != nil {
		t.Fatal(err)
	}
	if s.Flags["met_mayor"] != 1 || !s.Done["mayor"] {
		t.Fatalf("after the mayor: %+v", s)
	}
	// Not repeatable, so it is gone; the rescue is left.
	if avail := g.Available(s); len(avail) != 1 || avail[0].ID != "rescue" {
		t.Fatalf("available %+v", avail)
	}
	if err := g.Begin(s, "rescue"); err != nil {
		t.Fatal(err)
	}
	if j := g.Journal(s); len(j) != 1 || !j[0].Done {
		t.Fatalf("journal %+v", j)
	}
	if !g.Over(s) {
		t.Fatal("the game should be over")
	}
}

// A choice that raises the skill it checks rolls with the value the
// player was shown, and raises it afterwards.
func TestChecksRollBeforeEffects(t *testing.T) {
	b := bundle()
	b.Conversations[0].Nodes[0].Choices[0].Effects = []script.Effect{{Skill: "persuasion", Op: "+=", Value: 10}}
	g := game.New(b)
	s := g.NewState(7)
	shown := g.Options(s)[0].Check.Bonus
	if err := g.Choose(s, "ask"); err != nil {
		t.Fatal(err)
	}
	for _, e := range s.Log {
		if e.Kind == "check" && e.Bonus != shown {
			t.Fatalf("rolled with +%d, shown +%d", e.Bonus, shown)
		}
	}
	if s.Skills["persuasion"] != 12 {
		t.Fatalf("persuasion %d after the choice", s.Skills["persuasion"])
	}
}

func TestConditionsHideChoices(t *testing.T) {
	g := game.New(bundle())
	s := g.NewState(1)
	g.Apply(s, []script.Effect{{Flag: "met_mayor", Op: "=", Value: 1}})
	if ids(g.Options(s)) != "ask,leave" {
		t.Fatalf("options %s", ids(g.Options(s)))
	}
	if err := g.Choose(s, "stranger"); err != game.ErrUnavailable {
		t.Fatalf("a hidden choice was taken: %v", err)
	}
}

func TestStaleSaves(t *testing.T) {
	g := game.New(bundle())
	s := g.NewState(1)
	s.Node = "gone"
	if err := g.Choose(s, "ask"); err != game.ErrStale {
		t.Fatalf("stale: %v", err)
	}
	g.Recover(s)
	if s.Conversation != "" || len(g.Available(s)) == 0 {
		t.Fatalf("recovered: %+v", s)
	}
	if err := g.StartAt(s, "d2"); err != nil || s.Conversation != "" || s.Quests["brother"] != 1 {
		t.Fatalf("start at d2: %v %+v", err, s)
	}
}

// A pf2e game: an easy Diplomacy check, with all four branches.
func TestCriticalBranches(t *testing.T) {
	b := content.Bundle{
		Ruleset: "pf2e", Opening: "v", Characters: []content.Character{{ID: "aria", Name: "Aria"}},
		Skills: []content.Skill{{ID: "dip", Name: "Diplomacy", Initial: 2, Ability: "cha"}},
		Conversations: []content.Conversation{{ID: "v", Start: "n", Nodes: []content.Node{
			{ID: "n", Text: "Well?", Choices: []content.Choice{{ID: "ask", Text: "Please.", To: "ok",
				Check: &content.Check{Skill: "dip", Difficulty: 15, Fail: "no", CritSuccess: "great", CritFail: "awful"}}}},
			{ID: "ok", Text: "ok"}, {ID: "no", Text: "no"}, {ID: "great", Text: "great"}, {ID: "awful", Text: "awful"},
		}}},
	}
	g := game.New(b)
	reached := map[string]bool{}
	for seed := uint64(0); seed < 200; seed++ {
		s := g.NewState(seed)
		// Charisma +2, level 3, expert (rank 2): 2 + 3 + 4 = +9.
		g.Apply(s, []script.Effect{{Ability: "cha", Op: "=", Value: 2}, {Ability: "level", Op: "=", Value: 3}})
		if o := g.Options(s); seed == 0 && (o[0].Check.Bonus != 9 || o[0].Check.Chance != 75) {
			t.Fatalf("check option %+v", o[0].Check)
		}
		if err := g.Choose(s, "ask"); err != nil {
			t.Fatal(err)
		}
		var check game.Entry
		for _, e := range s.Log {
			if e.Kind == "check" {
				check = e
			}
		}
		line := lastLine(s).Text
		want := map[string]string{"critical_success": "great", "success": "ok", "failure": "no", "critical_failure": "awful"}[check.Degree]
		if line != want || len(check.Dice) != 1 {
			t.Fatalf("seed %d: %s rolled %v, then %q", seed, check.Degree, check.Dice, line)
		}
		reached[line] = true
	}
	// A d20 +9 against 15 can land in all four: 1 is a critical failure
	// (10, stepped down), 2-5 fail, 6-15 succeed, 16-20 succeed critically.
	if len(reached) != 4 {
		t.Fatalf("200 seeds reached only %v", reached)
	}
}
