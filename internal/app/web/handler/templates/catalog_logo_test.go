package templates

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
)

const logoID = "00000000-0000-7000-8000-000000000001"

var tinyPNG = []byte("\x89PNG\r\n\x1a\nlogo")

type logoRepo struct {
	port.TemplateRepository
	tpl    model.Template
	listed bool
}

func (r logoRepo) GetTemplate(context.Context, string, string) (model.Template, error) {
	return r.tpl, nil
}
func (r logoRepo) ListTemplates(context.Context, string) ([]model.TemplateSummary, error) {
	if !r.listed {
		return nil, nil
	}
	return []model.TemplateSummary{{Template: r.tpl, Instances: 1}}, nil
}

type logoTransport func(*http.Request) (*http.Response, error)

func (f logoTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func logoResponse(status int, body []byte, location string) *http.Response {
	h := make(http.Header)
	if location != "" {
		h.Set("Location", location)
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}
}

func logoHandler(tpl model.Template, transport logoTransport) *Handler {
	return NewHandler(&Deps{Logger: testLog, Users: orgs{}, Templates: logoRepo{tpl: tpl, listed: true}, LogoClient: &http.Client{Transport: transport}})
}

func catalogWithLogo(raw string) model.Template {
	return model.Template{ID: logoID, Source: model.TemplateSourceCatalog, CatalogMeta: &model.CatalogMeta{LogoURL: raw}}
}

func requestLogo(h *Handler) *httptest.ResponseRecorder {
	return serve(h.CatalogLogo, http.MethodGet, "/?id="+logoID, "")
}

func TestCatalogLogoProxy(t *testing.T) {
	valid := "https://winterflow.io/catalog/logos/shop.png"
	t.Run("serves current metadata from the fixed origin", func(t *testing.T) {
		calls := 0
		h := logoHandler(catalogWithLogo(valid), func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.String() != valid {
				t.Fatalf("fetched %s", r.URL)
			}
			return logoResponse(http.StatusOK, tinyPNG, ""), nil
		})
		w := requestLogo(h)
		if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), tinyPNG) || w.Header().Get("Content-Type") != "image/png" ||
			w.Header().Get("X-Content-Type-Options") != "nosniff" || calls != 1 {
			t.Fatalf("logo: status %d, headers %v, calls %d, body %q", w.Code, w.Header(), calls, w.Body.Bytes())
		}
	})
	for _, raw := range []string{
		"http://winterflow.io/catalog/logos/shop.png",
		"https://evil.test/catalog/logos/shop.png",
		"https://winterflow.io.evil.test/catalog/logos/shop.png",
		"https://winterflow.io:443/catalog/logos/shop.png",
		"https://winterflow.io/catalog/logos/../shop.png",
		"https://winterflow.io/catalog/logos/shop.png?url=https://evil.test",
		"https://winterflow.io/catalog/logos/%2e%2e.png",
		"https://winterflow.io/catalog/shop.png",
	} {
		t.Run(raw, func(t *testing.T) {
			h := logoHandler(catalogWithLogo(raw), func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unsafe URL fetched: %s", r.URL)
				return nil, nil
			})
			if w := requestLogo(h); w.Code != http.StatusNotFound {
				t.Fatalf("status = %d", w.Code)
			}
		})
	}
	t.Run("refuses an off-origin redirect", func(t *testing.T) {
		calls := 0
		h := logoHandler(catalogWithLogo(valid), func(r *http.Request) (*http.Response, error) {
			calls++
			if calls > 1 {
				t.Fatalf("followed redirect to %s", r.URL)
			}
			return logoResponse(http.StatusFound, nil, "https://evil.test/logo.png"), nil
		})
		if w := requestLogo(h); w.Code != http.StatusNotFound || calls != 1 {
			t.Fatalf("status %d, calls %d", w.Code, calls)
		}
	})
	t.Run("accepts a same-directory redirect", func(t *testing.T) {
		calls := 0
		h := logoHandler(catalogWithLogo(valid), func(r *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return logoResponse(http.StatusFound, nil, "/catalog/logos/shop-v2.png"), nil
			}
			return logoResponse(http.StatusOK, tinyPNG, ""), nil
		})
		if w := requestLogo(h); w.Code != http.StatusOK || calls != 2 {
			t.Fatalf("status %d, calls %d", w.Code, calls)
		}
	})
	t.Run("rejects bad bytes and large responses", func(t *testing.T) {
		for _, body := range [][]byte{[]byte("<html>bad</html>"), bytes.Repeat([]byte("x"), maxLogoBytes+1)} {
			h := logoHandler(catalogWithLogo(valid), func(*http.Request) (*http.Response, error) { return logoResponse(http.StatusOK, body, ""), nil })
			if w := requestLogo(h); w.Code != http.StatusNotFound {
				t.Errorf("status = %d", w.Code)
			}
		}
	})
	t.Run("org and invisible withdrawn templates cannot fetch", func(t *testing.T) {
		never := logoTransport(func(r *http.Request) (*http.Response, error) { t.Fatalf("fetched %s", r.URL); return nil, nil })
		org := catalogWithLogo(valid)
		org.Source = model.TemplateSourceOrg
		if w := requestLogo(logoHandler(org, never)); w.Code != http.StatusNotFound {
			t.Errorf("org = %d", w.Code)
		}
		withdrawn := catalogWithLogo(valid)
		now := time.Now()
		withdrawn.WithdrawnAt = &now
		h := NewHandler(&Deps{Logger: testLog, Users: orgs{}, Templates: logoRepo{tpl: withdrawn}, LogoClient: &http.Client{Transport: never}})
		if w := requestLogo(h); w.Code != http.StatusNotFound {
			t.Errorf("withdrawn = %d", w.Code)
		}
	})
	t.Run("SVG carries restrictive policy", func(t *testing.T) {
		svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><circle r="2"/></svg>`)
		h := logoHandler(catalogWithLogo("https://winterflow.io/catalog/logos/shop.svg"), func(*http.Request) (*http.Response, error) { return logoResponse(http.StatusOK, svg, ""), nil })
		w := requestLogo(h)
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/svg+xml" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
			t.Fatalf("SVG = %d %v", w.Code, w.Header())
		}
		bad := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
		h = logoHandler(catalogWithLogo("https://winterflow.io/catalog/logos/shop.svg"), func(*http.Request) (*http.Response, error) { return logoResponse(http.StatusOK, bad, ""), nil })
		if w := requestLogo(h); w.Code != http.StatusNotFound {
			t.Fatalf("unsafe SVG = %d", w.Code)
		}
	})
}

func TestCatalogLogoImageTypes(t *testing.T) {
	for _, tc := range []struct {
		name string
		ext  string
		body []byte
		want string
	}{
		{name: "JPEG", ext: "jpg", body: []byte("\xff\xd8\xffimage"), want: "image/jpeg"},
		{name: "GIF", ext: "gif", body: []byte("GIF89aimage"), want: "image/gif"},
		{name: "WebP", ext: "webp", body: []byte("RIFFxxxxWEBPimage"), want: "image/webp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := logoHandler(catalogWithLogo("https://winterflow.io/catalog/logos/shop."+tc.ext), func(*http.Request) (*http.Response, error) {
				return logoResponse(http.StatusOK, tc.body, ""), nil
			})
			w := requestLogo(h)
			if w.Code != http.StatusOK || w.Header().Get("Content-Type") != tc.want || !bytes.Equal(w.Body.Bytes(), tc.body) {
				t.Fatalf("logo = %d %s %q", w.Code, w.Header().Get("Content-Type"), w.Body.Bytes())
			}
		})
	}
	// Upstream headers are ignored, but the content must still agree with the
	// catalog filename. A renamed HTML or JPEG response is not served as PNG.
	h := logoHandler(catalogWithLogo("https://winterflow.io/catalog/logos/shop.png"), func(*http.Request) (*http.Response, error) {
		return logoResponse(http.StatusOK, []byte("\xff\xd8\xffimage"), ""), nil
	})
	if w := requestLogo(h); w.Code != http.StatusNotFound {
		t.Fatalf("mismatched format = %d", w.Code)
	}
}
