// Package app holds what every surface (REST, MCP, web pages) shares: the
// Store, the errors the surfaces map to their own responses, and Links.
// Domain rules that more than one endpoint needs live here too.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/xid"

	"github.com/KevinMcHugh/rpg/internal/auth"
	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
)

// Name is the app's display name, on the pages, the OAuth authorize page
// and the MCP server's implementation info.
const Name = "RPG"

// Errors the surfaces (REST, MCP, web) map to their own responses: REST to
// 404, 422 and 403, the pages to the same statuses, and MCP to a tool error.
var (
	ErrNotFound  = errors.New("not found")
	ErrBadInput  = errors.New("bad input")
	ErrForbidden = errors.New("forbidden")
)

// RequireHuman refuses an agent caller. Every endpoint that changes the
// game's content calls it first, whichever surface runs it: the REST guard
// in server.Mount covers only the API, and a page can be signed in with an
// agent key. See docs/authorship.md.
func RequireHuman(ctx context.Context) error {
	if !auth.IsHuman(ctx) {
		return fmt.Errorf("%w: only a person may change the game's content; an agent may leave a note instead", ErrForbidden)
	}
	return nil
}

// MaxBodyBytes bounds a request body.
const MaxBodyBytes = 1 << 20

// Store is the app's persistence: every sqlc query, plus the operations that
// need a transaction. Endpoints each declare a narrow interface of just the
// methods they call, and *Store satisfies all of them.
type Store struct {
	*dbgen.Queries
	Pool *pgxpool.Pool
}

// NewStore builds a Store on pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{Queries: dbgen.New(pool), Pool: pool}
}

// RedeemInvite spends a single-use invite: it mints the newcomer's api key
// (named after the invite's note) and runs approve with that key on the same
// transaction, so an invite is only used up if what it was redeemed for
// (an OAuth code) is stored too. An unknown, used, expired or deleted invite
// is pgx.ErrNoRows. The raw key is never shown to anyone: the newcomer acts
// through the OAuth tokens it approves, and deleting the key cuts them off.
func (s *Store) RedeemInvite(ctx context.Context, raw string, approve func(q *dbgen.Queries, apiKeyID string) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // a no-op after Commit
	q := s.WithTx(tx)
	inv, err := q.ClaimInviteByHash(ctx, auth.Hash(raw))
	if err != nil {
		return err
	}
	name := inv.Note
	if name == "" {
		name = "invite " + inv.ID
	}
	key, err := auth.GenerateKey()
	if err != nil {
		return err
	}
	// The key only ever acts through the connector it approves, so it is an
	// agent's.
	row, err := q.CreateAPIKey(ctx, dbgen.CreateAPIKeyParams{ID: xid.New().String(), Name: name, KeyHash: auth.Hash(key), Kind: auth.KindAgent})
	if err != nil {
		return err
	}
	if err := q.SetInviteKey(ctx, dbgen.SetInviteKeyParams{ID: inv.ID, ApiKeyID: &row.ID}); err != nil {
		return err
	}
	if err := approve(q, row.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// InTx runs fn on one transaction, committing only if it returns nil.
func (s *Store) InTx(ctx context.Context, fn func(q *dbgen.Queries) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // a no-op after Commit
	if err := fn(s.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Revise records body as the new text of a player-facing field (a
// character's name, a node's line, a choice's text) and returns the
// revision's id, which the caller stores on the row beside the text. It
// refuses an agent caller; the database refuses one too.
func Revise(ctx context.Context, q *dbgen.Queries, targetKind, targetID, field, body string) (string, error) {
	if err := RequireHuman(ctx); err != nil {
		return "", err
	}
	row, err := q.CreateTextRevision(ctx, dbgen.CreateTextRevisionParams{
		ID:         xid.New().String(),
		TargetKind: targetKind,
		TargetID:   targetID,
		Field:      field,
		Body:       body,
		AuthorKey:  auth.KeyID(ctx),
	})
	return row.ID, err
}

// Links builds the absolute URLs that view models carry.
type Links struct {
	// Base is the app's public origin ("https://app-xyz.sprites.app").
	// Empty gives relative links.
	Base string
}

// NewLinks trims base for joining.
func NewLinks(base string) Links { return Links{Base: strings.TrimRight(base, "/")} }

// Character is a character's page.
func (l Links) Character(id string) string { return l.Base + "/characters/" + id }

// Conversation is a conversation's page.
func (l Links) Conversation(id string) string { return l.Base + "/conversations/" + id }

// Node is a node's place on its conversation's page.
func (l Links) Node(conversationID, nodeID string) string {
	return l.Conversation(conversationID) + "#node-" + nodeID
}

// NodeByID is a node's place on its conversation's page, by way of a
// redirect, for when only the node's id is at hand.
func (l Links) NodeByID(id string) string { return l.Base + "/nodes/" + id }

// ChoiceByID is the place of a choice's node, by way of a redirect.
func (l Links) ChoiceByID(id string) string { return l.Base + "/choices/" + id }

// Target is the page of a character, conversation, node or choice by kind,
// and the notes page for anything else (a note on the whole game).
func (l Links) Target(kind, id string) string {
	switch kind {
	case "character":
		return l.Character(id)
	case "conversation":
		return l.Conversation(id)
	case "node":
		return l.NodeByID(id)
	case "choice":
		return l.ChoiceByID(id)
	default:
		return l.Notes()
	}
}

// Notes is the page of every open note.
func (l Links) Notes() string { return l.Base + "/notes" }

// Build is the build report's page.
func (l Links) Build() string { return l.Base + "/build" }

// NotFound turns pgx's no-rows into ErrNotFound, naming what was missing.
func NotFound(err error, what, id string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s %s", ErrNotFound, what, id)
	}
	return err
}
