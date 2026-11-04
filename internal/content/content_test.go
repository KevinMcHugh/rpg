package content_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/KevinMcHugh/rpg/internal/content"
	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
	"github.com/KevinMcHugh/rpg/internal/script"
)

func ptr(s string) *string { return &s }

// fixture is a small, healthy conversation: Aria greets the player, who can
// ask about her brother or leave.
//
//	n1 Aria: "You came back."
//	  c1 "Tell me about your brother." → n2
//	  c2 "I have to go."               → (end)
//	n2 Aria: "There is nothing to tell."
func fixture() content.Content {
	c := content.Content{
		Characters:    []dbgen.Character{{ID: "aria", Name: "Aria", NameRevisionID: "r-aria", Bible: "SECRET: her brother is alive."}},
		Conversations: []dbgen.Conversation{{ID: "v1", Title: "Homecoming", Summary: "authoring only", StartNodeID: ptr("n1"), Opening: true}},
		Nodes: []dbgen.Node{
			{ID: "n1", ConversationID: "v1", SpeakerID: ptr("aria"), Text: "You came back.", TextRevisionID: "r-n1"},
			{ID: "n2", ConversationID: "v1", SpeakerID: ptr("aria"), Text: "There is nothing to tell.", TextRevisionID: "r-n2"},
		},
		Choices: []dbgen.Choice{
			{ID: "c1", FromNodeID: "n1", ToNodeID: ptr("n2"), Text: "Tell me about your brother.", TextRevisionID: "r-c1"},
			{ID: "c2", FromNodeID: "n1", Text: "I have to go.", TextRevisionID: "r-c2"},
		},
		Revisions: map[string]dbgen.ListTextRevisionsByIDsRow{},
		OpenNotes: 2,
	}
	rev := func(id, kind, target, field, body string) {
		c.Revisions[id] = dbgen.ListTextRevisionsByIDsRow{ID: id, TargetKind: kind, TargetID: target, Field: field, Body: body, AuthorKind: "human", AuthorName: "kev"}
	}
	rev("r-aria", "character", "aria", "name", "Aria")
	rev("r-n1", "node", "n1", "text", "You came back.")
	rev("r-n2", "node", "n2", "text", "There is nothing to tell.")
	rev("r-c1", "choice", "c1", "text", "Tell me about your brother.")
	rev("r-c2", "choice", "c2", "text", "I have to go.")
	return c
}

// codes is each issue as "code target".
func codes(issues []content.Issue) []string {
	out := make([]string, len(issues))
	for i, is := range issues {
		out[i] = is.Code + " " + is.TargetID
	}
	return out
}

func TestHealthyContentBuilds(t *testing.T) {
	bundle, report := content.Compile(fixture())
	if !report.OK() || len(report.Warnings) != 0 {
		t.Fatalf("errors %v, warnings %v", codes(report.Errors), codes(report.Warnings))
	}
	want := content.Stats{Characters: 1, Conversations: 1, Nodes: 2, Choices: 2, Words: 17, OpenNotes: 2}
	if bundle.Opening != "v1" {
		t.Fatalf("opening %q", bundle.Opening)
	}
	if report.Stats != want {
		t.Fatalf("stats %+v, want %+v", report.Stats, want)
	}
	if got := bundle.Conversations[0]; got.Start != "n1" || len(got.Nodes) != 2 || got.Nodes[0].Choices[0].To != "n2" || got.Nodes[0].Choices[1].To != "" {
		t.Fatalf("bundle %+v", got)
	}

	// Nothing authoring-only ships.
	b, _ := json.Marshal(bundle)
	for _, secret := range []string{"SECRET", "Homecoming", "authoring only"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("bundle contains %q: %s", secret, b)
		}
	}
}

func TestProvenance(t *testing.T) {
	for name, tc := range map[string]struct {
		break_ func(c *content.Content)
		want   string
	}{
		"an agent wrote the line": {func(c *content.Content) {
			r := c.Revisions["r-n2"]
			r.AuthorKind, r.AuthorName = "agent", "claude"
			c.Revisions["r-n2"] = r
		}, "provenance n2"},
		"the line was changed without a revision": {func(c *content.Content) {
			c.Nodes[1].Text = "Something nobody signed for."
		}, "provenance n2"},
		"the revision is missing": {func(c *content.Content) {
			delete(c.Revisions, "r-c1")
		}, "provenance c1"},
		"the row borrows another row's revision": {func(c *content.Content) {
			c.Choices[1].TextRevisionID = "r-c1"
			c.Choices[1].Text = "Tell me about your brother."
		}, "provenance c2"},
		"a character's name": {func(c *content.Content) {
			c.Characters[0].Name = "Arya"
		}, "provenance aria"},
	} {
		t.Run(name, func(t *testing.T) {
			c := fixture()
			tc.break_(&c)
			_, report := content.Compile(c)
			if got := codes(report.Errors); len(got) != 1 || got[0] != tc.want {
				t.Fatalf("errors %v, want [%s]", got, tc.want)
			}
		})
	}
}

func TestGraphChecks(t *testing.T) {
	for name, tc := range map[string]struct {
		break_        func(c *content.Content)
		errors, warns []string
	}{
		"no start": {func(c *content.Content) {
			c.Conversations[0].StartNodeID = nil
		}, []string{"no_start v1"}, nil},
		"start deleted": {func(c *content.Content) {
			c.Conversations[0].StartNodeID = ptr("gone")
		}, []string{"bad_start v1"}, nil},
		"choice into a deleted node": {func(c *content.Content) {
			c.Nodes = c.Nodes[:1]
		}, []string{"dangling_choice c1"}, nil},
		"speaker deleted": {func(c *content.Content) {
			c.Characters = nil
		}, []string{"missing_speaker n1", "missing_speaker n2"}, nil},
		"unreachable node": {func(c *content.Content) {
			c.Choices = c.Choices[1:]
		}, nil, []string{"unreachable n2"}},
	} {
		t.Run(name, func(t *testing.T) {
			c := fixture()
			tc.break_(&c)
			_, report := content.Compile(c)
			if got := codes(report.Errors); strings.Join(got, ",") != strings.Join(tc.errors, ",") {
				t.Errorf("errors %v, want %v", got, tc.errors)
			}
			if got := codes(report.Warnings); strings.Join(got, ",") != strings.Join(tc.warns, ",") {
				t.Errorf("warnings %v, want %v", got, tc.warns)
			}
		})
	}
}

// withMechanics adds a flag, a skill, a quest and scripts to the fixture:
// asking about the brother is a Persuasion check, and starts the quest.
func withMechanics() content.Content {
	c := fixture()
	rev := func(id, kind, target, field, body string) {
		c.Revisions[id] = dbgen.ListTextRevisionsByIDsRow{ID: id, TargetKind: kind, TargetID: target, Field: field, Body: body, AuthorKind: "human", AuthorName: "kev"}
	}
	c.Flags = []dbgen.Flag{{ID: "f1", Key: "asked"}}
	c.Skills = []dbgen.Skill{{ID: "s1", Key: "persuasion", Name: "Persuasion", NameRevisionID: "r-s1"}}
	rev("r-s1", "skill", "s1", "name", "Persuasion")
	c.Quests = []dbgen.Quest{{ID: "q1", Key: "find_brother", Name: "Find Her Brother", NameRevisionID: "r-q1"}}
	rev("r-q1", "quest", "q1", "name", "Find Her Brother")
	c.Stages = []dbgen.QuestStage{{ID: "st1", QuestID: "q1", Number: 1, Journal: "Aria has a brother.", JournalRevisionID: "r-st1"}}
	rev("r-st1", "quest_stage", "st1", "journal", "Aria has a brother.")
	cond, _ := json.Marshal(script.Cond{Not: &script.Cond{Flag: "f1", Op: "!=", Value: 0}})
	eff, _ := json.Marshal([]script.Effect{{Flag: "f1", Op: "=", Value: 1}, {Quest: "q1", Op: "=", Value: 1}})
	c.Choices[0].Condition, c.Choices[0].Effects = cond, eff
	c.Choices[0].CheckSkillID, c.Choices[0].CheckDifficulty, c.Choices[0].FailNodeID = ptr("s1"), 8, ptr("n1")
	return c
}

func TestMechanics(t *testing.T) {
	bundle, report := content.Compile(withMechanics())
	if !report.OK() || len(report.Warnings) != 0 {
		t.Fatalf("errors %v, warnings %v", codes(report.Errors), codes(report.Warnings))
	}
	ch := bundle.Conversations[0].Nodes[0].Choices[0]
	if ch.Check == nil || ch.Check.Skill != "s1" || ch.Check.Fail != "n1" || ch.Condition == nil || len(ch.Effects) != 2 {
		t.Fatalf("choice %+v", ch)
	}
	if len(bundle.Quests) != 1 || bundle.Quests[0].Stages[0].Journal != "Aria has a brother." || bundle.Skills[0].Name != "Persuasion" {
		t.Fatalf("world %+v %+v", bundle.Quests, bundle.Skills)
	}

	for name, tc := range map[string]struct {
		break_        func(c *content.Content)
		errors, warns []string
	}{
		"a script names a deleted flag": {func(c *content.Content) {
			c.Flags = nil
		}, []string{"bad_reference c1", "bad_reference c1"}, nil},
		"a script moves a quest to a stage it lacks": {func(c *content.Content) {
			c.Stages = nil
		}, []string{"bad_reference c1"}, []string{"no_stages q1"}},
		"a check on a deleted skill": {func(c *content.Content) {
			c.Skills = nil
		}, []string{"bad_check c1"}, nil},
		"a journal entry no human wrote": {func(c *content.Content) {
			r := c.Revisions["r-st1"]
			r.AuthorKind = "agent"
			c.Revisions["r-st1"] = r
		}, []string{"provenance st1"}, nil},
		"a flag set that nothing reads": {func(c *content.Content) {
			c.Choices[0].Condition = nil
		}, nil, []string{"flag_never_read f1"}},
		"a flag read that nothing sets": {func(c *content.Content) {
			eff, _ := json.Marshal([]script.Effect{{Quest: "q1", Op: "=", Value: 1}})
			c.Choices[0].Effects = eff
		}, nil, []string{"flag_never_set f1"}},
		"abilities are not content": {func(c *content.Content) {
			eff, _ := json.Marshal([]script.Effect{{Flag: "f1", Op: "=", Value: 1}, {Quest: "q1", Op: "=", Value: 1}, {Ability: "str", Op: "+=", Value: 1}, {Ability: "level", Op: "=", Value: 2}})
			c.Choices[0].Effects = eff
		}, nil, nil},
		"no opening": {func(c *content.Content) {
			c.Conversations[0].Opening = false
		}, []string{"no_opening "}, []string{"orphan v1"}},
	} {
		t.Run(name, func(t *testing.T) {
			c := withMechanics()
			tc.break_(&c)
			_, report := content.Compile(c)
			if got := codes(report.Errors); strings.Join(got, ",") != strings.Join(tc.errors, ",") {
				t.Errorf("errors %v, want %v", got, tc.errors)
			}
			if got := codes(report.Warnings); strings.Join(got, ",") != strings.Join(tc.warns, ",") {
				t.Errorf("warnings %v, want %v", got, tc.warns)
			}
		})
	}
}
