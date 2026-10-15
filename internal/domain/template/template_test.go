package template

import (
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/internal/domain/model"
)

// notesCompose is the spec's example: a required URL with a pattern, a
// required secret, an optional language with a default, a secret only the
// overrides use, an http and a tcp entry, and a files backup.
const notesCompose = `services:
  app:
    image: ghcr.io/example/notes:${NOTES_TAG:-latest}
    environment:
      # Public URL the browser uses
      BASE_URL: "http://localhost:8080"
      SESSION_SECRET: "changeme"
      LANGUAGE: "en"
      SMTP_HOST: ${SMTP_HOST}
    volumes:
      - data:/var/lib/notes
  db:
    image: postgres:16
    environment:
      POSTGRES_PASSWORD: notes
    volumes:
      - pg:/var/lib/postgresql/data
volumes:
  data:
  pg:
x-winterflow:
  expose:
    - { container: app, port: 8080 }
    - { container: db, port: 5432, protocol: tcp }
  variables:
    BASE_URL: { required: true, pattern: "^https?://.+" }
    SESSION_SECRET: { required: true, encrypted: true }
    LANGUAGE: { pattern: "^[a-z]{2}$" }
    DB_PASSWORD: { encrypted: true }
  overrides:
    services:
      db:
        environment:
          POSTGRES_PASSWORD: ${DB_PASSWORD}
  backups:
    - { name: db, type: files, volume: pg }
`

// notes is a body with notesCompose.
func notes() Body {
	return Body{Files: map[string]string{"compose.yaml": notesCompose}, Summary: "Notes", Icon: "note", Color: "#112233"}
}

// spec is notes' parsed block.
func spec(t *testing.T) xwinterflow.Spec {
	t.Helper()
	s, err := Validate(notes())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func wantInvalid(t *testing.T, err error, field string) {
	t.Helper()
	if !errors.Is(err, model.ErrRejected) {
		t.Fatalf("want a 400 naming %s, got %v", field, err)
	}
	if !strings.Contains(err.Error(), field) {
		t.Fatalf("error %q does not name %s", err, field)
	}
}
