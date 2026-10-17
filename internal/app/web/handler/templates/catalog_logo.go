package templates

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

const (
	maxLogoBytes = 1 << 20
	logoTimeout  = 5 * time.Second
)

var logoName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.(?:png|jpg|jpeg|gif|webp|svg)$`)

// CatalogLogoQuery identifies a catalog template. The caller cannot provide
// a remote URL: the latest logo_url is read from catalog metadata.
type CatalogLogoQuery struct {
	ID string `query:"id" required:"true"`
}

func allowedLogoURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "winterflow.io" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" || u.RawPath != "" {
		return nil, false
	}
	const prefix = "/catalog/logos/"
	if !strings.HasPrefix(u.Path, prefix) || !logoName.MatchString(strings.TrimPrefix(u.Path, prefix)) {
		return nil, false
	}
	return u, true
}

func logoType(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case len(data) >= 3 && bytes.Equal(data[:3], []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp"
	case safeSVG(data):
		return "image/svg+xml"
	default:
		return ""
	}
}

// safeSVG rejects XML features, active SVG nodes, event handlers and
// references. This keeps the proxy's SVG surface small even if someone
// opens an image directly instead of displaying it in an <img>.
func safeSVG(data []byte) bool {
	dec := xml.NewDecoder(bytes.NewReader(data))
	root := false
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return root && depth == 0
		}
		if err != nil {
			return false
		}
		switch v := tok.(type) {
		case xml.Directive, xml.ProcInst:
			return false
		case xml.StartElement:
			if root && depth == 0 {
				return false
			}
			if !root {
				if v.Name.Local != "svg" || (v.Name.Space != "" && v.Name.Space != "http://www.w3.org/2000/svg") {
					return false
				}
				root = true
			}
			depth++
			name := strings.ToLower(v.Name.Local)
			if v.Name.Space != "" && v.Name.Space != "http://www.w3.org/2000/svg" ||
				name == "script" || name == "foreignobject" || name == "iframe" || name == "image" || name == "use" ||
				name == "animate" || name == "set" || name == "style" {
				return false
			}
			for _, a := range v.Attr {
				key := strings.ToLower(a.Name.Local)
				if strings.HasPrefix(key, "on") || key == "href" || key == "style" || key == "src" ||
					strings.Contains(strings.ToLower(a.Value), "url(") || a.Name.Space == "http://www.w3.org/1999/xlink" {
					return false
				}
			}
		case xml.EndElement:
			depth--
		}
	}
}

func (h *Handler) CatalogLogo(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	id := r.URL.Query().Get("id")
	if !requireID(w, "id", id) {
		return
	}
	miss := func() { webutil.ErrorStatus(w, http.StatusNotFound, "catalog logo unavailable") }
	if h.templates == nil {
		miss()
		return
	}
	tpl, err := h.templates.GetTemplate(r.Context(), orgID, id)
	if err != nil || tpl.Source != model.TemplateSourceCatalog || tpl.CatalogMeta == nil {
		miss()
		return
	}
	if tpl.WithdrawnAt != nil {
		// A withdrawn catalog entry is visible only in orgs with instances.
		list, err := h.templates.ListTemplates(r.Context(), orgID)
		if err != nil {
			miss()
			return
		}
		visible := false
		for _, item := range list {
			if item.ID == id {
				visible = true
				break
			}
		}
		if !visible {
			miss()
			return
		}
	}
	u, valid := allowedLogoURL(tpl.CatalogMeta.LogoURL)
	if !valid {
		miss()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), logoTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		miss()
		return
	}
	client := http.Client{Timeout: logoTimeout}
	if h.logoClient != nil {
		client = *h.logoClient
		client.Timeout = logoTimeout
	}
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return http.ErrUseLastResponse
		}
		_, ok := allowedLogoURL(next.URL.String())
		if !ok {
			return http.ErrUseLastResponse
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		miss()
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength > maxLogoBytes {
		miss()
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxLogoBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxLogoBytes {
		miss()
		return
	}
	contentType := logoType(data)
	if contentType == "" {
		miss()
		return
	}
	// The filename and the bytes must agree; a misleading upstream header is
	// ignored, and an HTML response can never be delivered as an image.
	ext := strings.ToLower(u.Path[strings.LastIndexByte(u.Path, '.')+1:])
	if !logoExtensionMatches(ext, contentType) {
		miss()
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	if contentType == "image/svg+xml" {
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; script-src 'none'; style-src 'none'; img-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'")
	}
	_, _ = w.Write(data)
}

func logoExtensionMatches(ext, typ string) bool {
	switch ext {
	case "png":
		return typ == "image/png"
	case "jpg", "jpeg":
		return typ == "image/jpeg"
	case "gif":
		return typ == "image/gif"
	case "webp":
		return typ == "image/webp"
	case "svg":
		return typ == "image/svg+xml"
	default:
		return false
	}
}
