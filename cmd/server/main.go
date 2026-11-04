// Command server runs the app: web pages, a JSON API and MCP in one
// process, plus the admin commands that mint credentials. See README.md.
//
//	server [serve]                    run the server (DATABASE_URL, PORT, PUBLIC_URL)
//	server keys create --name NAME [--human] [--user USER]
//	                                  mint an api key (printed once); only a
//	                                  --human key may write the game's text.
//	                                  A key belongs to a user (an id or a
//	                                  name): a --human key with no --user
//	                                  makes a new user called NAME, and an
//	                                  agent key goes to the only user when
//	                                  there is just one
//	server users list                 list users, with their sign-ins
//	server members add --game GAME --user USER [--role writer|owner]
//	                                  put someone in a game without an invite
//	server import --game GAME --file FILE
//	                                  import licensed text (an SRD's skills)
//	                                  into a game; see docs/authorship.md
//	server build [--game GAME] [--out FILE]
//	                                  (GAME is a game's id, or its key when
//	                                  no other game shares it)
//	                                  check a game's content and write the
//	                                  bundle it runs on (stdout by default);
//	                                  --game may be left out when there is one
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
// (Signing in with Google mints a web session, as pasting a key on /login
// does, and the first time also the person's own human key, which nobody
// ever sees: see docs/auth.md.)
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
	case "users":
		err = users(args)
	case "members":
		err = members(args)
	case "build":
		err = buildContent(args)
	case "import":
		err = importLicensed(args)
	default:
		err = fmt.Errorf("unknown command %q; use serve, keys, users, members, invites, import or build", cmd)
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
	// Google sign-in is on when both are set (docs/configuration.md).
	srv.Google = web.GoogleConfig{ClientID: os.Getenv("GOOGLE_CLIENT_ID"), ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET")}
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
	user := fs.String("user", "", "the user the key belongs to, by id or name (create)")
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
		var userID string
		switch {
		case *user != "":
			u, err := findUser(ctx, q, *user)
			if err != nil {
				return err
			}
			userID = u.ID
		case *human:
			u, err := q.CreateUser(ctx, dbgen.CreateUserParams{ID: xid.New().String(), Name: *name})
			if err != nil {
				return fmt.Errorf("create user: %w", err)
			}
			fmt.Printf("user_id: %s (new user %s)\n", u.ID, u.Name)
			userID = u.ID
		default:
			all, err := q.ListUsers(ctx)
			if err != nil {
				return err
			}
			if len(all) != 1 {
				return fmt.Errorf("say whose agent this is with --user (%s)", userNames(all))
			}
			userID = all[0].ID
		}
		return mint(ctx, q, *name, kind, &userID)
	case "list":
		rows, err := q.ListAPIKeys(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		all, err := q.ListUsers(ctx)
		if err != nil {
			return err
		}
		names := map[string]string{}
		for _, u := range all {
			names[u.ID] = u.Name
		}
		fmt.Fprintln(tw, "ID\tNAME\tKIND\tUSER\tCREATED\tLAST USED")
		for _, r := range rows {
			last := "-"
			if r.LastUsedAt.Valid {
				last = r.LastUsedAt.Time.UTC().Format(time.DateTime)
			}
			owner := "-"
			if r.UserID != nil {
				owner = names[*r.UserID]
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Name, r.Kind, owner, r.CreatedAt.Time.UTC().Format(time.DateTime), last)
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
		return mint(ctx, q, old.Name, old.Kind, old.UserID)
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

func mint(ctx context.Context, q *dbgen.Queries, name, kind string, userID *string) error {
	raw, err := auth.GenerateKey()
	if err != nil {
		return err
	}
	row, err := q.CreateAPIKey(ctx, dbgen.CreateAPIKeyParams{ID: xid.New().String(), Name: name, KeyHash: auth.Hash(raw), Kind: kind, UserID: userID})
	if err != nil {
		return fmt.Errorf("create api key: %w", err)
	}
	fmt.Printf("key_id:  %s\nkind:    %s\napi_key: %s\n\nSave the key now; it is never shown again.\n", row.ID, row.Kind, raw)
	return nil
}

// findGame is the game a command names by id or key. A key is a label
// that several people's games may share, so an ambiguous one is refused
// with their ids.
func findGame(ctx context.Context, q *dbgen.Queries, idOrKey string) (dbgen.Game, error) {
	found, err := q.FindGames(ctx, idOrKey)
	if err != nil {
		return dbgen.Game{}, err
	}
	for _, g := range found {
		if g.ID == idOrKey {
			return g, nil
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return dbgen.Game{}, fmt.Errorf("no game %q", idOrKey)
	}
	ids := make([]string, len(found))
	for i, g := range found {
		ids[i] = g.ID + " (" + g.Name + ")"
	}
	return dbgen.Game{}, fmt.Errorf("%d games have the key %q; name one by id: %s", len(found), idOrKey, strings.Join(ids, ", "))
}

// findUser is a user by id, or else by name, which must be unambiguous.
func findUser(ctx context.Context, q *dbgen.Queries, idOrName string) (dbgen.User, error) {
	all, err := q.ListUsers(ctx)
	if err != nil {
		return dbgen.User{}, err
	}
	var found []dbgen.User
	for _, u := range all {
		if u.ID == idOrName {
			return u, nil
		}
		if strings.EqualFold(u.Name, idOrName) {
			found = append(found, u)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return dbgen.User{}, fmt.Errorf("no user %q (%s)", idOrName, userNames(all))
	default:
		return dbgen.User{}, fmt.Errorf("%d users are called %q; use an id from users list", len(found), idOrName)
	}
}

func userNames(all []dbgen.User) string {
	if len(all) == 0 {
		return "there are no users yet; make one with keys create --human"
	}
	names := make([]string, len(all))
	for i, u := range all {
		names[i] = u.Name
	}
	return "users: " + strings.Join(names, ", ")
}

// users lists the people who can sign in.
func users(args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return errors.New("usage: users list")
	}
	ctx := context.Background()
	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	q := dbgen.New(pool)
	all, err := q.ListUsers(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSIGN-INS\tCREATED")
	for _, u := range all {
		ids, err := q.ListUserIdentities(ctx, u.ID)
		if err != nil {
			return err
		}
		signIns := make([]string, len(ids))
		for i, id := range ids {
			signIns[i] = id.Provider + ":" + id.Email
		}
		if len(signIns) == 0 {
			signIns = []string{"-"}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", u.ID, u.Name, strings.Join(signIns, ", "), u.CreatedAt.Time.UTC().Format(time.DateTime))
	}
	return tw.Flush()
}

// members puts a user in a game directly, for an admin: a game made by a
// binary from before there were users has no owner until someone adds one.
func members(args []string) error {
	if len(args) == 0 || args[0] != "add" {
		return errors.New("usage: members add --game GAME --user USER [--role writer|owner]")
	}
	fs := flag.NewFlagSet("members add", flag.ExitOnError)
	gameKey := fs.String("game", "", "the game's key")
	user := fs.String("user", "", "the user, by id or name")
	role := fs.String("role", app.RoleWriter, "writer or owner")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *gameKey == "" || *user == "" {
		return errors.New("--game and --user are required")
	}
	if *role != app.RoleWriter && *role != app.RoleOwner {
		return fmt.Errorf("--role must be writer or owner, not %q", *role)
	}
	ctx := context.Background()
	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	q := dbgen.New(pool)
	game, err := findGame(ctx, q, *gameKey)
	if err != nil {
		return err
	}
	u, err := findUser(ctx, q, *user)
	if err != nil {
		return err
	}
	if err := q.AddGameMember(ctx, dbgen.AddGameMemberParams{GameID: game.ID, UserID: u.ID, Role: *role}); err != nil {
		return fmt.Errorf("add member (a game has one owner): %w", err)
	}
	fmt.Printf("%s is in %s as %s (if they were already a member, their role is unchanged)\n", u.Name, game.Key, *role)
	return nil
}

// buildContent checks the content and, if it passes, writes the bundle the
// game runs on. Errors stop it; warnings are printed and let it through.
func buildContent(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	out := fs.String("out", "", "file to write the bundle to (default stdout)")
	gameKey := fs.String("game", "", "the game's id or key (default: the only game)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// A command on the server reaches every game.
	ctx := auth.System(context.Background())
	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	q := dbgen.New(pool)
	var game dbgen.Game
	if *gameKey != "" {
		if game, err = findGame(ctx, q, *gameKey); err != nil {
			return err
		}
	} else {
		games, err := q.ListGames(ctx)
		if err != nil {
			return err
		}
		if len(games) != 1 {
			keys := make([]string, len(games))
			for i, g := range games {
				keys[i] = g.Key
			}
			return fmt.Errorf("there are %d games; say which with --game (%s)", len(games), strings.Join(keys, ", "))
		}
		game = games[0]
	}
	c, err := content.Load(ctx, q, game.ID)
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

// importLicensed adds licensed text from a file to a game (app.Import).
func importLicensed(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	gameKey := fs.String("game", "", "the game's key")
	file := fs.String("file", "", "the JSON file to import (docs/authorship.md shows its shape)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *gameKey == "" || *file == "" {
		return errors.New("--game and --file are required")
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	var in app.Licensed
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("%s: %w", *file, err)
	}
	ctx := auth.System(context.Background())
	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := app.NewStore(pool)
	game, err := findGame(ctx, store.Queries, *gameKey)
	if err != nil {
		return err
	}
	added, kept, err := store.Import(ctx, game.ID, in)
	if err != nil {
		return err
	}
	fmt.Printf("%s: imported %d skills from %s; %d were already there\n", game.Key, added, in.Source, kept)
	return nil
}
