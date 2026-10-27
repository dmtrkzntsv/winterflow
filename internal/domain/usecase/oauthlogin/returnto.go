package oauthlogin

import (
	"net/url"
	"strings"
)

// maxReturnTo bounds the stored return path.
const maxReturnTo = 2048

// SafeReturnTo keeps raw only when it is a path on this origin ("/apps",
// "/settings?tab=x"); anything else (an absolute or scheme-relative URL, a
// backslash a browser would read as a slash, a control character) becomes
// "/". The sign-in ends with a redirect there, so it must never point away.
func SafeReturnTo(raw string) string {
	if raw == "" || len(raw) > maxReturnTo || !strings.HasPrefix(raw, "/") ||
		strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\\") {
		return "/"
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "/"
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" {
		return "/"
	}
	return raw
}

// withError is path (already a SafeReturnTo) with the query parameter
// error=code added.
func withError(path, code string) string {
	u, err := url.Parse(path)
	if err != nil {
		u = &url.URL{Path: "/"}
	}
	q := u.Query()
	q.Set("error", code)
	u.RawQuery = q.Encode()
	return u.String()
}

// loginErrorURL is the login page with error=code and, unless returnTo is
// home or not a SafeReturnTo, next=returnTo.
func loginErrorURL(returnTo, code string) string {
	q := url.Values{}
	q.Set("error", code)
	if next := SafeReturnTo(returnTo); next != "/" {
		q.Set("next", next)
	}
	return loginPage + "?" + q.Encode()
}
