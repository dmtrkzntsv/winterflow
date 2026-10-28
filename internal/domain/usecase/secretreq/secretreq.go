// Package secretreq is secret links: an MCP agent asks the user for an
// app's secret values (Create), the user opens the link signed in as a
// member of the request's organization (View) and the browser posts each
// value ECIES-encrypted to the server's key (Submit); the agent's next
// save_app takes the ciphertexts (Consume) and puts them in as those
// variables' values. The API never sees a plaintext: it checks only a
// ciphertext's shape and stores the ciphertexts until a save is dispatched
// with them (then they are deleted) or 24 hours pass.
//
// The link token is a capability only together with a member's session: a
// request of another organization, an unknown token and one that can no
// longer be submitted all read model.ErrNotFound.
package secretreq

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/crypto"
	"winterflow/pkg/pat"
	"winterflow/pkg/util"
)

const (
	// TokenPrefix marks a secret link's token.
	TokenPrefix = "wfx_"
	// tokenRandomBytes is the token's entropy.
	tokenRandomBytes = 32

	// SubmitWindow is how long a request takes a submission.
	SubmitWindow = 24 * time.Hour
	// UseWindow is how long submitted ciphertexts wait for a save.
	UseWindow = 24 * time.Hour
	// Retention is how long an ended request is kept.
	Retention = 7 * 24 * time.Hour

	// MaxNames caps the names of one request.
	MaxNames = 32
	// MaxNameBytes caps a name.
	MaxNameBytes = 128
	// MaxNoteBytes caps one name's note.
	MaxNoteBytes = 500
	// MaxMessageBytes caps the request's message.
	MaxMessageBytes = 2000
	// MaxCiphertextBytes caps one submitted ciphertext (a secret of about
	// 24 KiB).
	MaxCiphertextBytes = 32 << 10

	// commitTimeout bounds the writes that end or release a claim.
	commitTimeout = 30 * time.Second
	// commitTries is how often a dispatched claim's MarkUsed is tried.
	commitTries = 4
	// commitRetryDelay is the first delay between those tries (doubling).
	commitRetryDelay = 250 * time.Millisecond
	// ClaimTTL is how long a save holds a claimed request: past the MCP
	// wait (50 s) plus its margins, so a live save never loses its claim;
	// a crashed one's lapses and the request reads submitted again.
	ClaimTTL = 2 * time.Minute
)

// nameRE is the shape of a variable name compose interpolates.
var nameRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// Orgs resolves a user's organization.
type Orgs interface {
	PrimaryOrganizationID(ctx context.Context, userID string) (string, error)
}

// Servers answers ownership and lists the caller's servers (for a name).
type Servers interface {
	UserOwnsServer(ctx context.Context, userID, serverID string) (bool, error)
	GetServers(ctx context.Context, userID string) ([]model.Server, error)
}

// Apps reads the API's app catalog (for a name).
type Apps interface {
	GetApp(ctx context.Context, appID string) (model.App, error)
}

// Deps wires the usecase.
type Deps struct {
	Repo    port.SecretRequestRepository
	Orgs    Orgs
	Servers Servers
	Apps    Apps
	// Now defaults to time.Now.
	Now func() time.Time
	// RetryDelay is the first delay between MarkUsed tries (default
	// 250 ms).
	RetryDelay time.Duration
}

// UseCase is the secret links usecase.
type UseCase struct {
	d          Deps
	retryDelay time.Duration
}

// New builds the usecase.
func New(d Deps) *UseCase {
	if d.Now == nil {
		d.Now = time.Now
	}
	delay := d.RetryDelay
	if delay <= 0 {
		delay = commitRetryDelay
	}
	return &UseCase{d: d, retryDelay: delay}
}

// Request is a request as its creator sees it.
type Request = model.SecretRequest

// View is what the link's page shows.
type View struct {
	ServerID   string
	ServerName string
	AppID      string
	AppName    string
	Names      []string
	Notes      map[string]string
	Message    string
	ExpiresAt  time.Time
}

// errNotFound is the one answer for a link that cannot be used: unknown,
// another organization's, submitted, used or expired.
var errNotFound = fmt.Errorf("this link is not valid: it may have expired or been used already: %w", model.ErrNotFound)

// noRequest is a request id that names nothing the caller's org has.
var noRequest = fmt.Errorf("no such secret request in your organization: %w", model.ErrNotFound)

func rejected(format string, args ...any) error {
	return model.Rejected(fmt.Sprintf(format, args...))
}

// Create stores a pending request for names of an app (appID nil or empty:
// a new app) on serverID and returns it with its raw link token.
func (u *UseCase) Create(ctx context.Context, userID, serverID string, appID *string, names []string,
	notes map[string]string, message string) (Request, string, error) {
	app := ""
	if appID != nil {
		app = strings.TrimSpace(*appID)
	}
	if err := checkRequest(names, notes, message); err != nil {
		return Request{}, "", err
	}
	if app != "" && !model.ValidAppID(app) {
		return Request{}, "", rejected("invalid app_id")
	}
	owns, err := u.d.Servers.UserOwnsServer(ctx, userID, serverID)
	if err != nil {
		return Request{}, "", err
	}
	if !owns {
		return Request{}, "", fmt.Errorf("no such server in your organization: %w", model.ErrForbidden)
	}
	orgID, err := u.d.Orgs.PrimaryOrganizationID(ctx, userID)
	if err != nil {
		return Request{}, "", err
	}
	b := make([]byte, tokenRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return Request{}, "", err
	}
	raw := TokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	now := u.d.Now()
	if notes == nil {
		notes = map[string]string{}
	}
	req := Request{
		ID: util.GenerateID(), OrgID: orgID, ServerID: serverID, AppID: app, CreatedBy: userID,
		Names: slices.Clone(names), Notes: notes, Message: message, Status: model.SecretPending,
		CreatedAt: now, ExpiresAt: now.Add(SubmitWindow),
	}
	if err := u.d.Repo.Create(ctx, req, pat.Hash(raw)); err != nil {
		return Request{}, "", err
	}
	return req, raw, nil
}

// checkRequest checks a request's names, notes and message.
func checkRequest(names []string, notes map[string]string, message string) error {
	if len(names) == 0 {
		return rejected("names is required: the secret variables to ask for")
	}
	if len(names) > MaxNames {
		return rejected("at most %d names per request", MaxNames)
	}
	seen := map[string]bool{}
	for _, n := range names {
		switch {
		case len(n) > MaxNameBytes:
			return rejected("%s: a name is at most %d characters", n, MaxNameBytes)
		case !nameRE.MatchString(n):
			return rejected("invalid name %q: use A-Z, 0-9 and _, starting with a letter", n)
		case model.ReservedVariableName(n):
			return rejected("%s is reserved", n)
		case seen[n]:
			return rejected("%s is listed twice", n)
		}
		seen[n] = true
	}
	for n, note := range notes {
		if !seen[n] {
			return rejected("a note for %s, which is not in names", n)
		}
		if len(note) > MaxNoteBytes {
			return rejected("%s: a note is at most %d bytes", n, MaxNoteBytes)
		}
	}
	if len(message) > MaxMessageBytes {
		return rejected("the message is at most %d bytes", MaxMessageBytes)
	}
	return nil
}

// Get reads the caller's org's request, its status as of now.
func (u *UseCase) Get(ctx context.Context, userID, id string) (Request, error) {
	if !validID(id) {
		return Request{}, noRequest
	}
	orgID, err := u.d.Orgs.PrimaryOrganizationID(ctx, userID)
	if err != nil {
		return Request{}, err
	}
	req, err := u.d.Repo.Get(ctx, orgID, id)
	if errors.Is(err, model.ErrNotFound) {
		return Request{}, noRequest
	}
	if err != nil {
		return Request{}, err
	}
	// A claimed request reads submitted: a save holds it for a moment.
	if req.Status = req.StatusAt(u.d.Now()); req.Status == model.SecretClaimed {
		req.Status = model.SecretSubmitted
	}
	req.Ciphertexts = nil
	return req, nil
}

// validID reports a request id's shape (a UUID: on Postgres anything else
// could not even be looked up).
func validID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

// pending reads the request of a link the caller may still submit:
// pending, unexpired and of the caller's org; anything else is errNotFound.
func (u *UseCase) pending(ctx context.Context, userID, raw string) (Request, error) {
	if !strings.HasPrefix(raw, TokenPrefix) || len(raw) > 128 {
		return Request{}, errNotFound
	}
	req, err := u.d.Repo.GetByTokenHash(ctx, pat.Hash(raw))
	if errors.Is(err, model.ErrNotFound) {
		return Request{}, errNotFound
	}
	if err != nil {
		return Request{}, err
	}
	orgID, err := u.d.Orgs.PrimaryOrganizationID(ctx, userID)
	if err != nil {
		return Request{}, err
	}
	if req.OrgID != orgID || req.StatusAt(u.d.Now()) != model.SecretPending {
		return Request{}, errNotFound
	}
	return req, nil
}

// View is what the link's page shows to a member of the request's org.
func (u *UseCase) View(ctx context.Context, userID, raw string) (View, error) {
	req, err := u.pending(ctx, userID, raw)
	if err != nil {
		return View{}, err
	}
	v := View{ServerID: req.ServerID, AppID: req.AppID, Names: req.Names, Notes: req.Notes,
		Message: req.Message, ExpiresAt: req.ExpiresAt}
	if v.Notes == nil {
		v.Notes = map[string]string{}
	}
	// The names are for display only: a failed read leaves them empty.
	if servers, err := u.d.Servers.GetServers(ctx, userID); err == nil {
		for _, s := range servers {
			if s.ID == req.ServerID {
				v.ServerName = s.Name
			}
		}
	}
	if req.AppID != "" && u.d.Apps != nil {
		if app, err := u.d.Apps.GetApp(ctx, req.AppID); err == nil && app.ServerID == req.ServerID {
			v.AppName = app.Name
		}
	}
	return v, nil
}

// Submit stores the ciphertexts of exactly the requested names; each must
// have the ECIES wire format's shape (a plaintext is refused).
func (u *UseCase) Submit(ctx context.Context, userID, raw string, ciphertexts map[string]string) error {
	req, err := u.pending(ctx, userID, raw)
	if err != nil {
		return err
	}
	for _, n := range req.Names {
		if _, ok := ciphertexts[n]; !ok {
			return rejected("a value for %s is required", n)
		}
	}
	if len(ciphertexts) != len(req.Names) {
		return rejected("values must be exactly the requested names: %s", strings.Join(req.Names, ", "))
	}
	for n, ct := range ciphertexts {
		if len(ct) > MaxCiphertextBytes {
			return rejected("%s: the value is too large", n)
		}
		if crypto.CheckCiphertext(ct) != nil {
			return rejected("%s: not an encrypted value (encrypt it with the server's public key)", n)
		}
	}
	now := u.d.Now()
	ok, err := u.d.Repo.Submit(ctx, req.ID, ciphertexts, now, now.Add(UseWindow))
	if err != nil {
		return err
	}
	if !ok {
		// Submitted (or expired) since the read.
		return errNotFound
	}
	return nil
}

// errInUse refuses a request another save holds.
var errInUse = model.Rejected("secrets_from: this secret request is already being used by another save; " +
	"wait for that save, then check get_secret_request")

// Claim is a save's hold on a submitted request: no other save may use
// it until Commit (or ClaimTTL, unless Dispatch marked it dispatched).
type Claim struct {
	u    *UseCase
	ctx  context.Context
	id   string
	cid  string
	once sync.Once
	err  error
}

// Dispatch renews the claim and marks the request dispatched right before
// the save is sent, in one conditional write: from then on no claim takes
// it again, even if Commit never comes (a crash, a database that fails
// every try): the sweep ends it used. An error (the claim lapsed and
// another save took the request) means do not dispatch.
func (c *Claim) Dispatch(ctx context.Context) error {
	ok, err := c.u.d.Repo.MarkDispatched(ctx, c.id, c.cid, c.u.d.Now().Add(ClaimTTL))
	if err != nil {
		return err
	}
	if !ok {
		return errInUse
	}
	return nil
}

// Commit ends the claim once (later calls answer the first's result):
// dispatched marks the request used and deletes its ciphertexts, retried
// a few times on an error (the request stays dispatched either way, so it
// is never used twice), else it returns to submitted for a retry (the save
// was refused, or its send failed). A dispatched save whose claim was lost
// is an error: another save may have used the request too.
func (c *Claim) Commit(dispatched bool) error {
	c.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.ctx), commitTimeout)
		defer cancel()
		var ok bool
		if dispatched {
			ok, c.err = c.markUsed(ctx)
		} else {
			ok, c.err = c.u.d.Repo.Release(ctx, c.id, c.cid)
		}
		if c.err == nil && !ok && dispatched {
			c.err = errors.New("the secret request's claim was lost before it could be marked used")
		}
	})
	return c.err
}

// markUsed is MarkUsed, tried up to commitTries times with a doubling
// delay while it fails (a transient database error).
func (c *Claim) markUsed(ctx context.Context) (bool, error) {
	delay := c.u.retryDelay
	for try := 1; ; try++ {
		ok, err := c.u.d.Repo.MarkUsed(ctx, c.id, c.cid, c.u.d.Now())
		if err == nil || try == commitTries {
			return ok, err
		}
		select {
		case <-ctx.Done():
			return false, err
		case <-time.After(delay):
		}
		delay *= 2
	}
}

// Consume claims the caller's org's submitted request id for a save on
// serverID (and appID, when the request named one) and hands it the
// ciphertexts. The save must Commit the claim on every path: dispatched
// or not.
func (u *UseCase) Consume(ctx context.Context, userID, serverID, appID, id string) (map[string]string, *Claim, error) {
	if !validID(id) {
		return nil, nil, noRequest
	}
	orgID, err := u.d.Orgs.PrimaryOrganizationID(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	req, err := u.d.Repo.Get(ctx, orgID, id)
	if errors.Is(err, model.ErrNotFound) {
		return nil, nil, noRequest
	}
	if err != nil {
		return nil, nil, err
	}
	switch {
	case req.ServerID != serverID:
		return nil, nil, rejected("secrets_from: the request is for another server")
	case req.AppID != "" && req.AppID != appID:
		return nil, nil, rejected("secrets_from: the request is for another app (%s)", req.AppID)
	}
	now := u.d.Now()
	switch req.StatusAt(now) {
	case model.SecretPending:
		return nil, nil, rejected("secrets_from: the user has not submitted the values yet; check with get_secret_request")
	case model.SecretUsed:
		return nil, nil, rejected("secrets_from: the request was used already; ask again with request_secrets")
	case model.SecretExpired:
		return nil, nil, rejected("secrets_from: the request expired; ask again with request_secrets")
	case model.SecretClaimed:
		return nil, nil, errInUse
	case model.SecretSubmitted:
	default:
		return nil, nil, rejected("secrets_from: the request was used already; ask again with request_secrets")
	}
	c := &Claim{u: u, ctx: ctx, id: req.ID, cid: util.GenerateID()}
	ok, err := u.d.Repo.Claim(ctx, req.ID, c.cid, now, now.Add(ClaimTTL))
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		// Another save claimed it (or it ended) since the read.
		return nil, nil, errInUse
	}
	return req.Ciphertexts, c, nil
}

// Sweep expires the requests past their deadline (deleting their
// ciphertexts) and prunes those that ended more than Retention ago.
func (u *UseCase) Sweep(ctx context.Context) error {
	now := u.d.Now()
	if _, err := u.d.Repo.Expire(ctx, now); err != nil {
		return err
	}
	_, err := u.d.Repo.Prune(ctx, now.Add(-Retention))
	return err
}
