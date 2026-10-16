// Package catalogfeed fetches the catalog feed over HTTP: catalog.json
// with If-None-Match, then catalog.json.sig, each bounded in time and size.
// Only https is fetched, except from a loopback host (tests, a feed served
// on the same box), redirects included.
package catalogfeed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
)

// Bounds of one fetch.
const (
	// RequestTimeout bounds each request, body included.
	RequestTimeout = 30 * time.Second
	// MaxFeedBytes caps catalog.json; MaxSigBytes catalog.json.sig.
	MaxFeedBytes = 8 << 20
	MaxSigBytes  = 4 << 10
	maxRedirects = 5
)

// Fetcher gets one feed URL and its signature.
type Fetcher struct {
	feed, sig *url.URL
	client    *http.Client
	timeout   time.Duration
}

var _ port.CatalogFetcher = (*Fetcher)(nil)

// New makes the fetcher of feedURL (CATALOG_URL); its signature is
// feedURL's path plus ".sig".
func New(feedURL string) (*Fetcher, error) {
	u, err := url.Parse(feedURL)
	if err != nil {
		return nil, fmt.Errorf("catalog URL: %w", err)
	}
	if err := allowed(u); err != nil {
		return nil, fmt.Errorf("catalog URL: %w", err)
	}
	sig := *u
	sig.Path += ".sig"
	sig.RawPath = ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &Fetcher{
		feed: u, sig: &sig, timeout: RequestTimeout,
		client: &http.Client{Transport: transport, CheckRedirect: checkRedirect},
	}, nil
}

// FetchCatalog GETs the feed (304 when etag still matches), then its
// signature; any other answer, a body over its cap or a request past
// RequestTimeout is an error.
func (f *Fetcher) FetchCatalog(ctx context.Context, etag string) (model.CatalogFetch, error) {
	feed, newETag, notModified, err := f.get(ctx, f.feed, etag, MaxFeedBytes)
	if err != nil {
		return model.CatalogFetch{}, err
	}
	if notModified {
		return model.CatalogFetch{NotModified: true, ETag: etag}, nil
	}
	sig, _, _, err := f.get(ctx, f.sig, "", MaxSigBytes)
	if err != nil {
		return model.CatalogFetch{}, err
	}
	return model.CatalogFetch{Feed: feed, Sig: sig, ETag: newETag}, nil
}

// get GETs u; a 304 counts only when it was asked for (etag set).
func (f *Fetcher) get(ctx context.Context, u *url.URL, etag string, limit int64) (body []byte, newETag string, notModified bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", false, err
	}
	req.Header.Set("User-Agent", "winterflow")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotModified && etag != "":
		return nil, "", true, nil
	case resp.StatusCode != http.StatusOK:
		return nil, "", false, fmt.Errorf("GET %s: %s", u.Redacted(), resp.Status)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", false, fmt.Errorf("GET %s: %w", u.Redacted(), err)
	}
	if int64(len(body)) > limit {
		return nil, "", false, fmt.Errorf("GET %s: larger than %d bytes", u.Redacted(), limit)
	}
	return body, resp.Header.Get("ETag"), false, nil
}

// checkRedirect follows at most maxRedirects redirects, each to an
// allowed URL.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("too many redirects")
	}
	return allowed(req.URL)
}

// allowed accepts https, and http to a loopback host only.
func allowed(u *url.URL) error {
	switch {
	case u.Host == "":
		return fmt.Errorf("%q has no host", u.Redacted())
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && loopback(u.Hostname()):
		return nil
	case u.Scheme == "http":
		return fmt.Errorf("%s: http only for a loopback host; use https", u.Redacted())
	}
	return fmt.Errorf("%s: not an https URL", u.Redacted())
}

func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
