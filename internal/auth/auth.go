// Package auth carries who a request is from, and the key and token
// formats. A request acts as an api key, which belongs to a user; the user
// decides which games the request may see (app.Access), and the key is
// recorded on what it creates.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// Token prefixes. The prefix tells the middleware which table to look in,
// and tells a person what they are holding. Rename them for your app (the
// session cookie in middleware.go and the security schemes in
// api/openapi.yaml name them too). KeyPrefix must be a prefix of the
// others, and api keys never contain "_" after it, so the access-token
// check in Resolve can run first.
const (
	KeyPrefix          = "rpg_"    // api keys, minted by `server keys create`
	AccessTokenPrefix  = "rpg_at_" // OAuth access tokens and web sessions
	RefreshTokenPrefix = "rpg_rt_" // OAuth refresh tokens
	AuthCodePrefix     = "rpg_ac_" // OAuth authorization codes
	InvitePrefix       = "rpg_iv_" // single-use invites, minted by `server invites create`
	GameInvitePrefix   = "rpg_gi_" // single-use links into a game, made by its owner
)

// GenerateKey returns a new raw api key. It is shown to a person once; only
// Hash(key) is stored.
func GenerateKey() (string, error) {
	var b [20]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	return KeyPrefix + strings.ToLower(enc), nil
}

// RandToken returns prefix and 32 random bytes, URL-safe base64.
func RandToken(prefix string) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// Hash is the hex SHA-256 stored in place of every key and token.
func Hash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Key kinds. A human key may write the game's text; an agent key may read
// it and leave notes. See docs/authorship.md.
const (
	KindHuman = "human"
	KindAgent = "agent"
)

// Caller is who a request acts as.
type Caller struct {
	// KeyID is the api key behind the request: used directly, or through an
	// OAuth token or web session it approved.
	KeyID string
	// Human is true only for a human key used directly or through a web
	// session. An OAuth connector token is never human, whichever key
	// approved it: what holds it is a model.
	Human bool
	// UserID is the person the key belongs to, whose games the request
	// may see. "" for a key that has none, which sees no game.
	UserID string
	// System is a command run on the server (`server build`, `server
	// import`), which may reach every game. No request is ever System.
	System bool
}

type ctxKey struct{}

// WithCaller returns ctx carrying c.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// System marks ctx as a command run on the server itself, which may reach
// every game. Only cmd/ calls it.
func System(ctx context.Context) context.Context {
	return WithCaller(ctx, Caller{System: true})
}

// CallerFrom is the request's caller; the zero Caller outside one.
func CallerFrom(ctx context.Context) Caller {
	c, _ := ctx.Value(ctxKey{}).(Caller)
	return c
}

// UserID is the person the request acts for, or "".
func UserID(ctx context.Context) string { return CallerFrom(ctx).UserID }

// KeyID is the api key the request acts as, or "" outside an authenticated
// request (a CLI command, a test).
func KeyID(ctx context.Context) string {
	c, _ := ctx.Value(ctxKey{}).(Caller)
	return c.KeyID
}

// KeyIDPtr is KeyID as a nullable column value.
func KeyIDPtr(ctx context.Context) *string {
	if v := KeyID(ctx); v != "" {
		return &v
	}
	return nil
}

// IsHuman reports whether the request may write the game's text.
func IsHuman(ctx context.Context) bool {
	c, _ := ctx.Value(ctxKey{}).(Caller)
	return c.Human
}
