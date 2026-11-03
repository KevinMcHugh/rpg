package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
	"github.com/KevinMcHugh/rpg/internal/script"
)

// NamesStore is what LoadNames reads.
type NamesStore interface {
	ListFlags(ctx context.Context) ([]dbgen.Flag, error)
	ListSkills(ctx context.Context) ([]dbgen.Skill, error)
	ListCharacters(ctx context.Context) ([]dbgen.Character, error)
	ListQuests(ctx context.Context) ([]dbgen.Quest, error)
	ListConversations(ctx context.Context) ([]dbgen.Conversation, error)
}

// LoadNames is every key a script can use, for parsing what an author typed
// and formatting what is stored.
func LoadNames(ctx context.Context, s NamesStore) (*script.Names, error) {
	n := script.NewNames()
	flags, err := s.ListFlags(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range flags {
		n.Add(script.KindFlag, f.ID, f.Key)
	}
	skills, err := s.ListSkills(ctx)
	if err != nil {
		return nil, err
	}
	for _, sk := range skills {
		n.Add(script.KindSkill, sk.ID, sk.Key)
	}
	chars, err := s.ListCharacters(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range chars {
		n.Add(script.KindCharacter, c.ID, c.Key)
	}
	quests, err := s.ListQuests(ctx)
	if err != nil {
		return nil, err
	}
	for _, q := range quests {
		n.Add(script.KindQuest, q.ID, q.Key)
	}
	convs, err := s.ListConversations(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range convs {
		n.Add(script.KindConversation, c.ID, c.Key)
	}
	return n, nil
}

// ParseCond parses a condition an author typed into its stored JSON; empty
// text is no condition (nil).
func ParseCond(field, text string, names *script.Names) ([]byte, error) {
	c, err := script.ParseCond(text, names)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrBadInput, field, err)
	}
	if c == nil {
		return nil, nil
	}
	return json.Marshal(c)
}

// ParseEffects parses effects an author typed into their stored JSON.
func ParseEffects(field, text string, names *script.Names) ([]byte, error) {
	es, err := script.ParseEffects(text, names)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrBadInput, field, err)
	}
	return json.Marshal(es)
}

// DecodeCond is a stored condition; nil when there is none.
func DecodeCond(raw []byte) *script.Cond {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var c script.Cond
	if json.Unmarshal(raw, &c) != nil {
		return nil
	}
	return &c
}

// DecodeEffects is stored effects.
func DecodeEffects(raw []byte) []script.Effect {
	var es []script.Effect
	_ = json.Unmarshal(raw, &es)
	return es
}

// FormatCond is a stored condition as an author would type it.
func FormatCond(raw []byte, names *script.Names) string {
	return script.FormatCond(DecodeCond(raw), names)
}

// FormatEffects is stored effects as an author would type them.
func FormatEffects(raw []byte, names *script.Names) string {
	return script.FormatEffects(DecodeEffects(raw), names)
}

// CleanKey checks a key an author gave, or makes one from name when they
// gave none. With no name (an update), a key is required.
func CleanKey(key, name string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" && strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("%w: key is required", ErrBadInput)
	}
	if key == "" {
		key = script.Slug(name)
	}
	if err := script.CheckKey(key); err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadInput, err)
	}
	return key, nil
}

// KeyTaken turns a unique-index violation on a key into ErrBadInput.
func KeyTaken(err error, kind, key string) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return fmt.Errorf("%w: another %s already has the key %q", ErrBadInput, kind, key)
	}
	return err
}
