package content_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/KevinMcHugh/rpg/internal/content"
	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
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
		Conversations: []dbgen.Conversation{{ID: "v1", Title: "Homecoming", Summary: "authoring only", StartNodeID: ptr("n1")}},
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
