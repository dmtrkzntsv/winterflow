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
	"github.com/nicholas-fedor/shoutrrr/pkg/types"
)

// maxURLLength bounds a channel URL.
const maxURLLength = 2048

// Shoutrrr sends through Shoutrrr with a per-send timeout.
type Shoutrrr struct {
	timeout time.Duration
	// client is the guarded HTTP client; nil when private targets are
	// allowed (Shoutrrr's own client is used).
	client *http.Client
}

// NewShoutrrr builds a sender whose sends give up after timeout. Unless
// allowPrivate, sends to loopback, private and link-local addresses fail
// with ErrPrivateTarget (the distributed API must not reach its own
// network on a tenant's behalf).
func NewShoutrrr(timeout time.Duration, allowPrivate bool) *Shoutrrr {
	s := &Shoutrrr{timeout: timeout}
	if !allowPrivate {
		s.client = guardedClient(timeout)
	}
	return s
}

// Service validates rawURL as a Shoutrrr URL and returns its service name:
// the scheme without a "+transport" suffix ("generic+https" -> "generic").
func Service(rawURL string) (string, error) {
	if rawURL == "" || len(rawURL) > maxURLLength {
		return "", errors.New("the notification URL is empty or too long")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		return "", errors.New("the notification URL is not a valid URL")
	}
	if _, err := shoutrrr.CreateSenderWithOptions(types.SenderOptions{}, rawURL); err != nil {
		return "", fmt.Errorf("invalid %s URL: %s", u.Scheme, Redact(err.Error(), rawURL))
	}
	return strings.SplitN(strings.ToLower(u.Scheme), "+", 2)[0], nil
}

// Send delivers one notification. It returns when the service answers, the
// sender's timeout passes or ctx ends, whichever is first.
func (s *Shoutrrr) Send(ctx context.Context, rawURL, title, body string) error {
	opts := types.SenderOptions{Timeout: s.timeout}
	if s.client != nil {
		if err := checkHost(ctx, rawURL); err != nil {
			return errors.New(Redact(err.Error(), rawURL))
		}
		opts.HTTPClient = s.client
	}
	r, err := shoutrrr.CreateSenderWithOptions(opts, rawURL)
	if err != nil {
		return errors.New(Redact(err.Error(), rawURL))
	}
	done := make(chan error, 1)
	go func() {
		for _, e := range r.Send(body, &types.Params{"title": title}) {
			if e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
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
