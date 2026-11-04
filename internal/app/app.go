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
// 404, 422 and 403 (and ErrConflict, in guard.go, to 409), the pages to the
// same statuses, and MCP to a tool error.
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

// RedeemInvite spends a single-use invite: it makes the newcomer a user and
// mints their api key (both named after the invite's note), and runs approve
// with that key on the same transaction, so an invite is only used up if
// what it was redeemed for (an OAuth code) is stored too. An unknown, used,
// expired or deleted invite is pgx.ErrNoRows. The raw key is never shown to
// anyone: the newcomer acts through the OAuth tokens it approves, and
// deleting the key cuts them off. They are in no game until someone invites
// them to one.
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
	user, err := q.CreateUser(ctx, dbgen.CreateUserParams{ID: xid.New().String(), Name: name})
	if err != nil {
		return err
	}
	// The key only ever acts through the connector it approves, so it is an
	// agent's.
	row, err := q.CreateAPIKey(ctx, dbgen.CreateAPIKeyParams{ID: xid.New().String(), Name: name, KeyHash: auth.Hash(key), Kind: auth.KindAgent, UserID: &user.ID})
	if err != nil {
		return err
	}
	if err := q.SetInviteKey(ctx, dbgen.SetInviteKeyParams{ID: inv.ID, ApiKeyID: &row.ID, UserID: &user.ID}); err != nil {
		return err
	}
	if err := approve(q, row.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// InTx runs fn on one transaction, committing only if it returns nil. If
// ctx carries a Guard (WithGuard), it is checked first: see Guard.
func (s *Store) InTx(ctx context.Context, fn func(q *dbgen.Queries) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // a no-op after Commit
	q := s.WithTx(tx)
	// A request that carries a Guard checks it first, so the row stays
	// locked, and as the caller saw it, until fn's write commits.
	if err := checkGuard(ctx, q); err != nil {
		return err
	}
	if err := fn(q); err != nil {
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
		AuthorKey:  auth.KeyIDPtr(ctx),
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

// Join is where a game invite's link goes.
func (l Links) Join(code string) string { return l.Base + "/join/" + code }

// Members is a game's page of members and invites.
func (l Links) Members(gameID string) string { return l.Game(gameID) + "/members" }

// Game is a game's page.
func (l Links) Game(id string) string { return l.Base + "/games/" + id }

// Target is the page of what a note or build issue in a game is about:
// a character, conversation, node, choice, flag, skill or quest, or the
// game itself.
func (l Links) Target(gameID, kind, id string) string {
	switch kind {
	case "character":
		return l.Character(id)
	case "conversation":
		return l.Conversation(id)
	case "node":
		return l.NodeByID(id)
	case "choice":
		return l.ChoiceByID(id)
	case "flag":
		return l.World(gameID) + "#flag-" + id
	case "skill":
		return l.World(gameID) + "#skill-" + id
	case "quest":
		return l.Quest(id)
	case "quest_stage":
		return l.Base + "/stages/" + id
	default:
		return l.Game(gameID)
	}
}

// World is a game's page of flags, skills and quests.
func (l Links) World(gameID string) string { return l.Game(gameID) + "/world" }

// Quest is a quest's page.
func (l Links) Quest(id string) string { return l.Base + "/quests/" + id }

// Notes is a game's page of notes.
func (l Links) Notes(gameID string) string { return l.Game(gameID) + "/notes" }

// Build is a game's build report.
func (l Links) Build(gameID string) string { return l.Game(gameID) + "/build" }

// Roles a member may have in a game.
const (
	// RoleOwner may change or delete the game and invite people, and write.
	RoleOwner = "owner"
	// RoleWriter may write the game's content and leave notes.
	RoleWriter = "writer"
)

// AccessStore is what finding a caller's way into a game reads.
type AccessStore interface {
	GameAccess(ctx context.Context, arg dbgen.GameAccessParams) (dbgen.GameAccessRow, error)
}

// Membership is the game something belongs to and the caller's role in it.
type Membership struct {
	GameID string
	Role   string
}

// Access finds the game that kind/id belongs to ("character", "node",
// "quest_stage", "invite", "game", ...) and the caller's role in it. A
// thing in a game the caller's user is not a member of is ErrNotFound,
// exactly like a thing that does not exist, so a game's existence does not
// leak. Every endpoint calls it (or Game) before touching anything, which
// is what keeps a game private to its members on every surface. A command
// run on the server (auth.System) reaches every game as its owner.
func Access(ctx context.Context, s AccessStore, kind, id string) (Membership, error) {
	c := auth.CallerFrom(ctx)
	row, err := s.GameAccess(ctx, dbgen.GameAccessParams{UserID: c.UserID, Kind: kind, TargetID: id})
	if err != nil {
		return Membership{}, NotFound(err, strings.ReplaceAll(kind, "_", " "), id)
	}
	if c.System {
		return Membership{GameID: row.GameID, Role: RoleOwner}, nil
	}
	if c.UserID == "" || row.Role == "" {
		return Membership{}, fmt.Errorf("%w: %s %s", ErrNotFound, strings.ReplaceAll(kind, "_", " "), id)
	}
	return Membership{GameID: row.GameID, Role: row.Role}, nil
}

// RequireOwner is Access to a game, refused unless the caller owns it.
func RequireOwner(ctx context.Context, s AccessStore, gameID, doing string) (Membership, error) {
	m, err := Access(ctx, s, "game", gameID)
	if err != nil {
		return m, err
	}
	if m.Role != RoleOwner {
		return m, fmt.Errorf("%w: only the game's owner may %s", ErrForbidden, doing)
	}
	return m, nil
}

// GameStore is what checking a caller may reach a game reads.
type GameStore interface {
	AccessStore
	GetGame(ctx context.Context, id string) (dbgen.Game, error)
}

// Game is a live game the caller is a member of, or ErrNotFound. Endpoints
// that list or create in a game call it first, so a game the caller cannot
// see is a 404 rather than an empty list.
func Game(ctx context.Context, s GameStore, id string) (dbgen.Game, error) {
	if _, err := Access(ctx, s, "game", id); err != nil {
		return dbgen.Game{}, err
	}
	g, err := s.GetGame(ctx, id)
	return g, NotFound(err, "game", id)
}

// SameGame refuses to connect things in different games: a speaker, a
// check's skill, a note's target.
func SameGame(what, gameID, want string) error {
	if gameID != want {
		return fmt.Errorf("%w: %s is in another game", ErrBadInput, what)
	}
	return nil
}

// NotFound turns pgx's no-rows into ErrNotFound, naming what was missing.
func NotFound(err error, what, id string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s %s", ErrNotFound, what, id)
	}
	return err
}
