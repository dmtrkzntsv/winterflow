package dockercompose

import (
	"os"
	"path/filepath"
	"strings"
)

// slugify turns an app name into a filesystem-friendly symlink name:
// lowercase, [a-z0-9-] only, runs collapsed to one '-'. Never empty.
func slugify(name string) string {
	var b strings.Builder
	prevDash := true // suppress leading dash
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "app"
	}
	return s
}

// linkTarget is the relative target for an app's symlink: ../{dataDirName}/{appID}.
func linkTarget(dataDirName, appID string) string {
	return filepath.Join("..", dataDirName, appID)
}

// appIDFromLink extracts the app id a symlink in appsDir points at, or "" if
// the entry is not one of our links.
func appIDFromLink(appsDir, name, dataDirName string) string {
	target, err := os.Readlink(filepath.Join(appsDir, name))
	if err != nil {
		return ""
	}
	prefix := filepath.Join("..", dataDirName) + string(filepath.Separator)
	if !strings.HasPrefix(target, prefix) {
		return ""
	}
	return strings.TrimPrefix(target, prefix)
}

// ensureAppSymlink makes {appsDir}/{slug(name)} point at ../{dataDirName}/{appID}.
// Any other link for the same app is removed first (rename). If the desired
// slug already belongs to a different app, "-{appID[:8]}" is appended.
// Returns the link name used.
func ensureAppSymlink(appsDir, dataDirName, appID, name string) (string, error) {
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		return "", err
	}

	slug := slugify(name)
	if owner := appIDFromLink(appsDir, slug, dataDirName); owner != "" && owner != appID {
		suffix := appID
		if len(suffix) > 8 {
			suffix = suffix[:8]
		}
		slug = slug + "-" + suffix
	}

	// Drop any existing links for this app under other names.
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.Name() == slug {
			continue
		}
		if appIDFromLink(appsDir, e.Name(), dataDirName) == appID {
			if err := os.Remove(filepath.Join(appsDir, e.Name())); err != nil {
				return "", err
			}
		}
	}

	linkPath := filepath.Join(appsDir, slug)
	target := linkTarget(dataDirName, appID)
	if current, err := os.Readlink(linkPath); err == nil {
		if current == target {
			return slug, nil
		}
		if err := os.Remove(linkPath); err != nil {
			return "", err
		}
	}
	if err := os.Symlink(target, linkPath); err != nil {
		return "", err
	}
	return slug, nil
}

// removeAppSymlink removes any link in appsDir pointing at appID.
func removeAppSymlink(appsDir, appID string) error {
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if appIDFromLink(appsDir, e.Name(), dataDirName(appsDir)) == appID {
			if err := os.Remove(filepath.Join(appsDir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// dataDirName assumes the canonical sibling layout ({root}/apps next to
// {root}/apps-data); it exists so removeAppSymlink doesn't need the name
// threaded through every caller.
func dataDirName(string) string { return "apps-data" }

// healAppSymlinks reconciles appsDir with the given appID->name set: dangling
// or unknown links are pruned, missing links are created.
func healAppSymlinks(appsDir, dirName string, apps map[string]string) error {
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink == 0 {
			continue
		}
		id := appIDFromLink(appsDir, e.Name(), dirName)
		if _, live := apps[id]; id == "" || !live {
			if err := os.Remove(filepath.Join(appsDir, e.Name())); err != nil {
				return err
			}
		}
	}
	for id, name := range apps {
		if _, err := ensureAppSymlink(appsDir, dirName, id, name); err != nil {
			return err
		}
	}
	return nil
}
