// Package appmcp exposes the app over the Model Context Protocol. Each tool
// drives the same Endpoint the matching REST operation runs
// (server.BuildViewModel: Interact + Build) and wraps the view model in its
// own output in place of Render, so a rule lives in one place whichever
// surface calls it.
//
// The game's text is written by people, so there is deliberately no tool
// that writes it: Claude reads the content and leaves notes. The endpoints
// would refuse a connector token anyway (a connector is never a human
// caller), but a tool that cannot succeed is only a temptation. Tools lists
// every tool, and a test pins it. See docs/authorship.md.
//
// The caller is already authenticated by the time a tool runs (the /mcp/rpc
// route requires a credential), so a tool reads who it is acting as from
// the context with auth.KeyID, and never takes it as an argument.
package appmcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	apigen "github.com/KevinMcHugh/rpg/internal/api/gen"
	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/content"
	"github.com/KevinMcHugh/rpg/internal/server"
	"github.com/KevinMcHugh/rpg/internal/server/build"
	"github.com/KevinMcHugh/rpg/internal/server/characters"
	"github.com/KevinMcHugh/rpg/internal/server/conversations"
	"github.com/KevinMcHugh/rpg/internal/server/games"
	"github.com/KevinMcHugh/rpg/internal/server/me"
	"github.com/KevinMcHugh/rpg/internal/server/notes"
	"github.com/KevinMcHugh/rpg/internal/server/world"
)

// Instructions is sent to every client on connect.
const Instructions = `This is the authoring tool for story-driven party RPGs. It holds several games, each with its own ruleset (how skill checks resolve) and its own characters, conversations (a graph of lines, called nodes, joined by the choices a player makes), world (flags, skills, quests with numbered stages), and notes. Start with list_games, which lists the games the person you work for belongs to (you see exactly what they see): most tools take the game's id, and anything you look up by id tells you its game. The list_ tools return a page at a time (100 unless you ask for up to 200) and a next cursor: to see more, call again with after set to next, until next is empty.

Choices and conversations carry scripts. A condition says when a choice shows or a conversation can be started ("met_mayor and skill persuasion >= 3"); effects say what a line or a choice changes ("approval aria += 5; quest find_brother = 2"). A choice can be a skill check, resolved by its game's ruleset (classic 2d6; SRD 5.2, 3.5 SRD or Pathfinder 2e on a d20, where a skill adds an ability and Pathfinder has critical successes and failures), with nodes for failure and, under Pathfinder, for the criticals. get_world lists the keys scripts use. Read scripts closely: a choice whose condition can never hold, a flag nothing sets, or a quest stage nothing reaches are the kind of thing worth a note.

Every word a player sees is written by a person. You cannot change the game's content, and you must not write it for them. Your job is to read it closely and leave notes that make it better:
- gap: a path, consequence or character beat the content is missing ("nothing acknowledges that the player sided with the guild").
- continuity: something that contradicts the content elsewhere. Cite both places by id.
- plot_idea: a direction the story could take, a payoff for something set up earlier.
- pacing, mechanics: structure, length, how choices are spread, how hard checks are, which paths conditions open and close.
- question: something you need the author to decide or explain.

In a note, describe intent, function and character beats. Never write sample dialogue, a sample choice, a name or any prose that could be pasted into the game: say what a line needs to do, not what it should say. Keep notes short and specific, and target the narrowest thing they are about.

Before commenting on a line, read the character's bible (get_character) and how the player got there (get_path_to). get_path_to is the shortest route by graph, ignoring conditions; check whether the conditions on the way can hold. check_build lists what would stop the content shipping. list_notes shows what is already open, so you do not repeat it; reply_to_note continues a thread. Only the author resolves notes.`

// Tools is the name of every tool, in the order they are registered. None
// of them writes the game's content.
var Tools = []string{
	"whoami", "list_games",
	"list_characters", "get_character",
	"list_conversations", "get_conversation", "get_path_to",
	"get_world",
	"check_build",
	"list_notes", "get_note", "add_note", "reply_to_note",
}

// NewServer builds the MCP server with every tool registered.
func NewServer(store server.Store, links app.Links) *mcpsdk.Server {
	s := mcpsdk.NewServer(&mcpsdk.Implementation{Name: app.Name, Version: "0.1.0"},
		&mcpsdk.ServerOptions{Instructions: Instructions})
	registerRead(s, store, links)
	registerNotes(s, store, links)
	return s
}

// ---- inputs and outputs -----------------------------------------------------

type Empty struct{}

type IDInput struct {
	ID string `json:"id" jsonschema:"the id"`
}

type GameInput struct {
	GameID string `json:"game_id" jsonschema:"the game's id, from list_games"`
}

// ListInput is a page of one of a game's lists. A list tool returns a page
// and a next cursor: call it again with that as after for the next page,
// until next is empty.
type ListInput struct {
	GameID string `json:"game_id" jsonschema:"the game's id, from list_games"`
	Limit  int    `json:"limit,omitempty" jsonschema:"how many to return, from 1 to 200; omit for 100"`
	After  string `json:"after,omitempty" jsonschema:"the next value from the page before; omit for the first page"`
}

type ListGamesInput struct {
	Limit int    `json:"limit,omitempty" jsonschema:"how many to return, from 1 to 200; omit for 100"`
	After string `json:"after,omitempty" jsonschema:"the next value from the page before; omit for the first page"`
}

type ListGamesOutput struct {
	Games []games.Game `json:"games"`
	Next  string       `json:"next" jsonschema:"send as after for the next page; empty on the last page"`
}

type MeOutput struct {
	Me me.Me `json:"me"`
}

type ListCharactersOutput struct {
	Characters []characters.Character `json:"characters"`
	Next       string                 `json:"next" jsonschema:"send as after for the next page; empty on the last page"`
}

type CharacterOutput struct {
	Character characters.Character `json:"character"`
}

type ListConversationsOutput struct {
	Conversations []conversations.Conversation `json:"conversations"`
	Next          string                       `json:"next" jsonschema:"send as after for the next page; empty on the last page"`
}

type ConversationOutput struct {
	Conversation conversations.Graph `json:"conversation"`
}

type PathOutput struct {
	Path conversations.Path `json:"path"`
}

type WorldOutput struct {
	Flags  []world.Flag  `json:"flags"`
	Skills []world.Skill `json:"skills"`
	Quests []world.Quest `json:"quests"`
}

type BuildOutput struct {
	Report build.Report `json:"report"`
}

type ListNotesInput struct {
	GameID     string `json:"game_id" jsonschema:"the game's id, from list_games"`
	Status     string `json:"status,omitempty" jsonschema:"open, resolved or dismissed; omit for all"`
	TargetKind string `json:"target_kind,omitempty" jsonschema:"game, character, conversation, node, choice, flag, skill or quest; omit for all"`
	TargetID   string `json:"target_id,omitempty" jsonschema:"only notes on this id"`
	Limit      int    `json:"limit,omitempty" jsonschema:"how many to return, from 1 to 200; omit for 100"`
	After      string `json:"after,omitempty" jsonschema:"the next value from the page before; omit for the first page"`
}

type ListNotesOutput struct {
	Notes []notes.Note `json:"notes"`
	Next  string       `json:"next" jsonschema:"send as after for the next page; empty on the last page"`
}

type NoteOutput struct {
	Note notes.Note `json:"note"`
}

type AddNoteInput struct {
	GameID     string `json:"game_id" jsonschema:"the game's id; what the note is about must be in it"`
	TargetKind string `json:"target_kind" jsonschema:"what the note is about: game, character, conversation, node, choice, flag, skill or quest"`
	TargetID   string `json:"target_id,omitempty" jsonschema:"its id; omit for game"`
	Kind       string `json:"kind" jsonschema:"gap, continuity, plot_idea, pacing, mechanics, question or other"`
	Body       string `json:"body" jsonschema:"the note, up to 10000 characters. Describe intent; never write dialogue, choices, names or prose for the game"`
}

type ReplyInput struct {
	GameID string `json:"game_id" jsonschema:"the game's id"`
	NoteID string `json:"note_id" jsonschema:"the note to reply to"`
	Body   string `json:"body" jsonschema:"the reply. Describe intent; never write dialogue, choices, names or prose for the game"`
}

// ---- registration -----------------------------------------------------------

func registerRead(s *mcpsdk.Server, store server.Store, links app.Links) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "whoami",
		Description: "The api key this connection acts as, and whether it may write content (over MCP, it never may).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ Empty) (*mcpsdk.CallToolResult, MeOutput, error) {
		vm, err := server.BuildViewModel(ctx, me.Store(store), me.Endpoint{}, apigen.GetMeRequestObject{})
		return nil, MeOutput{Me: vm}, err
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_games",
		Description: "The games, oldest first, a page at a time, with their rulesets (classic 2d6, SRD 5.2, 3.5 SRD or Pathfinder 2e). Other tools take a game's id.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in ListGamesInput) (*mcpsdk.CallToolResult, ListGamesOutput, error) {
		vm, err := server.BuildViewModel(ctx, games.ListStore(store), games.ListEndpoint{Links: links}, apigen.ListGamesRequestObject{Params: apigen.ListGamesParams{Limit: in.Limit, After: in.After}})
		return nil, ListGamesOutput{Games: vm.Items, Next: vm.Next}, err
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_characters",
		Description: "The characters in a game, by name, a page at a time, with their bibles.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in ListInput) (*mcpsdk.CallToolResult, ListCharactersOutput, error) {
		vm, err := server.BuildViewModel(ctx, characters.ListStore(store), characters.ListEndpoint{Links: links}, apigen.ListCharactersRequestObject{GameId: in.GameID, Params: apigen.ListCharactersParams{Limit: in.Limit, After: in.After}})
		return nil, ListCharactersOutput{Characters: vm.Items, Next: vm.Next}, err
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_character",
		Description: "One character. The bible is the author's notes on their voice, wants and secrets: read it before commenting on anything they say.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in IDInput) (*mcpsdk.CallToolResult, CharacterOutput, error) {
		vm, err := server.BuildViewModel(ctx, characters.GetStore(store), characters.GetEndpoint{Links: links}, apigen.GetCharacterRequestObject{CharacterId: in.ID})
		return nil, CharacterOutput{Character: vm}, err
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_conversations",
		Description: "The conversations in a game, newest first, a page at a time, with their titles and summaries but not their nodes.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in ListInput) (*mcpsdk.CallToolResult, ListConversationsOutput, error) {
		vm, err := server.BuildViewModel(ctx, conversations.ListStore(store), conversations.ListEndpoint{Links: links}, apigen.ListConversationsRequestObject{GameId: in.GameID, Params: apigen.ListConversationsParams{Limit: in.Limit, After: in.After}})
		return nil, ListConversationsOutput{Conversations: vm.Items, Next: vm.Next}, err
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_conversation",
		Description: "One conversation in full: every node (a line, with its speaker) and the choices from it. A choice with no toNodeId ends the conversation.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in IDInput) (*mcpsdk.CallToolResult, ConversationOutput, error) {
		vm, err := server.BuildViewModel(ctx, conversations.GraphStore(store), conversations.GetEndpoint{Links: links}, apigen.GetConversationRequestObject{ConversationId: in.ID})
		return nil, ConversationOutput{Conversation: vm}, err
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_path_to",
		Description: "The shortest route a player takes from the conversation's start to a node: each line they read and the choice they made after it. Read it before commenting on a line.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in IDInput) (*mcpsdk.CallToolResult, PathOutput, error) {
		vm, err := server.BuildViewModel(ctx, conversations.PathStore(store), conversations.PathEndpoint{}, apigen.GetNodePathRequestObject{NodeId: in.ID})
		return nil, PathOutput{Path: vm}, err
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_world",
		Description: "A game's every flag (with what it means), skill (with the player's starting level) and quest (with its numbered stages and journal entries): the keys that conditions and effects use.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in GameInput) (*mcpsdk.CallToolResult, WorldOutput, error) {
		// The world is every key scripts use, so it is read whole.
		var out WorldOutput
		var err error
		if out.Flags, err = server.All(ctx, world.ListFlagsStore(store), world.ListFlagsEndpoint{Links: links}, func(after string) apigen.ListFlagsRequestObject {
			return apigen.ListFlagsRequestObject{GameId: in.GameID, Params: apigen.ListFlagsParams{Limit: app.MaxLimit, After: after}}
		}); err != nil {
			return nil, out, err
		}
		if out.Skills, err = server.All(ctx, world.ListSkillsStore(store), world.ListSkillsEndpoint{Links: links}, func(after string) apigen.ListSkillsRequestObject {
			return apigen.ListSkillsRequestObject{GameId: in.GameID, Params: apigen.ListSkillsParams{Limit: app.MaxLimit, After: after}}
		}); err != nil {
			return nil, out, err
		}
		out.Quests, err = server.All(ctx, world.ListQuestsStore(store), world.ListQuestsEndpoint{Links: links}, func(after string) apigen.ListQuestsRequestObject {
			return apigen.ListQuestsRequestObject{GameId: in.GameID, Params: apigen.ListQuestsParams{Limit: app.MaxLimit, After: after}}
		})
		return nil, out, err
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "check_build",
		Description: "Check a game's content as the build would. Errors stop it shipping (no start node or opening, a choice leading nowhere, a script naming something deleted, text no person wrote); warnings do not (unreachable nodes, flags set but never read or read but never set).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in GameInput) (*mcpsdk.CallToolResult, BuildOutput, error) {
		vm, err := server.BuildViewModel(ctx, content.LoadStore(store), build.Endpoint{Links: links}, apigen.GetBuildReportRequestObject{GameId: in.GameID})
		return nil, BuildOutput{Report: vm}, err
	})
}

func registerNotes(s *mcpsdk.Server, store server.Store, links app.Links) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_notes",
		Description: "A game's notes, oldest first, a page at a time, optionally filtered. Check what is already open before adding one.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in ListNotesInput) (*mcpsdk.CallToolResult, ListNotesOutput, error) {
		vm, err := server.BuildViewModel(ctx, notes.ListStore(store), notes.ListEndpoint{Links: links}, apigen.ListNotesRequestObject{GameId: in.GameID, Params: apigen.ListNotesParams{
			Status:     apigen.NoteStatus(in.Status),
			TargetKind: apigen.NoteTargetKind(in.TargetKind),
			TargetId:   in.TargetID,
			Limit:      in.Limit,
			After:      in.After,
		}})
		return nil, ListNotesOutput{Notes: vm.Items, Next: vm.Next}, err
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_note",
		Description: "One note.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in IDInput) (*mcpsdk.CallToolResult, NoteOutput, error) {
		vm, err := server.BuildViewModel(ctx, notes.GetStore(store), notes.GetEndpoint{Links: links}, apigen.GetNoteRequestObject{NoteId: in.ID})
		return nil, NoteOutput{Note: vm}, err
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "add_note",
		Description: "Leave a note for the author on the game, a character, a conversation, a node or a choice. Say what is missing, wrong or possible; never write the game's text.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in AddNoteInput) (*mcpsdk.CallToolResult, NoteOutput, error) {
		vm, err := server.BuildViewModel(ctx, notes.CreateStore(store), notes.CreateEndpoint{Links: links}, apigen.CreateNoteRequestObject{GameId: in.GameID, Body: &apigen.NoteCreate{
			TargetKind: apigen.NoteTargetKind(in.TargetKind),
			TargetId:   in.TargetID,
			Kind:       apigen.NoteKind(in.Kind),
			Body:       in.Body,
		}})
		return nil, NoteOutput{Note: vm}, err
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "reply_to_note",
		Description: "Reply in a note's thread, for example to answer the author's question on one of your notes.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in ReplyInput) (*mcpsdk.CallToolResult, NoteOutput, error) {
		vm, err := server.BuildViewModel(ctx, notes.CreateStore(store), notes.CreateEndpoint{Links: links}, apigen.CreateNoteRequestObject{GameId: in.GameID, Body: &apigen.NoteCreate{
			ParentId: in.NoteID,
			Body:     in.Body,
		}})
		return nil, NoteOutput{Note: vm}, err
	})
}
