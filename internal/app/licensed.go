package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/rs/xid"

	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
	"github.com/KevinMcHugh/rpg/internal/rules"
)

// Licensed is licensed text to import into a game: where it comes from,
// the notice its license requires the game to show, and what it holds. The
// person running `server import` supplies it, from the source itself; see
// docs/authorship.md.
type Licensed struct {
	Source string `json:"source"` // "SRD 5.2"
	Notice string `json:"notice"` // the attribution the license requires, word for word
	Skills []struct {
		Key     string `json:"key"`
		Name    string `json:"name"`
		Ability string `json:"ability"`
	} `json:"skills"`
}

// Import adds licensed text to a game in one transaction. Each skill whose
// key the game lacks is created, its name recorded as licensed text (its
// source and notice, not a key) rather than as anyone's writing; skills
// the game already has are left alone. The build then puts the notice in
// the game's credits. There is no API for this: it is for whoever runs the
// server.
func (s *Store) Import(ctx context.Context, gameID string, in Licensed) (added, kept int, err error) {
	in.Source, in.Notice = strings.TrimSpace(in.Source), strings.TrimSpace(in.Notice)
	if in.Source == "" || in.Notice == "" {
		return 0, 0, fmt.Errorf("%w: licensed text needs its source and the notice its license requires", ErrBadInput)
	}
	err = s.InTx(ctx, func(q *dbgen.Queries) error {
		for _, sk := range in.Skills {
			name := strings.TrimSpace(sk.Name)
			if name == "" {
				return fmt.Errorf("%w: a skill has no name", ErrBadInput)
			}
			key, err := CleanKey(sk.Key, name)
			if err != nil {
				return fmt.Errorf("skill %q: %w", name, err)
			}
			var ability *string
			if a := strings.TrimSpace(sk.Ability); a != "" {
				if !slices.Contains(rules.Abilities, a) {
					return fmt.Errorf("%w: skill %q: ability %q is not one of %v", ErrBadInput, name, a, rules.Abilities)
				}
				ability = &a
			}
			if _, err := q.GetSkillByKey(ctx, dbgen.GetSkillByKeyParams{GameID: gameID, Key: key}); err == nil {
				kept++
				continue
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			id := xid.New().String()
			rev, err := q.CreateLicensedRevision(ctx, dbgen.CreateLicensedRevisionParams{
				ID: xid.New().String(), TargetKind: "skill", TargetID: id, Field: "name", Body: name,
				Source: &in.Source, LicenseNotice: &in.Notice,
			})
			if err != nil {
				return err
			}
			if _, err := q.CreateSkill(ctx, dbgen.CreateSkillParams{
				ID: id, GameID: gameID, Key: key, Name: name, NameRevisionID: rev.ID, Ability: ability,
			}); err != nil {
				return fmt.Errorf("skill %q: %w", name, err)
			}
			added++
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return added, kept, nil
}
