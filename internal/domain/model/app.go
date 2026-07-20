package model

import (
	"errors"
	"time"
)

// ErrAppNotFound is returned when an app id has no matching row.
var ErrAppNotFound = errors.New("app not found")

type App struct {
	ID         string    `json:"id"`
	ServerID   string    `json:"server_id"`
	Version    string    `json:"version"`
	TemplateID string    `json:"template_id"`
	Name       string    `json:"name"`
	Icon       string    `json:"icon"`
	Color      string    `json:"color"`
	CreatedAt  time.Time `json:"created_at"`
}
