// Command game plays the bundle that `server build` writes. It is the game
// itself, separate from the authoring tool: it reads content.json once at
// startup and keeps saved games in a Postgres database of its own. See
// docs/mechanics.md.
//
//	game [serve]    serve the game (CONTENT, DATABASE_URL, PORT, TITLE)
//
// It has no accounts: everyone who can reach it shares one list of saved
// games. Run it locally, or put it behind something that authenticates.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KevinMcHugh/rpg/internal/content"
	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
	"github.com/KevinMcHugh/rpg/internal/game"
	"github.com/KevinMcHugh/rpg/internal/game/play"
)

func main() {
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "", "serve":
		err = serve()
	default:
		err = fmt.Errorf("unknown command %q; use serve", cmd)
	}
	if err != nil {
		slog.Error("game", "err", err)
		os.Exit(1)
	}
}

func serve() error {
	path := envOr("CONTENT", "content.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read the bundle (make bundle writes it): %w", err)
	}
	var bundle content.Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	g := game.New(bundle)

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required: the game keeps saves in Postgres")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}

	h := &play.Handler{
		Title: envOr("TITLE", "RPG"),
		Store: dbgen.New(pool),
		Owner: func(context.Context) *string { return nil },
		Load: func(context.Context, string) (*game.Game, string, error) {
			return g, "", nil
		},
	}
	r := chi.NewRouter()
	r.Use(chimw.Logger, chimw.Recoverer)
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	r.Group(func(r chi.Router) {
		r.Use(play.Headers)
		h.Routes(r)
	})

	port := envOr("PORT", "8090")
	slog.Info("game listening", "port", port, "content", path, "conversations", len(bundle.Conversations))
	hs := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	return hs.ListenAndServe()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
