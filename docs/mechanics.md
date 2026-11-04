# Mechanics: world state, scripts and the game

The content is more than dialogue. Choices check skills, change approval and move quests on, and what the player did decides what they see later. This page covers how that is authored, built and played.

## Games

The tool holds several games. Each has its own characters, conversations, world, notes, build and playtest saves, and keys are unique only within a game, so two games can both have a flag called `met_mayor`. A line cannot be spoken by another game's character, a check cannot use another game's skill, and a note must be about something in its game. `make bundle GAME=key` builds one game.

Each game also has a ruleset, which decides how its checks resolve: `classic` (2d6 plus the skill, below), `srd52`, `srd35` or `pf2e`. Content from before there were games is the first game, `first`, on `classic`.

## The world

| thing | key example | player-facing | authoring-only |
| --- | --- | --- | --- |
| flag | `met_mayor` | | key, description, initial value |
| skill | `persuasion` | name (shown on checks) | key, starting level |
| quest | `find_brother` | name, each stage's journal entry | key, summary |
| character | `aria` | name | key, bible, whether a companion |
| conversation | `docks` | label (what the player picks to start it) | key, title, summary, condition, repeatable, opening |

A **key** is what scripts call something. Keys are lowercase letters, digits and underscores, and unique among live things of their kind. Leave one out and it is made from the name. Scripts are stored by id, so renaming a key changes every script that uses it at once.

A **flag** is a number; 0 is false. A **quest** is at a stage number; 0 is not started, and a stage marked "completes" finishes it. A **companion** has approval of the player, which the game shows.

## Scripts

Typed into the forms, sent as text over the API, stored as JSON with ids, and shown back with today's keys (`internal/script`).

**Conditions** decide when a choice shows and when a conversation can be started:

```
met_mayor                      a flag is set (not 0)
not met_mayor
gold >= 10                     == != >= <= > <
skill persuasion >= 3          the player's skill level
approval aria > 10             a character's approval of the player
quest find_brother >= 2        a quest's stage number
quest find_brother done        it reached a stage that completes it
done docks                     a conversation was finished at least once
a and (b or not c)             and binds tighter than or
```

**Effects** run when the player reaches a line or takes a choice, one per line or separated by `;`:

```
met_mayor                      set a flag to 1
met_mayor = false              true is 1, false is 0
gold += 10                     -= subtracts
skill persuasion += 1
approval aria -= 5
quest find_brother = 2         move a quest to a stage
```

A script that names something unknown is refused when saved, with the keys that do exist. One that names something later deleted keeps working in the editor (the key shows as `?id`) and fails the build.

## Skill checks

A choice can be a check: 2d6 plus the player's skill level against a difficulty (1 to 30). Success goes where the choice leads; failure goes to the failure line, or ends the conversation. The player sees the skill, their level, the difficulty and the chance before choosing, as in Disco Elysium. The choice's effects run either way. Dice come from the save's seed, so reloading does not reroll.

## How the game flows

The game opens with the **opening** conversation (the first one created, until you mark another). A conversation ends when the player reaches a line with no choice they can take. Between conversations, the player picks from those that have a **label**, whose **condition** holds, and that they have not finished (unless **repeatable**). When none is left, the game is over.

Choices cannot jump between conversations. To continue a story elsewhere, set a flag or a quest stage, and make the next conversation's condition read it.

## The build

On top of provenance (see [authorship.md](authorship.md)), the build checks:

- errors: a script naming something deleted; a quest moved to, or compared with, a stage it does not have; a check on a deleted skill or failing to a line elsewhere; no opening conversation.
- warnings: a flag read but never set (and starting at 0, so the condition never changes); a flag set but never read; a quest with no stages; a conversation with no label that does not open the game, so nothing starts it; lines nothing reaches.

## Playing

- **Playtest** (`/play` in the authoring tool) plays the content as it stands, rebuilt on every click, with saves per api key. It can start at any line ("Playtest from here" on a line in the conversation editor) and run setup effects first (`knows_mayor; skill persuasion = 5`). Build errors show above the game rather than stopping it.
- **The game** (`cmd/game`) plays `content.json` from `make bundle`, with saves in a Postgres database of its own (migrated with the same migrations; it only uses `saves`). `make play GAME_DATABASE_URL=…` runs it on :8090. It has no accounts, so run it locally or behind something that authenticates.

Both use the same engine (`internal/game`), which is pure: a state and a choice in, a state out.
