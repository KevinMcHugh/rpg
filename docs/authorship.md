# Authorship: people write the game, Claude reviews it

Every word a player sees is written by a person. Claude helps by reading the content and leaving **notes**: gaps, continuity problems, plot ideas, pacing, questions. It never writes dialogue, choices or names, not even as a suggestion to accept.

That rule is enforced by the system, not left to a prompt. Four layers each catch what the one before misses.

## What counts as content

| player-facing (people only) | authoring-only (never ships) |
| --- | --- |
| a character's name | a character's bible and key |
| a node's line | a conversation's title, summary and key |
| a choice's text | flags: keys, descriptions |
| a conversation's label | a quest's summary and key |
| a skill's name | notes |
| a quest's name, and each stage's journal entry | |

Structure and rules (which conversation a node is in, who speaks it, where a choice leads, conditions, effects, skill checks, flags) are also people-only, because they change what players see.

## Who is a person

A request is **human** only when it comes from a human api key, used directly or through a web session signed in with one. Everything else is an **agent**:

| credential | human? |
| --- | --- |
| `server keys create --name kev --human` (`make key`), used as a bearer token | yes |
| a web session signed in with a human key | yes |
| any key minted without `--human` (`make agent-key`) | no |
| a claude.ai connector's OAuth token, **even one a human key approved** | no |
| a key minted by redeeming an invite | no |

The connector case is the important one. You approve claude.ai's connector by pasting your own key, so its token acts as your key, but what holds it is a model. `auth.Resolve` marks a token with a client as non-human whatever key is behind it, and `auth.IsHuman(ctx)` is the only thing code checks.

Give Claude Code an agent key, not yours.

## The four layers

1. **No MCP tool writes content.** `appmcp.Tools` lists every tool: reads, `check_build`, and notes. A test pins the list against a real MCP session, so adding a content tool fails CI.
2. **The spec decides which writes need a person.** Every operation that is not a GET is human-only unless its spec entry says `x-agent-writable: true`. Today only `POST /games/{gameId}/notes` does. `Server.Mount` refuses an agent with a 403 before the body is even decoded. `TestAgentWritableOperations` pins the agent-writable set and `TestHumanOnlyOperations` checks that every new write is human-only by default.
3. **The endpoints refuse agents themselves.** MCP does not pass the REST guard, and the guard is one more place a rule could go missing. So every content endpoint calls `app.RequireHuman(ctx)` first, and every player-facing string is written through `app.Revise`, which refuses too.
4. **The database and the build keep the record.** `app.Revise` stores each new string in `text_revisions` with the key that wrote it, and the row points at its latest revision. A trigger refuses any revision whose author is not a human key, whatever the application does. The build (`server build`, `GET /api/build`, the Build screen) then checks every shipped string: its revision must exist, belong to that row and field, match the row's text exactly, and have been written by a human key. Text changed in `psql` or by a buggy migration fails the build.

The trigger checks only the key's kind, so on its own it would accept a connector token acting as a human key. Layer 3 is what refuses that; the trigger is the backstop against agent keys.

## The exception: the game's interface

The game's own interface has a few words of its own: "Journal updated", "Quest complete", "approves" and "disapproves", "Success" and "Failure", "The End", "Begin", "Continue". They are in `internal/game/play/templates` and Claude wrote them when it built the game, so they are not human-written and the build does not check them. Rewrite them in your own words. If you want them checked too, move them into the content (a settings table of interface strings, each with a revision) so the build covers them like everything else.

## The soft spot

Nothing in code stops a note from containing a sample line, or you from pasting it into the game. The MCP instructions tell Claude to describe intent and never write dialogue, choices, names or prose, and the tool descriptions repeat it. Whether a note's words end up in the game is your call.

## Notes

A note targets the whole game, a character, a conversation, a node or a choice, and has a kind: `gap`, `continuity`, `plot_idea`, `pacing`, `mechanics`, `question` or `other`. Replies (`parentId`) join the parent's thread and target. Anyone may write notes; only a person may resolve, dismiss, reopen or delete them.

`by_human` on a note is recorded from the request when it is written, not looked up from `created_by`, because a connector's notes are filed under the human key that approved it. The app marks agent notes, and styles notes nothing like content.
