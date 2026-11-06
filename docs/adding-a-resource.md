# Adding a resource, end to end

A recipe for a new CRUD resource, using `widget` as the example. The characters resource (`internal/server/characters`) is a complete worked example of every step, including a player-facing field written through `app.Revise`; copying it and renaming is usually the quickest start.

## 1. Migration

```sh
make new-migration NAME=create_widgets
```

```sql
-- migrate:up

CREATE TABLE widgets (
    id          CHAR(20)    PRIMARY KEY,
    name        TEXT        NOT NULL,
    created_by  CHAR(20)    REFERENCES api_keys(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMPTZ
);

CREATE INDEX widgets_active ON widgets (created_at DESC) WHERE deleted_at IS NULL;

-- migrate:down

DROP TABLE widgets;
```

Never edit an applied migration; add another.

## 2. Queries

`db/queries/widgets.sql`:

```sql
-- name: CreateWidget :one
INSERT INTO widgets (id, name, created_by) VALUES ($1, $2, $3) RETURNING *;

-- name: GetWidget :one
SELECT * FROM widgets WHERE id = $1 AND deleted_at IS NULL;

-- name: ListWidgets :many
SELECT * FROM widgets WHERE deleted_at IS NULL ORDER BY created_at DESC;

-- One page of the list, after the key of the last row on the page before
-- (app.Paginate). The sort ends in the id, so the key is unique per row.
-- name: PageWidgets :many
SELECT * FROM widgets WHERE deleted_at IS NULL
  AND (sqlc.narg(after_id)::text IS NULL OR (created_at, id) < (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::text))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(lim);

-- name: UpdateWidget :one
UPDATE widgets SET name = COALESCE(sqlc.narg(name), name), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL RETURNING *;

-- name: DeleteWidget :execrows
UPDATE widgets SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;
```

Every read and write filters `deleted_at IS NULL`. `ListWidgets` is for code that needs every row; the list endpoint uses `PageWidgets`. Keep their order the same, and flip the comparison (`>` for an ascending sort, `<` for a descending one) to match it.

## 3. Spec

Add paths and schemas to `api/openapi.yaml`, following the characters entries. Then decide who may call each operation:

- Leave `security` out and the operation is **private**, inheriting the global bearer-or-cookie requirement.
- Add `security: []` only to make it **public**. Public responses also get `Access-Control-Allow-Origin: *`.
- Every write needs a **person** unless it says `x-agent-writable: true`. Add that only for feedback-like data an agent should be able to write, never for anything a player sees ([authorship.md](authorship.md)).

For a PATCH body, mark each optional field `x-go-type-skip-optional-pointer: false`, so "not sent" differs from "sent empty". Every PATCH body also requires `updatedAt` (`format: date-time`), every PATCH declares `"409": $ref: "#/components/responses/Conflict"`, and the resource's schema shows `updatedAt` so a client has one to send back; `TestEveryPatchIsGuarded` checks the first two. A list operation (`listWidgets`) takes `$ref: "#/components/parameters/Limit"` and `After`, its list schema has a required `next`, and it declares the 422; `TestEveryListIsPaged` checks all three.

Name path parameters so they do not collide with schema names: oapi-codegen generates a Go type for both. Creature Lab's `Form` parameter clashed with its `Form` schema, for example.

## 4. Generate

```sh
make generate
```

The build now fails at `var _ apigen.StrictServerInterface = (*Server)(nil)` until step 6 is done. That is expected.

## 5. Endpoints

Create `internal/server/widgets/widgets.go`, using `characters/characters.go` as the model:

- a view model `Widget` with JSON tags, `ToViewModel(dbgen.Widget, app.Links) Widget`, and an unexported `toAPI(Widget) apigen.Widget`;
- for each operation, a narrow `Store` interface and an endpoint struct with `Interact`, `Build` and `Render`;
- validation in `Interact`, returning `fmt.Errorf("%w: …", app.ErrBadInput)` or `app.NotFound(err, "widget", id)`;
- `auth.KeyIDPtr(ctx)` for `created_by`;
- if the resource belongs to a game (most do): a `game_id` column, list and create under `/games/{gameId}` checked with `app.Game`, and `app.SameGame` on anything it connects to;
- `app.Access(ctx, store, kind, id)` first in every endpoint that takes the resource's id, which keeps it private to its game's members, with a case for the kind in the `GameAccess` query (`db/queries/games.sql`) and its id parameter in `TestEveryOperationIsMembersOnly`;
- `app.RequireHuman(ctx)` first in every write, and, for a player-facing field, a `*_revision_id` column written through `app.Revise` in the same transaction (`InTx`), plus a check in `internal/content` so the build verifies it;
- on the update endpoint, a `Guard` method naming the row and the `updatedAt` the caller sent (`app.Guard{Kind: "widget", ID: req.WidgetId, Seen: req.Body.UpdatedAt}`), with the write done in `InTx`, which checks it; and a CTE and arm for the kind in `LockForWrite` (`db/queries/guard.sql`). See "Stale writes" in [architecture.md](architecture.md);
- on the list endpoint, a `Page` method naming the list (`app.PageRequest{List: "widget", Limit: req.Params.Limit, After: req.Params.After}`), an `Interact` that returns `app.Paginate` over `PageWidgets` with the row's sort key, and `Build` and `Render` over the page with `app.MapPage`. See "Lists" in [architecture.md](architecture.md).

If a view model links to a page, add a method to `app.Links`.

If an operation needs a transaction, add a method to `app.Store` (see `RedeemInvite`) and to the `server.Store` interface.

## 6. Wire into the server

In `internal/server/server.go`, add one method per operation and map errors to the responses the spec declares:

```go
func (s *Server) GetWidget(ctx context.Context, req apigen.GetWidgetRequestObject) (apigen.GetWidgetResponseObject, error) {
	resp, err := Run(ctx, widgets.GetEndpoint{Store: s.store, Links: s.links}, req)
	if isNotFound(err) {
		return apigen.GetWidget404JSONResponse{NotFoundJSONResponse: notFound(err)}, nil
	}
	return resp, err
}
```

Check `req.Body == nil` in operations that take a body, and return the 400. A PATCH runs with `RunGuarded` instead of `Run`, and maps `isConflict(err)` to its 409 with `conflict(err)`. A list runs with `RunPaged`, and maps `isUnprocessable(err)` to its 422.

## 7. MCP tools

If Claude should see it, add tools in `internal/appmcp/appmcp.go`: an input struct with `jsonschema` descriptions, an output struct wrapping the view model, and a handler that calls `server.BuildViewModel` with a request object it fills in. Keep tool descriptions specific about when to use the tool. Say "confirm with the user first" on anything destructive, and update `Instructions` if the typical session changes.

## 8. The authoring app

If people should see it in the browser, add a screen to the authoring app in `ui/`. `make generate` has already given it the new operations' types (`ui/src/api/schema.ts`); call them with `api.GET`/`api.POST` from `ui/src/api/client.ts`, and add the route to `ui/src/App.tsx` and to the list of app paths in `internal/web/server.go`. Hide editing controls when `useMe().human` is false; that is only for looks, and the endpoints are what refuse an agent. Run `cd ui && npm run typecheck`.

## 9. Tests

- If the public or agent-writable set changed, update `TestPublicOperations` or `TestAgentWritableOperations` in `internal/server/server_test.go`. Add the new writes to `TestHumanOnlyOperations` and `TestAgentCallers`.
- Add a flow to `internal/web/e2e_test.go`: create, read, update, delete over the API, plus the MCP tools. Run it with `TEST_DATABASE_URL` set.

## 10. Docs

If the resource has rules a newcomer would otherwise have to reverse-engineer, write them down in `docs/`.

## Common pitfalls

- Forgetting `make generate` after editing the spec or SQL. The compiler will not find the new types, and CI's `make check` fails.
- A query that scans deleted rows (a missing `AND deleted_at IS NULL`).
- Putting a rule in an MCP tool or in the app instead of the endpoint, so the other surfaces skip it.
- Making a route public in the router instead of with `security: []` in the spec.
