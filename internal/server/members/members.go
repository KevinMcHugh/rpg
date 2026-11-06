// Package members holds the endpoints for who may work on a game: its
// members (one owner, and writers) and the single-use links its owner makes
// to bring someone in. A link is redeemed on the join page (package web),
// never through the API or MCP: redeeming one needs a person signing in.
package members

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/xid"

	apigen "github.com/KevinMcHugh/rpg/internal/api/gen"
	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/auth"
	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
)

// Invite lifetimes, in hours.
const (
	DefaultInviteHours = 7 * 24
	MaxInviteHours     = 30 * 24
	MaxNote            = 200
)

// -----------------------------------------------------------------------------
// Members
// -----------------------------------------------------------------------------

// Member is someone in a game.
type Member struct {
	UserID   string    `json:"userId"`
	Name     string    `json:"name"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joinedAt"`
}

type ListStore interface {
	app.AccessStore
	PageGameMembers(ctx context.Context, arg dbgen.PageGameMembersParams) ([]dbgen.PageGameMembersRow, error)
}

// ListEndpoint is a page of a game's members, owner first, then by when
// they joined. Any member may see them.
type ListEndpoint struct{ Store ListStore }

// Page is the page the caller asked for.
func (ListEndpoint) Page(req apigen.ListMembersRequestObject) app.PageRequest {
	return app.PageRequest{List: "member", Limit: req.Params.Limit, After: req.Params.After}
}

// memberKey is where a page of members starts: the owner sorts first.
type memberKey struct {
	NotOwner bool
	JoinedAt time.Time
	UserID   string
}

func (e ListEndpoint) Interact(ctx context.Context, req apigen.ListMembersRequestObject) (app.Page[dbgen.PageGameMembersRow], error) {
	if _, err := app.Access(ctx, e.Store, "game", req.GameId); err != nil {
		return app.Page[dbgen.PageGameMembersRow]{}, err
	}
	return app.Paginate(e.Page(req),
		func(after *memberKey, limit int32) ([]dbgen.PageGameMembersRow, error) {
			arg := dbgen.PageGameMembersParams{GameID: req.GameId, Lim: limit}
			if after != nil {
				arg.AfterNotOwner, arg.AfterCreatedAt, arg.AfterUserID = &after.NotOwner, pgtype.Timestamptz{Time: after.JoinedAt, Valid: true}, &after.UserID
			}
			return e.Store.PageGameMembers(ctx, arg)
		},
		func(r dbgen.PageGameMembersRow) memberKey {
			return memberKey{r.Role != app.RoleOwner, r.CreatedAt.Time, r.UserID}
		})
}

func (ListEndpoint) Build(p app.Page[dbgen.PageGameMembersRow]) app.Page[Member] {
	return app.MapPage(p, func(r dbgen.PageGameMembersRow) Member {
		return Member{UserID: r.UserID, Name: r.Name, Role: r.Role, JoinedAt: r.CreatedAt.Time}
	})
}

func (ListEndpoint) Render(p app.Page[Member]) apigen.ListMembersResponseObject {
	return apigen.ListMembers200JSONResponse{Next: p.Next, Members: app.MapPage(p, func(m Member) apigen.Member {
		return apigen.Member{UserId: m.UserID, Name: m.Name, Role: apigen.Role(m.Role), JoinedAt: m.JoinedAt}
	}).Items}
}

type RemoveStore interface {
	app.AccessStore
	RemoveGameMember(ctx context.Context, arg dbgen.RemoveGameMemberParams) (int64, error)
}

// RemoveEndpoint takes a writer out of a game: the owner may remove anyone
// but themselves (a game always has its owner), and a writer may remove
// themselves, which is leaving. People only.
type RemoveEndpoint struct{ Store RemoveStore }

func (e RemoveEndpoint) Interact(ctx context.Context, req apigen.RemoveMemberRequestObject) (bool, error) {
	m, err := app.Access(ctx, e.Store, "game", req.GameId)
	if err != nil {
		return false, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return false, err
	}
	self := req.UserId == auth.UserID(ctx)
	switch {
	case self && m.Role == app.RoleOwner:
		return false, fmt.Errorf("%w: the owner cannot leave their own game; delete it instead", app.ErrBadInput)
	case !self && m.Role != app.RoleOwner:
		return false, fmt.Errorf("%w: only the game's owner may remove someone else", app.ErrForbidden)
	}
	n, err := e.Store.RemoveGameMember(ctx, dbgen.RemoveGameMemberParams{GameID: req.GameId, UserID: req.UserId})
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, fmt.Errorf("%w: member %s", app.ErrNotFound, req.UserId)
	}
	return true, nil
}

func (RemoveEndpoint) Build(ok bool) bool { return ok }

func (RemoveEndpoint) Render(bool) apigen.RemoveMemberResponseObject {
	return apigen.RemoveMember204Response{}
}

// -----------------------------------------------------------------------------
// Invites
// -----------------------------------------------------------------------------

// Invite is a link into a game. URL is set only when it was just made: the
// code is never stored, so the link cannot be shown again.
type Invite struct {
	ID        string    `json:"id"`
	GameID    string    `json:"gameId"`
	Note      string    `json:"note"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
	URL       string    `json:"url,omitempty"`
}

func toView(r dbgen.GameInvite, url string) Invite {
	return Invite{ID: r.ID, GameID: r.GameID, Note: r.Note, ExpiresAt: r.ExpiresAt.Time, CreatedAt: r.CreatedAt.Time, URL: url}
}

func toAPI(i Invite) apigen.Invite {
	return apigen.Invite{Id: i.ID, GameId: i.GameID, Note: i.Note, ExpiresAt: i.ExpiresAt, CreatedAt: i.CreatedAt, Url: i.URL}
}

type ListInvitesStore interface {
	app.AccessStore
	PageGameInvites(ctx context.Context, arg dbgen.PageGameInvitesParams) ([]dbgen.GameInvite, error)
}

// ListInvitesEndpoint is a page of a game's links that still work, newest
// first. Its owner only.
type ListInvitesEndpoint struct{ Store ListInvitesStore }

// Page is the page the caller asked for.
func (ListInvitesEndpoint) Page(req apigen.ListInvitesRequestObject) app.PageRequest {
	return app.PageRequest{List: "invite", Limit: req.Params.Limit, After: req.Params.After}
}

// newest is where a page of a list sorted newest first starts.
type newest struct {
	CreatedAt time.Time
	ID        string
}

func (e ListInvitesEndpoint) Interact(ctx context.Context, req apigen.ListInvitesRequestObject) (app.Page[dbgen.GameInvite], error) {
	if _, err := app.RequireOwner(ctx, e.Store, req.GameId, "see its invites"); err != nil {
		return app.Page[dbgen.GameInvite]{}, err
	}
	return app.Paginate(e.Page(req),
		func(after *newest, limit int32) ([]dbgen.GameInvite, error) {
			arg := dbgen.PageGameInvitesParams{GameID: req.GameId, Lim: limit}
			if after != nil {
				arg.AfterCreatedAt, arg.AfterID = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}, &after.ID
			}
			return e.Store.PageGameInvites(ctx, arg)
		},
		func(r dbgen.GameInvite) newest { return newest{r.CreatedAt.Time, r.ID} })
}

func (ListInvitesEndpoint) Build(p app.Page[dbgen.GameInvite]) app.Page[Invite] {
	return app.MapPage(p, func(r dbgen.GameInvite) Invite { return toView(r, "") })
}

func (ListInvitesEndpoint) Render(p app.Page[Invite]) apigen.ListInvitesResponseObject {
	return apigen.ListInvites200JSONResponse{Invites: app.MapPage(p, toAPI).Items, Next: p.Next}
}

type CreateInviteStore interface {
	app.AccessStore
	CreateGameInvite(ctx context.Context, arg dbgen.CreateGameInviteParams) (dbgen.GameInvite, error)
}

// CreateInviteEndpoint makes a single-use link into a game, for a writer.
// Its owner only, and a person: an agent cannot bring anyone in.
type CreateInviteEndpoint struct {
	Store CreateInviteStore
	Links app.Links
}

type created struct {
	row  dbgen.GameInvite
	code string
}

func (e CreateInviteEndpoint) Interact(ctx context.Context, req apigen.CreateInviteRequestObject) (created, error) {
	if _, err := app.RequireOwner(ctx, e.Store, req.GameId, "invite people"); err != nil {
		return created{}, err
	}
	if err := app.RequireHuman(ctx); err != nil {
		return created{}, err
	}
	note := strings.TrimSpace(req.Body.Note)
	if n := utf8.RuneCountInString(note); n > MaxNote {
		return created{}, fmt.Errorf("%w: note is %d characters; the limit is %d", app.ErrBadInput, n, MaxNote)
	}
	hours := req.Body.ExpiresInHours
	if hours == 0 {
		hours = DefaultInviteHours
	}
	if hours < 1 || hours > MaxInviteHours {
		return created{}, fmt.Errorf("%w: expiresInHours must be from 1 to %d", app.ErrBadInput, MaxInviteHours)
	}
	code, err := auth.RandToken(auth.GameInvitePrefix)
	if err != nil {
		return created{}, err
	}
	row, err := e.Store.CreateGameInvite(ctx, dbgen.CreateGameInviteParams{
		ID:        xid.New().String(),
		GameID:    req.GameId,
		CodeHash:  auth.Hash(code),
		Note:      note,
		CreatedBy: auth.UserID(ctx),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Duration(hours) * time.Hour), Valid: true},
	})
	return created{row: row, code: code}, err
}

func (e CreateInviteEndpoint) Build(c created) Invite { return toView(c.row, e.Links.Join(c.code)) }

func (CreateInviteEndpoint) Render(vm Invite) apigen.CreateInviteResponseObject {
	return apigen.CreateInvite201JSONResponse(toAPI(vm))
}

type DeleteInviteStore interface {
	app.AccessStore
	DeleteGameInvite(ctx context.Context, id string) (int64, error)
}

// DeleteInviteEndpoint revokes a link that has not been used. Its game's
// owner only.
type DeleteInviteEndpoint struct{ Store DeleteInviteStore }

func (e DeleteInviteEndpoint) Interact(ctx context.Context, req apigen.DeleteInviteRequestObject) (bool, error) {
	m, err := app.Access(ctx, e.Store, "invite", req.InviteId)
	if err != nil {
		return false, err
	}
	if m.Role != app.RoleOwner {
		return false, fmt.Errorf("%w: only the game's owner may revoke its invites", app.ErrForbidden)
	}
	if err := app.RequireHuman(ctx); err != nil {
		return false, err
	}
	n, err := e.Store.DeleteGameInvite(ctx, req.InviteId)
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, fmt.Errorf("%w: invite %s was already used or revoked", app.ErrNotFound, req.InviteId)
	}
	return true, nil
}

func (DeleteInviteEndpoint) Build(ok bool) bool { return ok }

func (DeleteInviteEndpoint) Render(bool) apigen.DeleteInviteResponseObject {
	return apigen.DeleteInvite204Response{}
}

// -----------------------------------------------------------------------------
// Redeeming
// -----------------------------------------------------------------------------

// ErrInviteInvalid is a link that is unknown, used, revoked or expired, or
// whose game was deleted.
var ErrInviteInvalid = errors.New("this invite link has been used, revoked or has expired")

// Redeem spends a game invite for userID, making them a writer in its game,
// and returns the game's id. It runs on q, so a caller can do it in the
// same transaction that signs the person up. Someone already in the game
// keeps their role, and the link stays unspent for whoever it was meant for.
func Redeem(ctx context.Context, q *dbgen.Queries, code, userID string) (string, error) {
	inv, err := q.GetGameInviteByHash(ctx, auth.Hash(code))
	if err != nil {
		return "", ErrInviteInvalid
	}
	if row, err := q.GameAccess(ctx, dbgen.GameAccessParams{UserID: userID, Kind: "game", TargetID: inv.GameID}); err == nil && row.Role != "" {
		return inv.GameID, nil
	}
	claimed, err := q.ClaimGameInvite(ctx, dbgen.ClaimGameInviteParams{CodeHash: auth.Hash(code), UsedBy: &userID})
	if err != nil {
		return "", ErrInviteInvalid
	}
	if err := q.AddGameMember(ctx, dbgen.AddGameMemberParams{GameID: claimed.GameID, UserID: userID, Role: app.RoleWriter}); err != nil {
		return "", err
	}
	return claimed.GameID, nil
}
