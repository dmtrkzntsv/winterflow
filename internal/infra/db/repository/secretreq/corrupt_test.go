package secretreq

import (
	"context"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/dbtest"
)

// TestCorruptRowIsAnError: a row whose JSON columns do not decode is an
// error naming the column, never a request read with empty names or
// values (and never "not found").
func TestCorruptRowIsAnError(t *testing.T) {
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org, server)
	dbtest.SeedMember(t, d, user, org)
	r, err := New(d.Tx, d.Driver)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id := dbtest.ID(10)
	create(t, r, id, "hash-1", "", t0)
	if got, err := r.Get(ctx, org, id); err != nil || got.AppID != "" {
		t.Fatalf("a request without an app = %+v, %v", got, err)
	}
	for _, c := range []struct{ column, set string }{
		{"names", "names = 'not json'"},
		{"notes", "names = '[\"API_KEY\"]', notes = '['"},
		{"ciphertexts", "notes = '{}', ciphertexts = '{'"},
	} {
		dbtest.Exec(t, d, "UPDATE secret_requests SET "+c.set+" WHERE token_hash = ?", "hash-1")
		_, err := r.GetByTokenHash(ctx, "hash-1")
		if err == nil || errors.Is(err, model.ErrNotFound) || !strings.Contains(err.Error(), c.column) {
			t.Errorf("corrupt %s = %v", c.column, err)
		}
	}
}

// TestQueryErrorsSurface: a failed query is returned as is.
func TestQueryErrorsSurface(t *testing.T) {
	r := open(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Get(ctx, org, dbtest.ID(10)); err == nil || errors.Is(err, model.ErrNotFound) {
		t.Fatalf("get on a cancelled context = %v", err)
	}
	if _, err := r.Expire(ctx, t0); err == nil {
		t.Fatal("expire on a cancelled context succeeded")
	}
}
