package script_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/KevinMcHugh/rpg/internal/script"
)

func names() *script.Names {
	n := script.NewNames()
	n.Add(script.KindFlag, "F1", "met_mayor")
	n.Add(script.KindFlag, "F2", "gold")
	n.Add(script.KindSkill, "S1", "persuasion")
	n.Add(script.KindCharacter, "C1", "aria")
	n.Add(script.KindQuest, "Q1", "find_brother")
	n.Add(script.KindConversation, "V1", "docks")
	return n
}

func TestConditionsRoundTrip(t *testing.T) {
	for in, want := range map[string]string{
		"met_mayor":                               "met_mayor",
		"  NOT met_mayor ":                        "not met_mayor",
		"gold>=10":                                "gold >= 10",
		"met_mayor == true":                       "met_mayor == 1",
		"skill persuasion >= 3":                   "skill persuasion >= 3",
		"approval aria > -5":                      "approval aria > -5",
		"quest find_brother >= 2":                 "quest find_brother >= 2",
		"quest find_brother done":                 "quest find_brother done",
		"done docks":                              "done docks",
		"ability str >= 14":                       "ability str >= 14",
		"level>2 and not ability cha < 8":         "level > 2 and not ability cha < 8",
		"a_ and b":                                "",
		"met_mayor and gold > 1 or done docks":    "met_mayor and gold > 1 or done docks",
		"met_mayor and (gold > 1 or done docks)":  "met_mayor and (gold > 1 or done docks)",
		"not (met_mayor or gold > 1)":             "not (met_mayor or gold > 1)",
		"(met_mayor and gold > 1) and done docks": "met_mayor and gold > 1 and done docks",
		"": "",
	} {
		c, err := script.ParseCond(in, names())
		if want == "" && in != "" {
			if err == nil {
				t.Errorf("%q parsed; want an error", in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		got := script.FormatCond(c, names())
		if got != want {
			t.Errorf("%q formats as %q, want %q", in, got, want)
		}
		// What formats parses back to the same thing.
		again, err := script.ParseCond(got, names())
		if err != nil {
			t.Errorf("%q: reparse: %v", got, err)
			continue
		}
		a, _ := json.Marshal(c)
		b, _ := json.Marshal(again)
		if string(a) != string(b) {
			t.Errorf("%q: %s reparses as %s", in, a, b)
		}
	}
}

func TestConditionErrors(t *testing.T) {
	for in, want := range map[string]string{
		"met_mayr":            `no flag named "met_mayr" (there is gold, met_mayor)`,
		"skill charm >= 2":    `no skill named "charm"`,
		"gold = 3":            "use == to compare",
		"met_mayor and":       "expected a condition, found nothing at the end",
		"met_mayor gold":      `expected and, or or the end, found "gold" at column 11`,
		"(met_mayor":          "expected ), found nothing",
		"met_mayor; gold":     "join parts with and or or",
		"quest find_brother":  "expected a comparison",
		"met_mayor & gold":    `unexpected "&" at column 11`,
		"approval nobody > 1": `no character named "nobody"`,
		"done ?d0abc":         "conversation d0abc was deleted",
		"ability luck > 3":    "expected an ability (str dex con int wis cha)",
	} {
		_, err := script.ParseCond(in, names())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want one containing %q", in, err, want)
		}
	}
}

func TestEffectsRoundTrip(t *testing.T) {
	in := "met_mayor\ngold += 10; gold -= 2;; met_mayor = false\n skill persuasion += 1\napproval aria -= 5\nquest find_brother = 2\nability str = 16; level += 1\n"
	es, err := script.ParseEffects(in, names())
	if err != nil {
		t.Fatal(err)
	}
	want := "met_mayor\ngold += 10\ngold -= 2\nmet_mayor = 0\nskill persuasion += 1\napproval aria -= 5\nquest find_brother = 2\nability str = 16\nlevel += 1"
	if got := script.FormatEffects(es, names()); got != want {
		t.Fatalf("effects format as\n%s\nwant\n%s", got, want)
	}
	if es, err := script.ParseEffects("  ", names()); err != nil || len(es) != 0 {
		t.Fatalf("empty effects: %v %v", es, err)
	}
	for in, want := range map[string]string{
		"gold == 3":               "expected =, += or -=",
		"quest find_brother += 1": "expected =, found",
		"quest find_brother = -1": "0 or more",
		"gold = 1 met_mayor":      "expected ; or a new line",
		"skill persuasion":        "expected =, += or -=",
	} {
		if _, err := script.ParseEffects(in, names()); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want one containing %q", in, err, want)
		}
	}
}

// world is a script.World from maps.
type world struct {
	flags, skills, approval, quests, abilities map[string]int
	questDone, done                            map[string]bool
}

func (w world) Flag(id string) int       { return w.flags[id] }
func (w world) Skill(id string) int      { return w.skills[id] }
func (w world) Approval(id string) int   { return w.approval[id] }
func (w world) Quest(id string) int      { return w.quests[id] }
func (w world) QuestDone(id string) bool { return w.questDone[id] }
func (w world) Done(id string) bool      { return w.done[id] }
func (w world) Ability(key string) int   { return w.abilities[key] }

func TestEval(t *testing.T) {
	w := world{
		flags:     map[string]int{"F1": 1, "F2": 7},
		skills:    map[string]int{"S1": 3},
		approval:  map[string]int{"C1": -2},
		quests:    map[string]int{"Q1": 2},
		questDone: map[string]bool{},
		done:      map[string]bool{"V1": true},
		abilities: map[string]int{"str": 15, "level": 3},
	}
	for in, want := range map[string]bool{
		"met_mayor":                                 true,
		"not met_mayor":                             false,
		"gold >= 7 and gold < 8":                    true,
		"skill persuasion > 3":                      false,
		"approval aria < 0":                         true,
		"quest find_brother == 2":                   true,
		"quest find_brother done":                   false,
		"done docks":                                true,
		"skill persuasion > 3 or approval aria < 0": true,
		"not (met_mayor and done docks)":            false,
		"ability str >= 15 and level == 3":          true,
		"ability dex > 0":                           false,
	} {
		c, err := script.ParseCond(in, names())
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got := c.Eval(w); got != want {
			t.Errorf("%q is %v, want %v", in, got, want)
		}
	}
}

func TestKeys(t *testing.T) {
	for _, ok := range []string{"aria", "act_2", "x"} {
		if err := script.CheckKey(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Aria", "2nd", "has space", "quest", "and", "level", "ability"} {
		if script.CheckKey(bad) == nil {
			t.Errorf("%q passed", bad)
		}
	}
	for in, want := range map[string]string{"Aria Venn": "aria_venn", "The Citadel's Gate!": "the_citadel_s_gate", "2nd Act": "k_2nd_act", "Quest": "quest_"} {
		if got := script.Slug(in); got != want || script.CheckKey(got) != nil {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}
