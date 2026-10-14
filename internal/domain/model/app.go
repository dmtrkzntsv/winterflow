package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrAppNotFound is returned when an app id has no matching row.
var ErrAppNotFound = errors.New("app not found")

// ValidAppID reports whether id is safe to use as a single path element
// under the agent's apps-data dir (command.ValidAppID): a legit id is a
// generated UUID, so anything empty, "."/"..", or containing a path
// separator (or NUL) is refused rather than normalised.
func ValidAppID(id string) bool {
	switch id {
	case "", ".", "..":
		return false
	}
	return !strings.ContainsAny(id, "/\\\x00")
}

// ValidateAppID is ValidAppID as an error.
func ValidateAppID(id string) error {
	if !ValidAppID(id) {
		return fmt.Errorf("invalid app id %q", id)
	}
	return nil
}

type App struct {
	ID         string `json:"id"`
	ServerID   string `json:"server_id"`
	Version    string `json:"version"`
	TemplateID string `json:"template_id"`
	Name       string `json:"name"`
	Icon       string `json:"icon"`
	Color      string `json:"color"`
	// Ingress is parsed straight out of the committed config.json when the
	// agent lists apps (ListApps unmarshals the raw blob into App), letting
	// the API reconcile the app_domains index without a second command.
	Ingress   *Ingress  `json:"ingress,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
