package app

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
)

// The Page methods are the store's paged lists: each takes what the list is
// filtered by and the page the caller asked for, and returns that page. The
// keyset query (…After), its sort key and app.Paginate stay here, so a list
// endpoint depends only on "this page of this list" through its narrow
// Store interface, and a fake store can return a page outright.

// Pager is every paged list the store holds. server.Store includes it,
// and a list endpoint's own Store interface names the one it reads.
type Pager interface {
	PageGamesForUser(ctx context.Context, userID string, p PageRequest) (Page[dbgen.GamesForUserAfterRow], error)
	PageGameMembers(ctx context.Context, gameID string, p PageRequest) (Page[dbgen.GameMembersAfterRow], error)
	PageGameInvites(ctx context.Context, gameID string, p PageRequest) (Page[dbgen.GameInvite], error)
	PageCharacters(ctx context.Context, gameID string, p PageRequest) (Page[dbgen.Character], error)
	PageConversations(ctx context.Context, gameID string, p PageRequest) (Page[dbgen.Conversation], error)
	PageFlags(ctx context.Context, gameID string, p PageRequest) (Page[dbgen.Flag], error)
	PageSkills(ctx context.Context, gameID string, p PageRequest) (Page[dbgen.Skill], error)
	PageQuests(ctx context.Context, gameID string, p PageRequest) (Page[dbgen.Quest], error)
	PageNotes(ctx context.Context, f NoteFilter, p PageRequest) (Page[dbgen.NotesAfterRow], error)
}

var _ Pager = (*Store)(nil)

// Sort keys: where a page starts, as the cursor holds it.
type (
	byName struct{ Name, ID string }
	byKey  struct{ Key, ID string }
	byTime struct {
		At time.Time
		ID string
	}
	memberKey struct {
		NotOwner bool
		JoinedAt time.Time
		UserID   string
	}
)

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// PageGamesForUser is a page of the games userID is a member of, oldest
// first, with their role in each.
func (s *Store) PageGamesForUser(ctx context.Context, userID string, p PageRequest) (Page[dbgen.GamesForUserAfterRow], error) {
	return Paginate(p,
		func(after *byTime, limit int32) ([]dbgen.GamesForUserAfterRow, error) {
			arg := dbgen.GamesForUserAfterParams{UserID: userID, Lim: limit}
			if after != nil {
				arg.AfterCreatedAt, arg.AfterID = ts(after.At), &after.ID
			}
			return s.GamesForUserAfter(ctx, arg)
		},
		func(r dbgen.GamesForUserAfterRow) byTime { return byTime{r.CreatedAt.Time, r.ID} })
}

// PageGameMembers is a page of a game's members, the owner first, then by
// when they joined.
func (s *Store) PageGameMembers(ctx context.Context, gameID string, p PageRequest) (Page[dbgen.GameMembersAfterRow], error) {
	return Paginate(p,
		func(after *memberKey, limit int32) ([]dbgen.GameMembersAfterRow, error) {
			arg := dbgen.GameMembersAfterParams{GameID: gameID, Lim: limit}
			if after != nil {
				arg.AfterNotOwner, arg.AfterCreatedAt, arg.AfterUserID = &after.NotOwner, ts(after.JoinedAt), &after.UserID
			}
			return s.GameMembersAfter(ctx, arg)
		},
		func(r dbgen.GameMembersAfterRow) memberKey {
			return memberKey{r.Role != RoleOwner, r.CreatedAt.Time, r.UserID}
		})
}

// PageGameInvites is a page of a game's live invites, newest first.
func (s *Store) PageGameInvites(ctx context.Context, gameID string, p PageRequest) (Page[dbgen.GameInvite], error) {
	return Paginate(p,
		func(after *byTime, limit int32) ([]dbgen.GameInvite, error) {
			arg := dbgen.GameInvitesAfterParams{GameID: gameID, Lim: limit}
			if after != nil {
				arg.AfterCreatedAt, arg.AfterID = ts(after.At), &after.ID
			}
			return s.GameInvitesAfter(ctx, arg)
		},
		func(r dbgen.GameInvite) byTime { return byTime{r.CreatedAt.Time, r.ID} })
}

// PageCharacters is a page of a game's characters, by name.
func (s *Store) PageCharacters(ctx context.Context, gameID string, p PageRequest) (Page[dbgen.Character], error) {
	return Paginate(p,
		func(after *byName, limit int32) ([]dbgen.Character, error) {
			arg := dbgen.CharactersAfterParams{GameID: gameID, Lim: limit}
			if after != nil {
				arg.AfterName, arg.AfterID = &after.Name, &after.ID
			}
			return s.CharactersAfter(ctx, arg)
		},
		func(c dbgen.Character) byName { return byName{c.Name, c.ID} })
}

// PageConversations is a page of a game's conversations, newest first.
func (s *Store) PageConversations(ctx context.Context, gameID string, p PageRequest) (Page[dbgen.Conversation], error) {
	return Paginate(p,
		func(after *byTime, limit int32) ([]dbgen.Conversation, error) {
			arg := dbgen.ConversationsAfterParams{GameID: gameID, Lim: limit}
			if after != nil {
				arg.AfterCreatedAt, arg.AfterID = ts(after.At), &after.ID
			}
			return s.ConversationsAfter(ctx, arg)
		},
		func(c dbgen.Conversation) byTime { return byTime{c.CreatedAt.Time, c.ID} })
}

// PageFlags is a page of a game's flags, by key.
func (s *Store) PageFlags(ctx context.Context, gameID string, p PageRequest) (Page[dbgen.Flag], error) {
	return Paginate(p,
		func(after *byKey, limit int32) ([]dbgen.Flag, error) {
			arg := dbgen.FlagsAfterParams{GameID: gameID, Lim: limit}
			if after != nil {
				arg.AfterKey, arg.AfterID = &after.Key, &after.ID
			}
			return s.FlagsAfter(ctx, arg)
		},
		func(f dbgen.Flag) byKey { return byKey{f.Key, f.ID} })
}

// PageSkills is a page of a game's skills, by name.
func (s *Store) PageSkills(ctx context.Context, gameID string, p PageRequest) (Page[dbgen.Skill], error) {
	return Paginate(p,
		func(after *byName, limit int32) ([]dbgen.Skill, error) {
			arg := dbgen.SkillsAfterParams{GameID: gameID, Lim: limit}
			if after != nil {
				arg.AfterName, arg.AfterID = &after.Name, &after.ID
			}
			return s.SkillsAfter(ctx, arg)
		},
		func(k dbgen.Skill) byName { return byName{k.Name, k.ID} })
}

// PageQuests is a page of a game's quests, by name, without their stages.
func (s *Store) PageQuests(ctx context.Context, gameID string, p PageRequest) (Page[dbgen.Quest], error) {
	return Paginate(p,
		func(after *byName, limit int32) ([]dbgen.Quest, error) {
			arg := dbgen.QuestsAfterParams{GameID: gameID, Lim: limit}
			if after != nil {
				arg.AfterName, arg.AfterID = &after.Name, &after.ID
			}
			return s.QuestsAfter(ctx, arg)
		},
		func(q dbgen.Quest) byName { return byName{q.Name, q.ID} })
}

// NoteFilter narrows a game's notes; an empty field matches every note.
type NoteFilter struct {
	GameID     string
	Status     string
	TargetKind string
	TargetID   string
}

// PageNotes is a page of a game's notes matching f, oldest first, with
// their authors' names.
func (s *Store) PageNotes(ctx context.Context, f NoteFilter, p PageRequest) (Page[dbgen.NotesAfterRow], error) {
	return Paginate(p,
		func(after *byTime, limit int32) ([]dbgen.NotesAfterRow, error) {
			arg := dbgen.NotesAfterParams{
				GameID: f.GameID, Status: orNil(f.Status), TargetKind: orNil(f.TargetKind), TargetID: orNil(f.TargetID), Lim: limit,
			}
			if after != nil {
				arg.AfterCreatedAt, arg.AfterID = ts(after.At), &after.ID
			}
			return s.NotesAfter(ctx, arg)
		},
		func(r dbgen.NotesAfterRow) byTime { return byTime{r.Note.CreatedAt.Time, r.Note.ID} })
}

// orNil is s, or nil for "" (a filter that matches everything).
func orNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
