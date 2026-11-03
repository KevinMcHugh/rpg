# Auth

There are no tenants and no open sign-up. An **api key** is an identity, a person's or an agent's. Every other credential (OAuth tokens, web sessions) acts as the key that approved it. `auth.KeyID(ctx)` is how code learns who is calling, and `auth.IsHuman(ctx)` whether a person is: only a person may write the game's content ([authorship.md](authorship.md)).

## Credentials

| credential | prefix | minted by | used for |
| --- | --- | --- | --- |
| api key | `rpg_` | `server keys create --name NAME [--human]` | `Authorization: Bearer` from scripts and Claude Code; signing in to the pages |
| OAuth access token | `rpg_at_` | `/oauth/token` (24 h) | claude.ai custom connectors |
| OAuth refresh token | `rpg_rt_` | `/oauth/token` (30 d, single use) | getting a new access token |
| web session | `rpg_at_`, in the `rpg_session` cookie | `/login` (7 d, HttpOnly, SameSite=Lax) | the pages |
| invite | `rpg_iv_` | `server invites create --note NAME [--expires-in 168h]` | a newcomer's first OAuth connection |

Only a SHA-256 hash of any of them is stored. Raw values are printed once, by the CLI, and never again. Deleting a key (`server keys delete --id …`) cuts off every token and session it approved, because those lookups join on a live key. `server keys rotate --id …` deletes a key and mints a replacement with the same name.

The prefixes are constants in `internal/auth/auth.go`.

## The OAuth flow (claude.ai)

1. claude.ai calls an MCP route with no credential and gets a 401 whose `WWW-Authenticate` header points at `/.well-known/oauth-protected-resource`.
2. It reads discovery, registers itself at `/oauth/register` (RFC 7591 dynamic client registration; public clients, no secret), and sends the person to `/oauth/authorize` with a PKCE S256 challenge.
3. The person pastes an api key, or an invite if they are new. The server mints a one-time code bound to the client, the redirect URI and the challenge, and redirects back.
4. claude.ai trades the code and its verifier at `/oauth/token` for an access/refresh pair. Each refresh revokes the old pair and issues a new one.

Registration stays open, because claude.ai registers before any person is involved and has nowhere to put an invite. A registered client can do nothing until someone approves it with a key or an invite.

## Invites

An invite is redeemed *for* a key. Pasting one on the authorize page claims it (one `UPDATE … WHERE used_at IS NULL`, so two racing redemptions cannot both win), mints an api key named after the invite's note, and stores the OAuth code, all in one transaction. A failure partway leaves the invite unspent. Nobody sees the minted key: the newcomer acts through the tokens it approved, `keys list` shows it by name, and `keys delete` cuts them off like anyone else.

Invites cover OAuth only, and the key one mints is an agent's: it only ever acts through the connector it approved. A co-writer needs a human key minted for them (`server keys create --name ana --human`) to sign in to the pages and write.

## Public versus private

**The spec decides.** The global `security` in `api/openapi.yaml` requires a bearer token or the session cookie. An operation with `security: []` is public. An operation that says nothing inherits the requirement, so a new operation is private until someone deliberately opens it. `server.PublicOperations()` reads the public set from the embedded spec, and `TestPublicOperations` pins it, so opening an operation is a visible change in review.

- Public API responses carry `Access-Control-Allow-Origin: *`, so another site can fetch them. No credentials are allowed cross-site, so this exposes nothing an anonymous caller could not already get. Private operations send no CORS header.
- Nothing is public: the content is unreleased, so the API, MCP and every page need a credential. `TestPublicOperations` pins the public set as empty.
- Every write route on the pages uses `auth.Require` and `sameOrigin`, which refuses a POST whose `Origin` header names another site. That is a second lock after SameSite=Lax. Hiding a form from an agent is only for looks; the endpoints refuse it.
- MCP (`/mcp/rpc`) always requires a credential.

## Who can do what

There are two kinds of caller. A **person** (a human key, directly or through a web session) can do anything the API allows. An **agent** (an agent key, or any OAuth connector token) can read everything and write notes, and nothing else. The kind is a column on `api_keys`, `auth.Resolve` turns it into `auth.Caller.Human`, and the spec says which writes an agent may make (`x-agent-writable: true`). [authorship.md](authorship.md) has the details.
