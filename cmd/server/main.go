// Command server runs the app: web pages, a JSON API and MCP in one
// process, plus the admin commands that mint credentials. See README.md.
//
//	server [serve]                    run the server (DATABASE_URL, PORT, PUBLIC_URL)
//	server keys create --name NAME [--human]
//	                                  mint an api key (printed once); only a
//	                                  --human key may write the game's text
//	server build [--out FILE]         check the content and write the bundle
//	                                  the game runs on (stdout by default)
//	server keys list                  list live api keys
//	server keys rotate --id ID        replace a key, keeping its name
//	server keys delete --id ID        revoke a key and every token it approved
//	server invites create [--note N] [--expires-in D]
//	                                  mint a single-use invite (printed once)
//	server invites list               list unused, unexpired invites
//	server invites delete --id ID     withdraw an unused invite
//
// Credentials are minted here and nowhere else: there is no API or MCP tool
// for it, so whoever can run this binary against the database is the admin.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/xid"

	"github.com/KevinMcHugh/rpg/internal/app"
	"github.com/KevinMcHugh/rpg/internal/auth"
	"github.com/KevinMcHugh/rpg/internal/content"
	dbgen "github.com/KevinMcHugh/rpg/internal/db/gen"
	"github.com/KevinMcHugh/rpg/internal/web"
)

func main() {
	var err error
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "", "serve":
		err = serve()
	case "keys":
		err = keys(args)
	case "invites":
		err = invites(args)
	case "build":
		err = buildContent(args)
	default:
		err = fmt.Errorf("unknown command %q; use serve, keys, invites or build", cmd)
	}
	if err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

func serve() error {
	ctx := context.Background()
	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	issuer := strings.TrimRight(os.Getenv("PUBLIC_URL"), "/")
	port := envOr("PORT", "8080")
	if issuer == "" {
		issuer = "http://localhost:" + port
	}
	srv := web.New(app.NewStore(pool), issuer)
	slog.Info("listening", "port", port, "public_url", issuer)
	hs := &http.Server{Addr: ":" + port, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	return hs.ListenAndServe()
}

func keys(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: keys <create|list|rotate|delete> ...")
	}
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("keys "+sub, flag.ExitOnError)
	name := fs.String("name", "", "label for the key (create)")
	id := fs.String("id", "", "key id (rotate, delete)")
	human := fs.Bool("human", false, "a person's key, which may write the game's text (create)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	q := dbgen.New(pool)

	switch sub {
	case "create":
		if *name == "" {
			return errors.New("--name is required")
		}
		kind := auth.KindAgent
		if *human {
			kind = auth.KindHuman
		}
		return mint(ctx, q, *name, kind)
	case "list":
		rows, err := q.ListAPIKeys(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tKIND\tCREATED\tLAST USED")
		for _, r := range rows {
			last := "-"
			if r.LastUsedAt.Valid {
				last = r.LastUsedAt.Time.UTC().Format(time.DateTime)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.ID, r.Name, r.Kind, r.CreatedAt.Time.UTC().Format(time.DateTime), last)
		}
		return tw.Flush()
	case "rotate", "delete":
		if *id == "" {
			return errors.New("--id is required")
		}
		old, err := q.GetAPIKey(ctx, *id)
		if err != nil {
			return fmt.Errorf("look up key %s: %w", *id, err)
		}
		// Deleting the key also cuts off its OAuth tokens and web sessions:
		// their lookups join on a live key.
		if err := q.DeleteAPIKey(ctx, old.ID); err != nil {
			return err
		}
		if sub == "delete" {
			fmt.Printf("deleted key %s (%s)\n", old.ID, old.Name)
			return nil
		}
		fmt.Printf("rotated key %s (%s)\n", old.ID, old.Name)
		return mint(ctx, q, old.Name, old.Kind)
	default:
		return fmt.Errorf("unknown keys subcommand %q", sub)
	}
}

// invites manages the single-use codes a newcomer redeems on the OAuth
// authorize page in place of an api key. Redeeming one mints their key.
func invites(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: invites <create|list|delete> ...")
	}
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("invites "+sub, flag.ExitOnError)
	note := fs.String("note", "", "who it is for; also the name of the key it mints (create)")
	expiresIn := fs.Duration("expires-in", 0, "how long it stays redeemable, e.g. 168h; default never (create)")
	id := fs.String("id", "", "invite id (delete)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	q := dbgen.New(pool)

	switch sub {
	case "create":
		raw, err := auth.RandToken(auth.InvitePrefix)
		if err != nil {
			return err
		}
		arg := dbgen.CreateInviteParams{ID: xid.New().String(), CodeHash: auth.Hash(raw), Note: *note}
		if *expiresIn > 0 {
			arg.ExpiresAt = pgtype.Timestamptz{Time: time.Now().Add(*expiresIn), Valid: true}
		}
		row, err := q.CreateInvite(ctx, arg)
		if err != nil {
			return fmt.Errorf("create invite: %w", err)
		}
		fmt.Printf("invite_id: %s\ninvite:    %s\n\nPaste it on the authorize page when connecting Claude. It works once.\nSave it now; it is never shown again.\n", row.ID, raw)
		return nil
	case "list":
		rows, err := q.ListActiveInvites(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNOTE\tCREATED\tEXPIRES")
		for _, r := range rows {
			exp := "never"
			if r.ExpiresAt.Valid {
				exp = r.ExpiresAt.Time.UTC().Format(time.DateTime)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.ID, r.Note, r.CreatedAt.Time.UTC().Format(time.DateTime), exp)
		}
		return tw.Flush()
	case "delete":
		if *id == "" {
			return errors.New("--id is required")
		}
		n, err := q.DeleteInvite(ctx, *id)
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("no unused invite %s", *id)
		}
		fmt.Printf("deleted invite %s\n", *id)
		return nil
	default:
		return fmt.Errorf("unknown invites subcommand %q", sub)
	}
}

func mint(ctx context.Context, q *dbgen.Queries, name, kind string) error {
	raw, err := auth.GenerateKey()
	if err != nil {
		return err
	}
	row, err := q.CreateAPIKey(ctx, dbgen.CreateAPIKeyParams{ID: xid.New().String(), Name: name, KeyHash: auth.Hash(raw), Kind: kind})
	if err != nil {
		return fmt.Errorf("create api key: %w", err)
	}
	fmt.Printf("key_id:  %s\nkind:    %s\napi_key: %s\n\nSave the key now; it is never shown again.\n", row.ID, row.Kind, raw)
	return nil
}

// buildContent checks the content and, if it passes, writes the bundle the
// game runs on. Errors stop it; warnings are printed and let it through.
func buildContent(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	out := fs.String("out", "", "file to write the bundle to (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	c, err := content.Load(ctx, dbgen.New(pool))
	if err != nil {
		return err
	}
	bundle, report := content.Compile(c)
	for _, w := range report.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s: %s\n", w.Code, w.Message)
	}
	for _, e := range report.Errors {
		fmt.Fprintf(os.Stderr, "error: %s: %s\n", e.Code, e.Message)
	}
	if !report.OK() {
		return fmt.Errorf("%d errors; no bundle written", len(report.Errors))
	}
	b, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if *out == "" {
		_, err = os.Stdout.Write(b)
		return err
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		return err
	}
	s := report.Stats
	fmt.Fprintf(os.Stderr, "wrote %s: %d conversations, %d lines, %d choices, %d words\n", *out, s.Conversations, s.Nodes, s.Choices, s.Words)
	return nil
}

func openPool(ctx context.Context) (*pgxpool.Pool, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	return pool, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
