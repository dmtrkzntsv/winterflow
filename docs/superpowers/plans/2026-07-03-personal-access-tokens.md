# Personal Access Tokens Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Users can create, list, and revoke API tokens (`wfp_…`) at `/user/tokens`; requests with `Authorization: Bearer wfp_…` or `Basic base64(x:PAT)` authenticate as that user.

**Architecture:** Hashed-at-rest tokens (SHA-256, plaintext shown once). A `PATAuth` middleware intercepts `Bearer wfp_` before the go-pkgz JWT parser; the existing `BasicAuthChecker` path keeps working via the reworked `FindByToken`. Three synchronous DB-backed endpoints; React page + hook, no SSE.

**Tech Stack:** Go 1.25, Bun ORM (SQLite/Postgres), go-pkgz/auth v2, chi router; React 19 + shadcn/ui.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-07-03-personal-access-tokens-design.md`.
- Token plaintext: `wfp_` + 40 base62 chars; prefix = first 12 chars; hash = lowercase hex SHA-256.
- No scopes; token grants full user access. `token_type` stays `'pat'`.
- API responses use the `util.APIResponse` envelope (`{success, message, data}`); errors 400 via `util.Error`, auth failures 401.
- TDD: failing test before implementation, `go test ./...` green at every commit.
- Both SQL dialects must work (migration follows the dialect-switch pattern of the initial migration).

---

### Task 1: `pkg/pat` — token generation and hashing

**Files:**
- Create: `pkg/pat/pat.go`
- Test: `pkg/pat/pat_test.go`

**Interfaces:**
- Produces: `pat.Generate() (plaintext, hash, prefix string, err error)`, `pat.Hash(token string) string`, `pat.PrefixLen = 12`, `pat.TokenPrefix = "wfp_"`.

- [ ] **Step 1: Write the failing test** (`pkg/pat/pat_test.go`)

```go
package pat

import (
	"regexp"
	"strings"
	"testing"
)

func TestGenerateFormat(t *testing.T) {
	plaintext, hash, prefix, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plaintext, "wfp_") {
		t.Errorf("plaintext %q lacks wfp_ prefix", plaintext)
	}
	if len(plaintext) != len("wfp_")+40 {
		t.Errorf("len = %d, want %d", len(plaintext), len("wfp_")+40)
	}
	if !regexp.MustCompile(`^wfp_[0-9A-Za-z]{40}$`).MatchString(plaintext) {
		t.Errorf("plaintext %q not base62", plaintext)
	}
	if prefix != plaintext[:PrefixLen] {
		t.Errorf("prefix %q != first %d chars of %q", prefix, PrefixLen, plaintext)
	}
	if hash != Hash(plaintext) {
		t.Error("returned hash does not match Hash(plaintext)")
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hash) {
		t.Errorf("hash %q not hex sha256", hash)
	}
}

func TestGenerateUnique(t *testing.T) {
	a, _, _, _ := Generate()
	b, _, _, _ := Generate()
	if a == b {
		t.Error("two generated tokens are equal")
	}
}

func TestHashStable(t *testing.T) {
	if Hash("wfp_x") != Hash("wfp_x") {
		t.Error("Hash not deterministic")
	}
	if Hash("a") == Hash("b") {
		t.Error("distinct inputs collide")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./pkg/pat/`
Expected: FAIL (package does not exist / undefined: Generate)

- [ ] **Step 3: Implement** (`pkg/pat/pat.go`)

```go
// Package pat generates and hashes personal access tokens.
//
// A token is "wfp_" + 40 base62 chars (~238 bits from crypto/rand). Only its
// SHA-256 is stored; the plaintext is shown to the user once at creation.
// The entropy makes a salt/KDF unnecessary — lookup is a unique-index hit on
// the hex digest.
package pat

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

const (
	// TokenPrefix marks WinterFlow PATs (helps secret scanners and lets the
	// auth middleware route Bearer tokens without a DB hit).
	TokenPrefix = "wfp_"
	// PrefixLen is how much of the plaintext is stored and shown in lists.
	PrefixLen = 12

	randomLen = 40
	alphabet  = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// Generate returns a new token's plaintext, its hex SHA-256, and its display
// prefix (the first PrefixLen characters).
func Generate() (plaintext, hash, prefix string, err error) {
	buf := make([]byte, randomLen)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", err
	}
	chars := make([]byte, randomLen)
	for i, b := range buf {
		chars[i] = alphabet[int(b)%len(alphabet)]
	}
	plaintext = TokenPrefix + string(chars)
	return plaintext, Hash(plaintext), plaintext[:PrefixLen], nil
}

// Hash returns the lowercase hex SHA-256 of a token plaintext.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./pkg/pat/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/pat/
git commit -m "feat(pat): token generation and hashing (wfp_ + 40 base62, sha256 at rest)"
```

---

### Task 2: schema migration + models

**Files:**
- Create: `internal/infra/db/migrations/20260703000001_pat_tokens.go`
- Modify: `internal/infra/db/models/models.go` (UserToken struct)
- Modify: `internal/domain/model/user.go` (UserToken domain type, ErrTokenNotFound)

**Interfaces:**
- Produces: `models.UserToken{TokenID, UserID, Name, TokenPrefix, TokenHash, TokenType, ExpiresAt *types.DateTime, LastUsedAt *types.DateTime, CreatedAt}`; `model.UserToken{ID, UserID, Name, Prefix, ExpiresAt *time.Time, LastUsedAt *time.Time, CreatedAt time.Time}`; `model.ErrTokenNotFound`.

- [ ] **Step 1: Write the migration** (`internal/infra/db/migrations/20260703000001_pat_tokens.go`)

```go
package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		return recreateUserTokensForPATs(ctx, db)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS user_tokens")
		return err
	})
}

// recreateUserTokensForPATs replaces the never-used v1 user_tokens table with
// the PAT shape: hashed token, display prefix, name, last-used. Dropping is
// safe — the feature never shipped, the table is empty everywhere.
func recreateUserTokensForPATs(ctx context.Context, db *bun.DB) error {
	var timestampType, timestampDefault, uuidType string
	switch db.Dialect().Name() {
	case dialect.PG:
		timestampType, timestampDefault, uuidType = "TIMESTAMPTZ", "DEFAULT NOW()", "UUID"
	case dialect.SQLite:
		timestampType, timestampDefault, uuidType = "TIMESTAMP", "DEFAULT CURRENT_TIMESTAMP", "VARCHAR(36)"
	default:
		return fmt.Errorf("unsupported database dialect: %s", db.Dialect().Name())
	}

	stmts := []string{
		"DROP TABLE IF EXISTS user_tokens",
		fmt.Sprintf(`CREATE TABLE user_tokens (
            token_id     %s PRIMARY KEY,
            user_id      %s NOT NULL,
            name         VARCHAR(64) NOT NULL,
            token_prefix VARCHAR(16) NOT NULL,
            token_hash   VARCHAR(64) NOT NULL UNIQUE,
            token_type   VARCHAR(16) NOT NULL CHECK (token_type IN ('pat')),
            expires_at   %s,
            last_used_at %s,
            created_at   %s NOT NULL %s,
            FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
        )`, uuidType, uuidType, timestampType, timestampType, timestampType, timestampDefault),
		"CREATE INDEX IF NOT EXISTS idx_user_tokens_user_id ON user_tokens (user_id)",
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("pat migration: %w", err)
		}
	}
	return nil
}
```

- [ ] **Step 2: Update the Bun model** — in `internal/infra/db/models/models.go` replace the `UserToken` struct with:

```go
type UserToken struct {
	bun.BaseModel `bun:"table:user_tokens"`

	TokenID     string          `bun:"token_id,pk,type:char(36)" json:"token_id"`
	UserID      string          `bun:"user_id,type:char(36)" json:"user_id"`
	Name        string          `bun:"name,notnull" json:"name"`
	TokenPrefix string          `bun:"token_prefix,notnull" json:"token_prefix"`
	TokenHash   string          `bun:"token_hash,unique,notnull" json:"-"`
	TokenType   string          `bun:"token_type,notnull" json:"token_type"`
	ExpiresAt   *types.DateTime `bun:"expires_at,nullzero" json:"expires_at"`
	LastUsedAt  *types.DateTime `bun:"last_used_at,nullzero" json:"last_used_at"`
	CreatedAt   types.DateTime  `bun:"created_at,notnull" json:"created_at"`

	User *User `bun:"rel:belongs-to,join:user_id=user_id"`
}
```

- [ ] **Step 3: Add the domain type** — in `internal/domain/model/user.go` add:

```go
// ErrTokenNotFound is returned when a token id does not exist for the user.
var ErrTokenNotFound = errors.New("token not found")

// UserToken is a personal access token record. The plaintext is never stored;
// Prefix (e.g. "wfp_9k3Ldx2p") identifies the token in lists.
type UserToken struct {
	ID         string     `json:"token_id"`
	UserID     string     `json:"-"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}
```

(add `time` to imports; `errors` is already imported)

- [ ] **Step 4: Verify everything still builds and migrates**

Run: `go build ./... && go test ./internal/infra/db/...`
Expected: PASS (repo tests run migrations against fresh SQLite)

- [ ] **Step 5: Commit**

```bash
git add internal/infra/db/migrations/20260703000001_pat_tokens.go internal/infra/db/models/models.go internal/domain/model/user.go
git commit -m "feat(db): PAT-shaped user_tokens table (hash, prefix, name, last-used)"
```

---

### Task 3: repository — create/list/delete + hashed FindByToken

**Files:**
- Modify: `internal/domain/port/user.go`
- Modify: `internal/infra/db/repository/user.go`
- Test: `internal/infra/db/repository/user_test.go`

**Interfaces:**
- Consumes: `pat.Generate`, `pat.Hash` (Task 1); models/domain types (Task 2).
- Produces (added to `port.UserRepository`):

```go
// CreateToken mints a PAT for the user. Returns the stored record and the
// plaintext — the only time the plaintext ever exists outside the caller.
CreateToken(ctx context.Context, userID, name string, expiresAt *time.Time) (model.UserToken, string, error)
// ListTokens returns the user's tokens, newest first. Never plaintext.
ListTokens(ctx context.Context, userID string) ([]model.UserToken, error)
// DeleteToken removes the user's token. model.ErrTokenNotFound if the id
// does not exist or belongs to another user.
DeleteToken(ctx context.Context, userID, tokenID string) error
```

- [ ] **Step 1: Write the failing tests** — append to `internal/infra/db/repository/user_test.go`:

```go
func TestCreateTokenAndFindByToken(t *testing.T) {
	repo := newUserRepo(t)
	ctx := context.Background()
	u, err := repo.CreateUser(ctx, dto.UserDTO{Name: "Alice", Provider: "google", AccountID: "g1"})
	if err != nil {
		t.Fatal(err)
	}

	rec, plaintext, err := repo.CreateToken(ctx, u.ID, "ci deploy", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Name != "ci deploy" || rec.Prefix == "" || rec.ID == "" {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.HasPrefix(plaintext, "wfp_") || !strings.HasPrefix(plaintext, rec.Prefix) {
		t.Fatalf("plaintext %q / prefix %q mismatch", plaintext, rec.Prefix)
	}

	got, err := repo.FindByToken(ctx, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != u.ID {
		t.Errorf("resolved user %q, want %q", got.ID, u.ID)
	}

	// The DB must not contain the plaintext anywhere.
	var stored models.UserToken
	if err := repo.db.GetDB().NewSelect().Model(&stored).Where("token_id = ?", rec.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if stored.TokenHash == plaintext || stored.TokenHash == "" {
		t.Error("token stored in plaintext or empty")
	}
}

func TestFindByTokenRejectsUnknownAndExpired(t *testing.T) {
	repo := newUserRepo(t)
	ctx := context.Background()
	u, _ := repo.CreateUser(ctx, dto.UserDTO{Name: "A", Provider: "google", AccountID: "g2"})

	if _, err := repo.FindByToken(ctx, "wfp_nope"); !errors.Is(err, model.ErrInvalidToken) {
		t.Errorf("unknown token: err = %v, want ErrInvalidToken", err)
	}

	past := time.Now().Add(-time.Hour)
	_, expired, err := repo.CreateToken(ctx, u.ID, "old", &past)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.FindByToken(ctx, expired); !errors.Is(err, model.ErrInvalidToken) {
		t.Errorf("expired token: err = %v, want ErrInvalidToken", err)
	}
}

func TestFindByTokenStampsLastUsed(t *testing.T) {
	repo := newUserRepo(t)
	ctx := context.Background()
	u, _ := repo.CreateUser(ctx, dto.UserDTO{Name: "A", Provider: "google", AccountID: "g3"})
	rec, plaintext, _ := repo.CreateToken(ctx, u.ID, "t", nil)

	if _, err := repo.FindByToken(ctx, plaintext); err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListTokens(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != rec.ID {
		t.Fatalf("list = %+v", list)
	}
	if list[0].LastUsedAt == nil {
		t.Error("last_used_at not stamped on use")
	}
}

func TestDeleteTokenScopedToUser(t *testing.T) {
	repo := newUserRepo(t)
	ctx := context.Background()
	owner, _ := repo.CreateUser(ctx, dto.UserDTO{Name: "O", Provider: "google", AccountID: "g4"})
	other, _ := repo.CreateUser(ctx, dto.UserDTO{Name: "X", Provider: "google", AccountID: "g5"})
	rec, plaintext, _ := repo.CreateToken(ctx, owner.ID, "mine", nil)

	if err := repo.DeleteToken(ctx, other.ID, rec.ID); !errors.Is(err, model.ErrTokenNotFound) {
		t.Errorf("cross-user delete: err = %v, want ErrTokenNotFound", err)
	}
	if err := repo.DeleteToken(ctx, owner.ID, rec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.FindByToken(ctx, plaintext); !errors.Is(err, model.ErrInvalidToken) {
		t.Error("token still resolves after delete")
	}
	if err := repo.DeleteToken(ctx, owner.ID, rec.ID); !errors.Is(err, model.ErrTokenNotFound) {
		t.Errorf("double delete: err = %v, want ErrTokenNotFound", err)
	}
}
```

(add `"strings"` to the test file imports)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/infra/db/repository/ -run 'Token' -v`
Expected: FAIL (undefined: CreateToken / ListTokens / DeleteToken)

- [ ] **Step 3: Implement** — in `internal/infra/db/repository/user.go`:

Add imports: `winterflow/pkg/pat`, `winterflow/pkg/util`.

Replace `FindByToken` and add the new methods:

```go
// lastUsedWriteInterval throttles last_used_at updates so a busy API client
// costs at most one write per minute, not one per request.
const lastUsedWriteInterval = time.Minute

func (r *DbUserRepository) FindByToken(ctx context.Context, plaintext string) (model.User, error) {
	var t models.UserToken
	err := r.db.GetDB().NewSelect().Model(&t).
		Where("token_hash = ?", pat.Hash(plaintext)).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, model.ErrInvalidToken
		}
		return model.User{}, err
	}
	if t.ExpiresAt != nil && time.Now().After(t.ExpiresAt.Time()) {
		return model.User{}, model.ErrInvalidToken
	}
	if t.LastUsedAt == nil || time.Since(t.LastUsedAt.Time()) > lastUsedWriteInterval {
		now := types.NewDateTime()
		if _, err := r.db.GetDB().NewUpdate().Model((*models.UserToken)(nil)).
			Set("last_used_at = ?", now).
			Where("token_id = ?", t.TokenID).
			Exec(ctx); err != nil {
			r.log.Error("FindByToken: stamp last_used_at", "error", err)
		}
	}
	return r.GetUser(ctx, t.UserID)
}

func (r *DbUserRepository) CreateToken(ctx context.Context, userID, name string, expiresAt *time.Time) (model.UserToken, string, error) {
	plaintext, hash, prefix, err := pat.Generate()
	if err != nil {
		return model.UserToken{}, "", err
	}
	row := &models.UserToken{
		TokenID:     util.GenerateID(),
		UserID:      userID,
		Name:        name,
		TokenPrefix: prefix,
		TokenHash:   hash,
		TokenType:   "pat",
		CreatedAt:   types.NewDateTime(),
	}
	if expiresAt != nil {
		dt := types.DateTime(*expiresAt)
		row.ExpiresAt = &dt
	}
	if _, err := r.db.GetDB().NewInsert().Model(row).Exec(ctx); err != nil {
		return model.UserToken{}, "", err
	}
	return toDomainToken(row), plaintext, nil
}

func (r *DbUserRepository) ListTokens(ctx context.Context, userID string) ([]model.UserToken, error) {
	var rows []models.UserToken
	if err := r.db.GetDB().NewSelect().Model(&rows).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]model.UserToken, 0, len(rows))
	for i := range rows {
		out = append(out, toDomainToken(&rows[i]))
	}
	return out, nil
}

func (r *DbUserRepository) DeleteToken(ctx context.Context, userID, tokenID string) error {
	res, err := r.db.GetDB().NewDelete().Model((*models.UserToken)(nil)).
		Where("token_id = ? AND user_id = ?", tokenID, userID).
		Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.ErrTokenNotFound
	}
	return nil
}

func toDomainToken(t *models.UserToken) model.UserToken {
	out := model.UserToken{
		ID:        t.TokenID,
		UserID:    t.UserID,
		Name:      t.Name,
		Prefix:    t.TokenPrefix,
		CreatedAt: t.CreatedAt.Time(),
	}
	if t.ExpiresAt != nil {
		x := t.ExpiresAt.Time()
		out.ExpiresAt = &x
	}
	if t.LastUsedAt != nil {
		x := t.LastUsedAt.Time()
		out.LastUsedAt = &x
	}
	return out
}
```

Add to `port.UserRepository` (with `time` import) the three method signatures from the Interfaces block above.

Note: if `types.DateTime(*expiresAt)` does not compile, check `internal/infra/db/types` for the correct constructor (there may be a `types.NewDateTimeFrom(t time.Time)`-style helper; use whatever exists — the repo tests will catch it).

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/infra/db/repository/ ./internal/domain/... && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/domain/port/user.go internal/infra/db/repository/user.go internal/infra/db/repository/user_test.go
git commit -m "feat(db): PAT create/list/delete; FindByToken matches by hash and stamps last-used"
```

---

### Task 4: Bearer middleware

**Files:**
- Create: `internal/app/web/middleware/patauth/patauth.go`
- Test: `internal/app/web/middleware/patauth/patauth_test.go`
- Modify: `internal/app/web/routes.go` (wrap `amw.Auth`)

**Interfaces:**
- Consumes: `port.UserService.FindByToken`, `pat.TokenPrefix`.
- Produces: `patauth.Middleware(users port.UserService, jwtAuth func(http.Handler) http.Handler) func(http.Handler) http.Handler`.

- [ ] **Step 1: Write the failing test** (`internal/app/web/middleware/patauth/patauth_test.go`)

```go
package patauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"winterflow/internal/domain/model"
	webutil "winterflow/internal/app/web/util"
)

type fakeUsers struct{ valid string }

func (f *fakeUsers) FindByToken(_ context.Context, tok string) (model.User, error) {
	if tok == f.valid {
		return model.User{ID: "user-1", Name: "Alice"}, nil
	}
	return model.User{}, model.ErrInvalidToken
}

// jwtMarker stands in for the go-pkgz JWT middleware so the test can see
// whether the request fell through.
func jwtMarker(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Fell-Through", "jwt")
		w.WriteHeader(http.StatusUnauthorized)
	})
}

func run(t *testing.T, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	mw := Middleware(&fakeUsers{valid: "wfp_good"}, jwtMarker)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := webutil.GetUserID(r)
		if err != nil {
			t.Errorf("GetUserID: %v", err)
		}
		w.Write([]byte(id))
	}))
	r := httptest.NewRequest("GET", "/api/v1/app/get-apps", nil)
	if authorization != "" {
		r.Header.Set("Authorization", authorization)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestValidPATAuthenticates(t *testing.T) {
	w := run(t, "Bearer wfp_good")
	if w.Code != http.StatusOK || w.Body.String() != "user-1" {
		t.Fatalf("code=%d body=%q", w.Code, w.Body.String())
	}
}

func TestInvalidPATIs401NotFallthrough(t *testing.T) {
	w := run(t, "Bearer wfp_bad")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", w.Code)
	}
	if w.Header().Get("X-Fell-Through") == "jwt" {
		t.Error("invalid wfp_ token must not fall through to JWT parsing")
	}
}

func TestNonPATFallsThroughToJWT(t *testing.T) {
	for _, h := range []string{"", "Bearer eyJhbGciOi.jwt.jwt", "Basic dXNlcjp3ZnBfZ29vZA=="} {
		w := run(t, h)
		if w.Header().Get("X-Fell-Through") != "jwt" {
			t.Errorf("Authorization %q: expected fall-through to JWT middleware", h)
		}
	}
}
```

Note on the fake: `Middleware` must accept the narrow interface it needs (`interface{ FindByToken(...) }`), not all of `port.UserService`, so the test fake stays 5 lines.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/app/web/middleware/patauth/`
Expected: FAIL (undefined: Middleware)

- [ ] **Step 3: Implement** (`internal/app/web/middleware/patauth/patauth.go`)

```go
// Package patauth authenticates personal access tokens sent as
// "Authorization: Bearer wfp_…". Anything else falls through to the wrapped
// JWT middleware, so browser sessions are untouched. (PATs in Basic auth are
// handled separately by the go-pkgz BasicAuthChecker in web/bootstrap.)
package patauth

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-pkgz/auth/v2/token"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/pkg/pat"
)

// TokenResolver is the slice of port.UserService this middleware needs.
type TokenResolver interface {
	FindByToken(ctx context.Context, token string) (model.User, error)
}

const bearerPAT = "Bearer " + pat.TokenPrefix

// Middleware returns an auth middleware: PAT bearer requests are resolved
// against the DB; everything else is delegated to jwtAuth (go-pkgz).
func Middleware(users TokenResolver, jwtAuth func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		viaJWT := jwtAuth(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, bearerPAT) {
				viaJWT.ServeHTTP(w, r)
				return
			}
			user, err := users.FindByToken(r.Context(), strings.TrimPrefix(auth, "Bearer "))
			if err != nil {
				webutil.Unauthorized(w)
				return
			}
			// Mirror the claims shape the JWT path produces: the internal user
			// id lives in the "user_id" attribute (read by util.GetUserID).
			u := token.User{ID: user.ID, Name: user.Name}
			u.SetStrAttr("user_id", user.ID)
			next.ServeHTTP(w, token.SetUserInfo(r, u))
		})
	}
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/app/web/middleware/patauth/`
Expected: PASS

- [ ] **Step 5: Wire it in `routes.go`** — after `amw := s.Auth.Middleware()` add:

```go
// authMW = PAT bearer first, JWT session otherwise. Every /api/v1 route uses
// this so tokens work everywhere a browser session does.
authMW := patauth.Middleware(s.Deps.UserService, amw.Auth)
```

and replace every `s.Router.With(amw.Auth` with `s.Router.With(authMW` (including the `, happ.GetAppsValidationMiddleware)` variants and the SSE stream route). Import `"winterflow/internal/app/web/middleware/patauth"`.

- [ ] **Step 6: Full verify + commit**

Run: `go build ./... && go test ./internal/app/web/...`
Expected: PASS

```bash
git add internal/app/web/middleware/patauth/ internal/app/web/routes.go
git commit -m "feat(auth): accept 'Authorization: Bearer wfp_…' PATs on all API routes"
```

---

### Task 5: endpoints — create/list/delete token

**Files:**
- Create: `internal/app/web/handler/user/handler.go`
- Test: `internal/app/web/handler/user/handler_test.go`
- Modify: `internal/app/web/routes.go` (three routes)

**Interfaces:**
- Consumes: `port.UserService` (Task 3 methods), `webutil` helpers.
- Produces routes: `POST /api/v1/user/create-token`, `GET /api/v1/user/get-tokens`, `POST /api/v1/user/delete-token` (shapes per spec).

- [ ] **Step 1: Write the failing test** (`internal/app/web/handler/user/handler_test.go`)

```go
package user

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-pkgz/auth/v2/token"

	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

type fakeTokens struct {
	created  []model.UserToken
	deleted  []string
	deleteBy string
}

func (f *fakeTokens) CreateToken(_ context.Context, userID, name string, expiresAt *time.Time) (model.UserToken, string, error) {
	rec := model.UserToken{ID: "tok-1", UserID: userID, Name: name, Prefix: "wfp_abcd1234", ExpiresAt: expiresAt, CreatedAt: time.Now()}
	f.created = append(f.created, rec)
	return rec, "wfp_abcd1234SECRETSECRETSECRETSECRETSECRET", nil
}
func (f *fakeTokens) ListTokens(_ context.Context, userID string) ([]model.UserToken, error) {
	return f.created, nil
}
func (f *fakeTokens) DeleteToken(_ context.Context, userID, tokenID string) error {
	f.deleteBy = userID
	if tokenID != "tok-1" {
		return model.ErrTokenNotFound
	}
	f.deleted = append(f.deleted, tokenID)
	return nil
}

func authed(r *http.Request) *http.Request {
	u := token.User{ID: "user-1"}
	u.SetStrAttr("user_id", "user-1")
	return token.SetUserInfo(r, u)
}

func newHandler() (*Handler, *fakeTokens) {
	f := &fakeTokens{}
	return NewHandler(&Deps{
		Logger: logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
		Tokens: f,
	}), f
}

func TestCreateTokenReturnsPlaintextOnce(t *testing.T) {
	h, _ := newHandler()
	r := authed(httptest.NewRequest("POST", "/api/v1/user/create-token",
		strings.NewReader(`{"name":"ci","expires_in_days":30}`)))
	w := httptest.NewRecorder()
	h.CreateToken(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Token     string  `json:"token"`
			Prefix    string  `json:"prefix"`
			ExpiresAt *string `json:"expires_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resp.Data.Token, "wfp_") {
		t.Errorf("token = %q", resp.Data.Token)
	}
	if resp.Data.ExpiresAt == nil {
		t.Error("expires_in_days was sent but expires_at is null")
	}
}

func TestCreateTokenRequiresName(t *testing.T) {
	h, _ := newHandler()
	for _, body := range []string{`{}`, `{"name":""}`, `{"name":"` + strings.Repeat("x", 65) + `"}`} {
		r := authed(httptest.NewRequest("POST", "/x", strings.NewReader(body)))
		w := httptest.NewRecorder()
		h.CreateToken(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %s: code = %d, want 400", body, w.Code)
		}
	}
}

func TestGetTokensListsWithoutPlaintext(t *testing.T) {
	h, f := newHandler()
	f.CreateToken(context.Background(), "user-1", "ci", nil)
	r := authed(httptest.NewRequest("GET", "/x", nil))
	w := httptest.NewRecorder()
	h.GetTokens(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "SECRET") {
		t.Error("list response leaks plaintext")
	}
}

func TestDeleteTokenScopesAndErrors(t *testing.T) {
	h, f := newHandler()
	r := authed(httptest.NewRequest("POST", "/x", strings.NewReader(`{"token_id":"tok-1"}`)))
	w := httptest.NewRecorder()
	h.DeleteToken(w, r)
	if w.Code != http.StatusOK || f.deleteBy != "user-1" {
		t.Fatalf("code=%d deleteBy=%q", w.Code, f.deleteBy)
	}

	r = authed(httptest.NewRequest("POST", "/x", strings.NewReader(`{"token_id":"other"}`)))
	w = httptest.NewRecorder()
	h.DeleteToken(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing token: code = %d, want 400", w.Code)
	}
}

func TestUnauthenticatedIs401(t *testing.T) {
	h, _ := newHandler()
	w := httptest.NewRecorder()
	h.GetTokens(w, httptest.NewRequest("GET", "/x", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("code = %d, want 401", w.Code)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/app/web/handler/user/`
Expected: FAIL (package/Handler undefined)

- [ ] **Step 3: Implement** (`internal/app/web/handler/user/handler.go`)

```go
// Package user serves user-scoped endpoints: personal access token
// management. All three are synchronous DB operations (no agent round-trip).
package user

import (
	"context"
	"net/http"
	"strings"
	"time"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

// TokenStore is the slice of port.UserService this handler needs.
type TokenStore interface {
	CreateToken(ctx context.Context, userID, name string, expiresAt *time.Time) (model.UserToken, string, error)
	ListTokens(ctx context.Context, userID string) ([]model.UserToken, error)
	DeleteToken(ctx context.Context, userID, tokenID string) error
}

type Deps struct {
	Logger *logger.Logger
	Tokens TokenStore
}

type Handler struct {
	log    *logger.Logger
	tokens TokenStore
}

func NewHandler(d *Deps) *Handler {
	return &Handler{log: d.Logger, tokens: d.Tokens}
}

type createTokenRequest struct {
	Name          string `json:"name"`
	ExpiresInDays int    `json:"expires_in_days"`
}

// CreateToken mints a PAT. The response carries the plaintext — the only
// time it is ever returned; the DB keeps just the hash.
func (h *Handler) CreateToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[createTokenRequest](w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 64 {
		webutil.Error(w, "name is required (max 64 chars)", nil)
		return
	}
	var expiresAt *time.Time
	if req.ExpiresInDays > 0 {
		t := time.Now().AddDate(0, 0, req.ExpiresInDays)
		expiresAt = &t
	}
	rec, plaintext, err := h.tokens.CreateToken(r.Context(), userID, name, expiresAt)
	if err != nil {
		h.log.Error("CreateToken", "error", err, "user_id", userID)
		webutil.Error(w, "failed to create token", nil)
		return
	}
	webutil.Success(w, "token created", struct {
		TokenID   string     `json:"token_id"`
		Token     string     `json:"token"`
		Prefix    string     `json:"prefix"`
		Name      string     `json:"name"`
		ExpiresAt *time.Time `json:"expires_at"`
	}{rec.ID, plaintext, rec.Prefix, rec.Name, rec.ExpiresAt})
}

// GetTokens lists the caller's tokens — never any plaintext.
func (h *Handler) GetTokens(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	list, err := h.tokens.ListTokens(r.Context(), userID)
	if err != nil {
		h.log.Error("GetTokens", "error", err, "user_id", userID)
		webutil.Error(w, "failed to list tokens", nil)
		return
	}
	webutil.Success(w, "tokens", list)
}

type deleteTokenRequest struct {
	TokenID string `json:"token_id"`
}

// DeleteToken revokes one of the caller's tokens. Unknown ids (including
// other users' tokens) report not-found.
func (h *Handler) DeleteToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[deleteTokenRequest](w, r)
	if !ok {
		return
	}
	if req.TokenID == "" {
		webutil.Error(w, "token_id is required", nil)
		return
	}
	if err := h.tokens.DeleteToken(r.Context(), userID, req.TokenID); err != nil {
		webutil.Error(w, "token not found", nil)
		return
	}
	webutil.Success(w, "token deleted", nil)
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/app/web/handler/user/`
Expected: PASS

- [ ] **Step 5: Wire routes** — in `routes.go` (import `huser "winterflow/internal/app/web/handler/user"`), after the servers block:

```go
usersAPI := huser.NewHandler(&huser.Deps{
	Logger: s.Logger,
	Tokens: s.Deps.UserService,
})
s.Router.With(authMW).Post("/api/v1/user/create-token", usersAPI.CreateToken)
s.Router.With(authMW).Get("/api/v1/user/get-tokens", usersAPI.GetTokens)
s.Router.With(authMW).Post("/api/v1/user/delete-token", usersAPI.DeleteToken)
```

- [ ] **Step 6: Full verify + commit**

Run: `go build ./... && go test ./...`
Expected: PASS

```bash
git add internal/app/web/handler/user/ internal/app/web/routes.go
git commit -m "feat(api): user token endpoints (create-token / get-tokens / delete-token)"
```

---

### Task 6: web UI — /user/tokens

**Files:**
- Create: `web/src/hooks/use-tokens.ts`
- Create: `web/src/pages/user-tokens.tsx`
- Modify: `web/src/main.tsx` (route)
- Modify: `web/src/components/nav-user.tsx` (menu item)

**Interfaces:**
- Consumes: the three endpoints from Task 5; `apiBaseUrl` from `@/config`; shadcn components already in the repo (`Button, Input, Label, Card*, Table*, Dialog*, Spinner`).
- Produces: `useTokens()` hook: `{tokens, loading, error, refresh, create(name, expiresInDays): Promise<CreatedToken>, remove(tokenId): Promise<void>}`.

- [ ] **Step 1: Write the hook** (`web/src/hooks/use-tokens.ts`)

```ts
import { useCallback, useEffect, useState } from "react";

import { apiBaseUrl } from "@/config";

export type ApiToken = {
  token_id: string;
  name: string;
  prefix: string;
  expires_at: string | null;
  last_used_at: string | null;
  created_at: string;
};

export type CreatedToken = {
  token_id: string;
  token: string; // plaintext — shown once, never retrievable again
  prefix: string;
  name: string;
  expires_at: string | null;
};

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${apiBaseUrl}${path}`, {
    credentials: "include",
    ...init,
  });
  const body = await res.json().catch(() => null);
  if (!res.ok || !body?.success) {
    throw new Error(body?.message ?? `Request failed: ${res.status}`);
  }
  return body.data as T;
}

// useTokens loads and mutates the user's personal access tokens. All three
// endpoints are synchronous (plain DB), so there is no SSE involvement.
export function useTokens() {
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await api<ApiToken[] | null>("/api/v1/user/get-tokens");
      setTokens(data ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load tokens");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const create = useCallback(
    async (name: string, expiresInDays: number): Promise<CreatedToken> => {
      const created = await api<CreatedToken>("/api/v1/user/create-token", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, expires_in_days: expiresInDays }),
      });
      void refresh();
      return created;
    },
    [refresh],
  );

  const remove = useCallback(
    async (tokenId: string) => {
      await api("/api/v1/user/delete-token", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ token_id: tokenId }),
      });
      void refresh();
    },
    [refresh],
  );

  return { tokens, loading, error, refresh, create, remove };
}
```

- [ ] **Step 2: Write the page** (`web/src/pages/user-tokens.tsx`)

```tsx
import { useMemo, useState } from "react";
import { Check, Copy, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import { useTokens, type CreatedToken } from "@/hooks/use-tokens";

const EXPIRY_OPTIONS = [
  { label: "30 days", days: 30 },
  { label: "90 days", days: 90 },
  { label: "1 year", days: 365 },
  { label: "No expiry", days: 0 },
];

function fmtDate(iso: string | null): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleDateString();
}

// UserTokensPage manages personal access tokens: list, generate (with a
// one-time plaintext reveal), and revoke. Tokens authenticate API calls via
// "Authorization: Bearer wfp_…" or Basic auth (token as password).
export default function UserTokensPage() {
  const breadcrumbs = useMemo(
    () => [{ label: "User" }, { label: "API tokens" }],
    [],
  );
  useAppBreadcrumbs(breadcrumbs);

  const { tokens, loading, error, create, remove } = useTokens();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [name, setName] = useState("");
  const [expiryDays, setExpiryDays] = useState(30);
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<CreatedToken | null>(null);
  const [copied, setCopied] = useState(false);

  const openDialog = () => {
    setName("");
    setExpiryDays(30);
    setCreated(null);
    setCopied(false);
    setDialogOpen(true);
  };

  const handleCreate = async () => {
    if (!name.trim()) {
      toast.error("Give the token a name");
      return;
    }
    setBusy(true);
    try {
      setCreated(await create(name.trim(), expiryDays));
    } catch (e) {
      toast.error("Failed to create token", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  const handleCopy = async () => {
    if (!created) return;
    await navigator.clipboard.writeText(created.token);
    setCopied(true);
    toast.success("Token copied");
  };

  const handleRevoke = async (tokenId: string, tokenName: string) => {
    if (!window.confirm(`Revoke "${tokenName}"? Clients using it will stop working immediately.`)) {
      return;
    }
    try {
      await remove(tokenId);
      toast.success("Token revoked");
    } catch (e) {
      toast.error("Failed to revoke token", {
        description: e instanceof Error ? e.message : undefined,
      });
    }
  };

  return (
    <div className="mx-auto w-full max-w-4xl space-y-6">
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle>Personal access tokens</CardTitle>
          <Button size="sm" onClick={openDialog}>
            <Plus /> Generate token
          </Button>
        </CardHeader>
        <CardContent>
          {loading ? (
            <div className="flex min-h-32 items-center justify-center">
              <Spinner />
            </div>
          ) : error ? (
            <p className="text-sm text-destructive">{error}</p>
          ) : tokens.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No tokens yet. Generate one to call the API from scripts or CI —
              send it as <code>Authorization: Bearer wfp_…</code>.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Token</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead>Expires</TableHead>
                  <TableHead>Last used</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {tokens.map((t) => (
                  <TableRow key={t.token_id}>
                    <TableCell className="font-medium">{t.name}</TableCell>
                    <TableCell className="font-mono text-xs">
                      {t.prefix}…
                    </TableCell>
                    <TableCell>{fmtDate(t.created_at)}</TableCell>
                    <TableCell>{fmtDate(t.expires_at)}</TableCell>
                    <TableCell>
                      {t.last_used_at ? fmtDate(t.last_used_at) : "Never"}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="icon"
                        onClick={() => void handleRevoke(t.token_id, t.name)}
                      >
                        <Trash2 className="text-destructive" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent>
          {created ? (
            <>
              <DialogHeader>
                <DialogTitle>Token created</DialogTitle>
                <DialogDescription>
                  Copy it now — you won&apos;t be able to see it again.
                </DialogDescription>
              </DialogHeader>
              <div className="flex items-center gap-2">
                <Input
                  readOnly
                  value={created.token}
                  className="font-mono text-xs"
                  onFocus={(e) => e.currentTarget.select()}
                />
                <Button variant="outline" size="icon" onClick={() => void handleCopy()}>
                  {copied ? <Check /> : <Copy />}
                </Button>
              </div>
              <DialogFooter>
                <Button onClick={() => setDialogOpen(false)}>Done</Button>
              </DialogFooter>
            </>
          ) : (
            <>
              <DialogHeader>
                <DialogTitle>Generate token</DialogTitle>
                <DialogDescription>
                  The token has the same access as your account. No scopes.
                </DialogDescription>
              </DialogHeader>
              <div className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="token-name">Name</Label>
                  <Input
                    id="token-name"
                    placeholder="e.g. CI deploy"
                    value={name}
                    maxLength={64}
                    onChange={(e) => setName(e.target.value)}
                  />
                </div>
                <div className="space-y-2">
                  <Label>Expires</Label>
                  <div className="flex gap-2">
                    {EXPIRY_OPTIONS.map((o) => (
                      <Button
                        key={o.days}
                        type="button"
                        size="sm"
                        variant={expiryDays === o.days ? "default" : "outline"}
                        onClick={() => setExpiryDays(o.days)}
                      >
                        {o.label}
                      </Button>
                    ))}
                  </div>
                </div>
              </div>
              <DialogFooter>
                <Button variant="outline" onClick={() => setDialogOpen(false)} disabled={busy}>
                  Cancel
                </Button>
                <Button onClick={() => void handleCreate()} disabled={busy}>
                  {busy ? "Creating…" : "Create"}
                </Button>
              </DialogFooter>
            </>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
```

- [ ] **Step 3: Route** — in `web/src/main.tsx` add to the authed children (after `/settings`):

```tsx
{
  path: "/user/tokens",
  element: <UserTokensPage />,
},
```

with `import UserTokensPage from "@/pages/user-tokens"` next to the other page imports.

- [ ] **Step 4: Menu item** — in `web/src/components/nav-user.tsx` replace the placeholder middle group (`Account` / `Billing` / `Notifications` items, and the `Upgrade to Pro` group above it) with:

```tsx
<DropdownMenuGroup>
  <DropdownMenuItem onClick={() => navigate("/user/tokens")}>
    <KeyRound />
    API tokens
  </DropdownMenuItem>
</DropdownMenuGroup>
```

Add `import { useNavigate } from "react-router-dom"` (call `const navigate = useNavigate()` inside the component), swap the now-unused lucide imports (`Sparkles, BadgeCheck, CreditCard, Bell`) for `KeyRound`, and drop the `DropdownMenuSeparator` that separated the removed group.

- [ ] **Step 5: Verify**

Run: `pnpm --dir web run build && pnpm --dir web run lint`
Expected: both exit 0

- [ ] **Step 6: Commit**

```bash
git add web/src/hooks/use-tokens.ts web/src/pages/user-tokens.tsx web/src/main.tsx web/src/components/nav-user.tsx
git commit -m "feat(web): /user/tokens page — generate (one-time reveal), list, revoke PATs"
```

---

### Task 7: end-to-end smoke + docs

**Files:**
- Modify: `MIGRATION.md` (route list), `CLAUDE.md` (auth line)

- [ ] **Step 1: E2E smoke against a scratch standalone** (ports chosen to avoid the dev stack on :8085/:5173)

```bash
mkdir -p /tmp/wf-pat && make build
WEB_URL=http://localhost:18098 API_PORT=18098 \
  DATABASE_URL="sqlite:///tmp/wf-pat/wf.sqlite" \
  AGENT_DATA_DIR=/tmp/wf-pat/data HUB_CERT_DIR=/tmp/wf-pat/certs \
  HUB_CERT_EXT_PATH=/tmp/wf-pat/ext.cnf \
  AUTH_ENV_USERNAME=test AUTH_ENV_PASSWORD=qwe123 \
  bin/standalone & sleep 3

API=http://localhost:18098
JWT=$(curl -s -c - "$API/auth/env/login?user=test&passwd=qwe123" | awk '/JWT/ {print $NF}')
# 1. create a token with the session
TOKEN=$(curl -s -b "JWT=$JWT" -H 'Content-Type: application/json' \
  -d '{"name":"smoke","expires_in_days":30}' "$API/api/v1/user/create-token" | jq -r .data.token)
echo "token: $TOKEN"   # expect wfp_…
# 2. Bearer PAT reaches a protected endpoint
curl -s -H "Authorization: Bearer $TOKEN" "$API/api/v1/user/get-tokens" | jq .success   # true
# 3. Basic PAT still works
curl -s -u "x:$TOKEN" "$API/api/v1/server/get-servers" | jq .success                    # true
# 4. garbage is rejected
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer wfp_garbage" \
  "$API/api/v1/user/get-tokens"                                                        # 401
# 5. revoke, then the token stops working
ID=$(curl -s -b "JWT=$JWT" "$API/api/v1/user/get-tokens" | jq -r '.data[0].token_id')
curl -s -b "JWT=$JWT" -H 'Content-Type: application/json' -d "{\"token_id\":\"$ID\"}" \
  "$API/api/v1/user/delete-token" | jq .success                                        # true
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOKEN" \
  "$API/api/v1/user/get-tokens"                                                        # 401
```

Then kill the scratch process (separate command; do not touch the dev stack).

- [ ] **Step 2: Docs** — MIGRATION.md: add to the sync-route list: `user/create-token`, `user/get-tokens`, `user/delete-token`; add a short "Personal access tokens" note (hashed at rest, Bearer `wfp_…` or Basic, UI at `/user/tokens`). CLAUDE.md auth line: mention PATs (`Bearer wfp_…`/Basic) alongside JWT.

- [ ] **Step 3: Final verify + commit + push**

Run: `go test ./... && pnpm --dir web run lint`

```bash
git add MIGRATION.md CLAUDE.md
git commit -m "docs: personal access tokens (endpoints, auth headers, UI)"
git push origin v2
```
