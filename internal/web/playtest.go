package web

import (
	"context"
	"errors"
	"fmt"

	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/auth"
	"github.com/KevinMcHugh/rpg/internal/content"
	"github.com/KevinMcHugh/rpg/internal/game"
	"github.com/KevinMcHugh/rpg/internal/game/play"
	"github.com/KevinMcHugh/rpg/internal/script"
)

// ---- playtests --------------------------------------------------------------

// playtest is the game, playing the content as it stands. It compiles the
// content on every request, so an edit shows on the next click; build
// errors are shown above the game rather than stopping it.
func (s *Server) playtest() *play.Handler {
	return &play.Handler{
		Title: "Playtest",
		Base:  "/play",
		Back:  "/",
		Store: s.Store,
		Owner: auth.KeyIDPtr,
		Load: func(ctx context.Context) (*game.Game, string, error) {
			c, err := content.Load(ctx, s.Store)
			if err != nil {
				return nil, "", err
			}
			bundle, report := content.Compile(c)
			notice := ""
			if n := len(report.Errors); n > 0 {
				notice = fmt.Sprintf("The content has build errors (%d), so some of it may not play as written. See the Build page.", n)
			}
			return game.New(bundle), notice, nil
		},
		Setup: func(ctx context.Context, text string) ([]script.Effect, error) {
			names, err := app.LoadNames(ctx, s.Store)
			if err != nil {
				return nil, err
			}
			effects, err := script.ParseEffects(text, names)
			if err != nil {
				return nil, errors.New("setup: " + err.Error())
			}
			return effects, nil
		},
	}
}
