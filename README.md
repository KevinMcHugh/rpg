# RPG

The authoring tool for a story-driven party RPG in the BioWare mold: a big plot in acts, told through conversations, quests and companions, presented as text in the spirit of Disco Elysium or classic Fallout.

People write the game. Claude reads it over the [Model Context Protocol](https://modelcontextprotocol.io) and leaves notes (gaps, continuity problems, plot ideas, pacing, questions), but cannot write a word a player sees. That is enforced by the API, the endpoints, the database and the build, not by a prompt: see [docs/authorship.md](docs/authorship.md).

Content is authored here, in Postgres, and compiled into a bundle the game runs on. The database tolerates work in progress; the build refuses anything that would break in play, or that no person wrote.

It is built on [app-template](https://github.com/KevinMcHugh/app-template): one Go binary serves the authoring app (React, embedded in the binary), a JSON API and MCP, and deploys to a [sprites.dev](https://sprites.dev) sprite. [docs/architecture.md](docs/architecture.md) explains the choices.

## What is here

- **Characters**: a player-facing name, and a bible (voice, wants, secrets) that never ships but that Claude reads before commenting on anything they say.
- **Conversations**: a graph of nodes (a line, spoken by a character or the narrator) joined by choices (what the player says, leading to another node or ending the conversation). Titles and summaries are authoring labels. Edit them as a list of lines, or as a graph you can pan and zoom, with the selected line's editor beside it.
- **Notes**: feedback on the game, a character, a conversation, a node or a choice, from you or from Claude, with threads and open/resolved/dismissed.
- **Provenance**: every player-facing string is stored with a revision recording which key wrote it, and only a human key may write one.
- **The world**: flags, skills, and quests with numbered stages and journal entries; companions with approval.
- **Scripts**: conditions on choices and conversations (`not met_mayor and skill persuasion >= 3`), effects on lines and choices (`approval aria += 5; quest find_brother = 2`), and skill checks (2d6 plus a skill against a difficulty, with a failure branch). See [docs/mechanics.md](docs/mechanics.md).
- **The build**: `server build` (and the Build screen, and Claude's `check_build`) checks provenance, start nodes, choices leading nowhere, scripts naming deleted things, flags nothing sets or reads, and unreachable lines, then writes `content.json` with only what players see.
- **The game**: playtest the content as it stands at `/play`, from the start or any line, with the world set up however you like; or play `content.json` in `cmd/game`, the game itself.

## Run it locally

Prereqs: Go (the version in `go.mod`; `go` fetches it if yours is older), Node 22 and Postgres. sqlc, oapi-codegen and dbmate run through `go run …@version`, so you do not install them.

```sh
export DATABASE_URL="postgres://localhost/rpg?sslmode=disable"
createdb rpg
make ui                         # build the authoring app, which the server embeds
make dev                        # migrate, then serve on :8080
make key NAME=you               # in another terminal: your key, which may write content (printed once)
make agent-key NAME=claude-code # Claude's key, which may only read and leave notes
```

Open http://localhost:8080, sign in with your key, and write a conversation. While working on the app itself, `make ui-dev` serves it on :5173 with live reload, against `make dev`. Connect Claude Code with the agent key:

```sh
claude mcp add --transport http rpg http://localhost:8080/mcp/rpc --header "Authorization: Bearer rpg_…"
```

Then ask it to review a conversation. It reads the characters' bibles and the path to each line, checks the build, and leaves notes beside the lines they are about.

`make bundle` writes `content.json`, or tells you what stops it, and `make play GAME_DATABASE_URL=…` plays it in the game binary.

## Layout

```
api/openapi.yaml        the HTTP API's contract (and who may call what)
cmd/server/             the authoring tool: serve, build, keys, invites
cmd/game/               the game: plays content.json
db/migrations/          dbmate migrations
db/queries/             sqlc queries
deploy/                 sprite scripts: run-postgres.sh, redeploy.sh
docs/                   how it works and why
internal/app/           Store, Revise, RequireHuman, Links, shared errors and the app's name
internal/content/       the build: checks the content and compiles the game's bundle
internal/script/        the condition and effect language
internal/game/          the engine, and (play/) the game's pages
internal/auth/          key/token formats, middleware, who-is-calling context
internal/oauth/         the OAuth 2.1 server claude.ai needs
internal/server/        Endpoint contract, the composed REST server, one package per resource
internal/appmcp/        MCP tools over the same endpoints
ui/                     the authoring app (React, TypeScript, Vite); its API types come from the spec
internal/ui/            embeds the app's build and serves it
internal/web/           the router, the sign-in page, end-to-end tests
internal/api/gen/       oapi-codegen output: do not edit
internal/db/gen/        sqlc output: do not edit
```

## Make targets

| target | what it does |
| --- | --- |
| `make ui` | build the authoring app into the server |
| `make ui-dev` | serve the app with live reload on :5173, against `make dev` |
| `make dev` | migrate, then run the server on :8080 |
| `make generate` | regenerate sqlc, oapi-codegen and the app's API types after any SQL, migration or spec change |
| `make check` | fail if the generated code is stale (CI runs it) |
| `make migrate` / `make rollback` | dbmate up / roll back the last migration |
| `make new-migration NAME=…` | create a timestamped migration file |
| `make key NAME=…` | mint a person's api key, which may write content |
| `make agent-key NAME=…` | mint an agent's api key, which may only read and leave notes |
| `make bundle` | check the content and write `content.json` |
| `make play GAME_DATABASE_URL=…` | play `content.json` in the game binary on :8090 |
| `make invite NOTE=…` | mint a single-use invite |
| `make build` | build the app, then compile `bin/server` and `bin/game` |
| `make test` | run the tests; set `TEST_DATABASE_URL` to a scratch database **the tests may wipe** to include the end-to-end ones |

## Docs

- [Authorship](docs/authorship.md): people write the game, Claude reviews it, and how that is enforced
- [Mechanics](docs/mechanics.md): the world, the script language, skill checks, the build, and playing
- [Architecture](docs/architecture.md): the layers, the request flow, and why it is built this way
- [Adding a resource](docs/adding-a-resource.md): the end-to-end recipe
- [Auth](docs/auth.md): keys (human and agent), OAuth, invites, sessions
- [Deploy](docs/deploy.md): the sprites.dev recipe (by CLI or through the Fly.io MCP connector) and CI redeploys
- [Configuration](docs/configuration.md): config and secrets live on the sprite service's environment, never in env files
- [CLAUDE.md](CLAUDE.md): orientation for Claude Code sessions
