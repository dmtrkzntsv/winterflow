package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"winterflow/pkg/version"
)

// dockerHubRegistry is where bare image names ("nginx") resolve.
const dockerHubRegistry = "registry-1.docker.io"

// maxImageTags caps how many tags one listing returns (pagination stops there).
const maxImageTags = 500

// parseImageRef splits an image reference into registry host and repository.
// Tags/digests are ignored — we list what's available. Bare names go to
// Docker Hub with the implicit library/ namespace.
func parseImageRef(image string) (host, repo string, err error) {
	image = strings.TrimSpace(image)
	if image == "" {
		return "", "", fmt.Errorf("empty image reference")
	}
	// Strip digest, then tag (the tag colon comes after the last slash).
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		image = image[:i]
	}

	parts := strings.SplitN(image, "/", 2)
	// A first segment with a dot, colon, or "localhost" is a registry host;
	// anything else is a Docker Hub namespace or bare name.
	if len(parts) == 2 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		return parts[0], parts[1], nil
	}
	repo = image
	if !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}
	return dockerHubRegistry, repo, nil
}

// registryScheme allows plain http only for local registries.
func registryScheme(host string) string {
	if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.") ||
		strings.HasPrefix(host, "[::1]") {
		return "http"
	}
	return "https"
}

// basicAuthFor returns the docker-config Authorization value for a host, or
// "" when the user isn't logged into that registry.
func basicAuthFor(host string) string {
	cfgPath, err := dockerConfigPath()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return ""
	}
	var cfg struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return ""
	}
	for key, entry := range cfg.Auths {
		if entry.Auth == "" {
			continue
		}
		trimmed := strings.TrimPrefix(strings.TrimPrefix(key, "https://"), "http://")
		trimmed = strings.TrimSuffix(trimmed, "/v1/")
		if trimmed == host || (host == dockerHubRegistry && strings.Contains(trimmed, "docker.io")) {
			return "Basic " + entry.Auth
		}
	}
	return ""
}

var bearerParamRe = regexp.MustCompile(`(\w+)="([^"]*)"`)

// bearerToken performs the registry token dance described by a 401's
// WWW-Authenticate header, forwarding the user's basic credentials when set.
func bearerToken(ctx context.Context, client *http.Client, challenge, basicAuth string) (string, error) {
	params := map[string]string{}
	for _, m := range bearerParamRe.FindAllStringSubmatch(challenge, -1) {
		params[m[1]] = m[2]
	}
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("no realm in auth challenge %q", challenge)
	}
	tokenURL := realm
	sep := "?"
	for _, k := range []string{"service", "scope"} {
		if params[k] != "" {
			tokenURL += sep + k + "=" + params[k]
			sep = "&"
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", err
	}
	if basicAuth != "" {
		req.Header.Set("Authorization", basicAuth)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint: %s", resp.Status)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Token != "" {
		return body.Token, nil
	}
	return body.AccessToken, nil
}

// ImageTags lists the tags available for an image via the registry HTTP v2
// API, using the agent's docker-config credentials when present and the
// bearer-token flow when the registry demands it (Docker Hub does).
func (r *Repository) ImageTags(ctx context.Context, image string) ([]string, error) {
	host, repo, err := parseImageRef(image)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	basicAuth := basicAuthFor(host)
	bearer := ""

	base := fmt.Sprintf("%s://%s", registryScheme(host), host)
	url := fmt.Sprintf("%s/v2/%s/tags/list?n=100", base, repo)
	var tags []string

	for url != "" && len(tags) < maxImageTags {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		} else if basicAuth != "" {
			req.Header.Set("Authorization", basicAuth)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode == http.StatusUnauthorized && bearer == "" {
			challenge := resp.Header.Get("WWW-Authenticate")
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if !strings.HasPrefix(strings.ToLower(challenge), "bearer") {
				return nil, fmt.Errorf("registry %s: unauthorized", host)
			}
			bearer, err = bearerToken(ctx, client, challenge, basicAuth)
			if err != nil {
				return nil, fmt.Errorf("registry %s auth: %w", host, err)
			}
			continue // retry the same URL with the bearer token
		}
		if resp.StatusCode != http.StatusOK {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("registry %s: %s for %s", host, resp.Status, repo)
		}

		var body struct {
			Tags []string `json:"tags"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		tags = append(tags, body.Tags...)
		url = nextPageURL(base, link)
	}

	sortTags(tags)
	if len(tags) > maxImageTags {
		tags = tags[:maxImageTags]
	}
	return tags, nil
}

var linkNextRe = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="next"`)

// nextPageURL resolves an RFC5988 Link rel=next header against the registry
// base ("" when there is no next page).
func nextPageURL(base, link string) string {
	m := linkNextRe.FindStringSubmatch(link)
	if m == nil {
		return ""
	}
	next := m[1]
	if strings.HasPrefix(next, "http") {
		return next
	}
	return base + next
}

// sortTags orders tags for humans: "latest" first, then numeric-looking tags
// descending by version, then everything else alphabetically.
func sortTags(tags []string) {
	sort.SliceStable(tags, func(i, j int) bool {
		a, b := tags[i], tags[j]
		if a == "latest" || b == "latest" {
			return a == "latest" && b != "latest"
		}
		av, bv := version.ParseNumericVersion(a), version.ParseNumericVersion(b)
		switch {
		case av > 0 && bv > 0:
			if av != bv {
				return av > bv
			}
			return a < b
		case av > 0:
			return true
		case bv > 0:
			return false
		default:
			return a < b
		}
	})
}
