# Auth

There is no open sign-up. A **user** is a person. They sign in with Google, or with an **api key** the CLI minted for them, and each of their keys (their own, and their agents') belongs to them. Every other credential (OAuth tokens, web sessions) acts as the key that approved it. `auth.KeyID(ctx)` is the key a request acts as, `auth.UserID(ctx)` the person it acts for, and `auth.IsHuman(ctx)` whether a person is calling: only a person may write the game's content ([authorship.md](authorship.md)).

A **game** belongs to its members: one **owner**, who made it, and **writers**, whom the owner invited. A user sees only their games, and so do their agents. See "Games and members" below.

## Credentials

| credential | prefix | minted by | used for |
| --- | --- | --- | --- |
| api key | `rpg_` | `server keys create --name NAME [--human] [--user USER]` | `Authorization: Bearer` from scripts and Claude Code; signing in to the authoring app |
| OAuth access token | `rpg_at_` | `/oauth/token` (24 h) | claude.ai custom connectors |
| OAuth refresh token | `rpg_rt_` | `/oauth/token` (30 d, single use) | getting a new access token |
| web session | `rpg_at_`, in the `rpg_session` cookie | `/login` (7 d, HttpOnly, SameSite=Lax) | the authoring app and the playtest |
| web session | `rpg_at_`, in the cookie | Google sign-in (`/auth/google`, when configured) | the same, for someone who signs in with Google |
| invite | `rpg_iv_` | `server invites create --note NAME [--expires-in 168h]` | a newcomer's first OAuth connection, or first Google sign-in |
| game invite | `rpg_gi_` | a game's owner, on its Members page (`POST /games/{gameId}/invites`) | a link (`/join/…`) that makes whoever redeems it a writer in the game |

Only a SHA-256 hash of any of them is stored. Raw values are printed once, by the CLI, and never again. Deleting a key (`server keys delete --id …`) cuts off every token and session it approved, because those lookups join on a live key. `server keys rotate --id …` deletes a key and mints a replacement with the same name.

The prefixes are constants in `internal/auth/auth.go`.

## The OAuth flow (claude.ai)

1. claude.ai calls an MCP route with no credential and gets a 401 whose `WWW-Authenticate` header points at `/.well-known/oauth-protected-resource`.
2. It reads discovery, registers itself at `/oauth/register` (RFC 7591 dynamic client registration; public clients, no secret), and sends the person to `/oauth/authorize` with a PKCE S256 challenge.
3. The person pastes an api key, or an invite if they are new, or signs in with Google (when it is configured), which carries the request through Google and back. The server mints a one-time code bound to the client, the redirect URI and the challenge, and redirects back.
4. claude.ai trades the code and its verifier at `/oauth/token` for an access/refresh pair. Each refresh revokes the old pair and issues a new one.

Registration stays open, because claude.ai registers before any person is involved and has nowhere to put an invite. A registered client can do nothing until someone approves it with a key or an invite.

## CLI invites

An invite is redeemed *for* a key and a new user. Pasting one on the authorize page claims it (one `UPDATE … WHERE used_at IS NULL`, so two racing redemptions cannot both win), mints an api key named after the invite's note, and stores the OAuth code, all in one transaction. A failure partway leaves the invite unspent. Nobody sees the minted key: the newcomer acts through the tokens it approved, `keys list` shows it by name, and `keys delete` cuts them off like anyone else.

The key one mints is an agent's: it only ever acts through the connector it approved. The same invite can be redeemed by signing in with Google instead (`/auth/google?invite=…`), which makes the user a Google sign-in rather than an agent key. Either way the newcomer is in no game: a game's owner invites them to one with a game invite, which also works for someone with no account at all.

## Signing in with Google

When `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` are set ([configuration.md](configuration.md)), the sign-in page, the join page and the OAuth authorize page offer "Sign in with Google", after the inventory app's `internal/idp`. Google's answer is matched to a user by its (provider, subject) pair in `user_identities`:

- **Known**: that user is signed in.
- **Unknown, with an invite** (a game's or the CLI's): a new user is made, named as Google names them, and the invite is spent.
- **Unknown, without one**: refused. There is no open sign-up.
- **Linking**: someone signed in with a key can add Google as a way in from `/auth/google?link=1`.

A Google sign-in acts as the user's own human key, which it mints the first time and never shows. So a session from Google is the same thing as a session from pasting a key on `/login`, and a text revision records that key as its author, as for anyone. The state carried through Google (where to go after, the OAuth request it interrupted, an invite) waits in `sso_login_intents` for ten minutes.

## Games and members

Every endpoint starts with `app.Access` (or `app.Game`, which calls it): it finds the game the thing belongs to and the caller's role in it, and anything in a game the caller is not a member of is a **404**, exactly like something that does not exist, so a game's existence does not leak. This holds on every surface, because REST, MCP and the playtest all run the same endpoints. `TestEveryOperationIsMembersOnly` sends every operation in the spec as a stranger and expects a 404, so an endpoint that forgets the check fails it.

| | owner | writer | their agents | anyone else |
| --- | --- | --- | --- | --- |
| read the game, leave notes | yes | yes | yes | 404 |
| write content | yes | yes | no (403) | 404 |
| rename or delete the game, change its ruleset | yes | 403 | 403 | 404 |
| make, list and revoke invite links | yes | 403 | 403 | 404 |
| take a member out | anyone but themselves | only themselves (leaving) | 403 | 404 |

Whoever creates a game owns it. A **game invite** is a single-use link the owner makes on the game's Members page; only a hash of its code is stored, so the link is shown once. Opening it shows the game's name and who sent it. Accepting needs a person signed in: with their key, or with Google, which makes their account if they are new. Redeeming it is one `UPDATE … WHERE used_at IS NULL`, so two people racing one link cannot both join. A link is not a credential: it grants nothing until someone signs in to spend it, and no API or MCP operation redeems one. Request logs show `[invite]` in place of a code.

Server commands (`server build`, `server import`) run as `auth.System`, which reaches every game. `server members add --game GAME --user USER [--role owner]` puts someone in a game directly, which is how to give an owner to a game made by a binary from before there were users.

## Public versus private

**The spec decides.** The global `security` in `api/openapi.yaml` requires a bearer token or the session cookie. An operation with `security: []` is public. An operation that says nothing inherits the requirement, so a new operation is private until someone deliberately opens it. `server.PublicOperations()` reads the public set from the embedded spec, and `TestPublicOperations` pins it, so opening an operation is a visible change in review.

- Public API responses carry `Access-Control-Allow-Origin: *`, so another site can fetch them. No credentials are allowed cross-site, so this exposes nothing an anonymous caller could not already get. Private operations send no CORS header.
- Nothing is public but the app's built files, which hold no content: the content is unreleased, so the API, MCP, the app's page and the playtest need a credential. `TestPublicOperations` pins the public set as empty.
- The app calls `/api` with the session cookie, so `/api` (and the sign-in, sign-out and playtest forms) sit behind `sameOrigin`, which refuses a write whose `Origin` header names another site. That is a second lock after SameSite=Lax. A script with a bearer token sends no `Origin` and passes. Hiding an editing control from an agent is only for looks; the endpoints refuse it.
- MCP (`/mcp/rpc`) always requires a credential.

## Who can do what

Within the games they belong to, there are two kinds of caller. A **person** (a human key, directly or through a web session) can do anything the API allows, as their role permits. An **agent** (an agent key, or any OAuth connector token) can read its person's games and write notes, and nothing else. The kind is a column on `api_keys`, `auth.Resolve` turns it into `auth.Caller.Human`, and the spec says which writes an agent may make (`x-agent-writable: true`). [authorship.md](authorship.md) has the details.
