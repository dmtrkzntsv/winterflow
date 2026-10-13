// Package notify delivers alert notifications through Shoutrrr service
// URLs (telegram://, discord://, ntfy://, smtp://, generic://, ...). URLs
// carry secrets: every error leaving this package is redacted.
package notify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/nicholas-fedor/shoutrrr"
	"github.com/nicholas-fedor/shoutrrr/pkg/services/chat/matrix"
	"github.com/nicholas-fedor/shoutrrr/pkg/types"
)

// maxURLLength bounds a channel URL.
const maxURLLength = 2048

// Shoutrrr sends through Shoutrrr with a per-send timeout.
type Shoutrrr struct {
	timeout time.Duration
	// client and dial are the guarded HTTP client and TCP dialer (smtp,
	// mqtt, xmpp); nil when private targets are allowed (Shoutrrr's own are
	// used).
	client *http.Client
	dial   types.DialContextFunc
}

// NewShoutrrr builds a sender whose sends give up after timeout. Unless
// allowPrivate, sends to loopback, private and link-local addresses fail
// with ErrPrivateTarget (the distributed API must not reach its own
// network on a tenant's behalf).
func NewShoutrrr(timeout time.Duration, allowPrivate bool) *Shoutrrr {
	s := &Shoutrrr{timeout: timeout}
	if !allowPrivate {
		d := guardedDialer(timeout)
		s.client = guardedClient(d, timeout)
		s.dial = d.DialContext
	}
	return s
}

// Service validates rawURL as a Shoutrrr URL and returns its service name:
// the scheme without a "+transport" suffix ("generic+https" -> "generic").
// It never connects anywhere: Matrix, whose sender logs in while it is
// created, is checked from its config alone.
func Service(rawURL string) (string, error) {
	if rawURL == "" || len(rawURL) > maxURLLength {
		return "", errors.New("the notification URL is empty or too long")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		return "", errors.New("the notification URL is not a valid URL")
	}
	name := scheme(u)
	if name == "matrix" {
		if u.Scheme != "matrix" {
			return "", fmt.Errorf("invalid %s URL: matrix takes no transport suffix", u.Scheme)
		}
		if err := (&matrix.Config{}).SetURL(u); err != nil {
			return "", fmt.Errorf("invalid %s URL: %s", u.Scheme, Redact(err.Error(), rawURL))
		}
		return name, nil
	}
	if _, err := shoutrrr.CreateSenderWithOptions(types.SenderOptions{}, rawURL); err != nil {
		return "", fmt.Errorf("invalid %s URL: %s", u.Scheme, Redact(err.Error(), rawURL))
	}
	return name, nil
}

// noTitle are the services that refuse a "title" param: the title leads
// the body instead.
var noTitle = map[string]bool{"mqtt": true, "mqtts": true}

// Send delivers one notification. It returns when the service answers, the
// sender's timeout passes or ctx ends, whichever is first (creating the
// sender included: Matrix logs in then).
func (s *Shoutrrr) Send(ctx context.Context, rawURL, title, body string) error {
	done := make(chan error, 1)
	go func() { done <- s.send(ctx, rawURL, title, body) }()
	select {
	case err := <-done:
		if err != nil {
			return errors.New(Redact(err.Error(), rawURL))
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("send aborted: %w", ctx.Err())
	}
}

func (s *Shoutrrr) send(ctx context.Context, rawURL, title, body string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return errors.New("the notification URL is not a valid URL")
	}
	params := &types.Params{"title": title}
	if noTitle[scheme(u)] {
		body, params = title+"\n\n"+body, nil
	}
	if scheme(u) == "matrix" {
		return s.sendMatrix(ctx, u, body, params)
	}
	opts := types.SenderOptions{Timeout: s.timeout}
	if s.client != nil {
		opts.HTTPClient = s.client
		opts.DialContext = s.dial
	}
	r, err := shoutrrr.CreateSenderWithOptions(opts, rawURL)
	if err != nil {
		return err
	}
	for _, e := range r.Send(body, params) {
		if e != nil {
			return e
		}
	}
	return nil
}

// sendMatrix builds the Matrix sender itself: it logs in from Initialize,
// and Shoutrrr's router injects a client only after that, so the login
// would use Matrix's own client (no guard, following redirects anywhere).
// Setting our client first makes the login and every later request go
// through it.
func (s *Shoutrrr) sendMatrix(ctx context.Context, u *url.URL, body string, params *types.Params) error {
	if u.Scheme != "matrix" {
		return errors.New("matrix takes no transport suffix")
	}
	client := s.client
	if client == nil {
		client = &http.Client{Timeout: s.timeout}
	}
	svc := &matrix.Service{}
	svc.SetHTTPClient(client)
	if err := svc.Initialize(u, nil); err != nil {
		return fmt.Errorf("matrix: %w", err)
	}
	return svc.SendWithContext(ctx, body, params)
}

// minSecretLen is the shortest URL part Redact masks; shorter parts (a
// port, "api") are not secrets and masking them would garble messages.
const minSecretLen = 6

// Redact removes rawURL and its secret-looking parts (user info, host,
// path segments, query values) from msg. Each part is also split on
// [-:/@.] and every piece is masked on its own: services rebuild the URL
// they call from those pieces (slack://hook:T-B-SECRET@webhook posts to
// https://hooks.slack.com/services/T/B/SECRET), so the error text holds
// them in a different arrangement.
func Redact(msg, rawURL string) string {
	if rawURL == "" {
		return msg
	}
	msg = strings.ReplaceAll(msg, rawURL, "<url>")
	u, err := url.Parse(rawURL)
	if err != nil {
		return msg
	}
	var parts []string
	if u.User != nil {
		parts = append(parts, u.User.String(), u.User.Username())
		if p, ok := u.User.Password(); ok {
			parts = append(parts, p)
		}
	}
	parts = append(parts, u.Hostname())
	parts = append(parts, strings.Split(u.Path, "/")...)
	for _, vs := range u.Query() {
		parts = append(parts, vs...)
	}
	for _, p := range parts {
		parts = append(parts, strings.FieldsFunc(p, func(r rune) bool {
			return strings.ContainsRune("-:/@.", r)
		})...)
	}
	// Longest first, so a whole part is masked before its pieces.
	sort.SliceStable(parts, func(i, j int) bool { return len(parts[i]) > len(parts[j]) })
	for _, p := range parts {
		if len(p) >= minSecretLen {
			msg = strings.ReplaceAll(msg, p, "***")
		}
	}
	return msg
}
