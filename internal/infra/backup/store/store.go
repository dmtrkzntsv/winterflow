// Package store is the object interface over a storage: a folder on the
// server (local) or an S3-compatible bucket (s3). Keys are relative,
// "/"-separated and never escape the storage's folder or prefix.
package store

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"winterflow/internal/domain/model"
)

// Object is one stored object; Key is relative to the storage's folder or
// prefix.
type Object struct {
	Key     string
	Size    int64
	ModTime time.Time
}

// Store reads and writes objects. Every call honors ctx's deadline.
type Store interface {
	// Put streams r into key, replacing it; a reader that fails leaves
	// no object behind.
	Put(ctx context.Context, key string, r io.Reader) error
	// Get opens key for reading; the caller closes it.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// List returns the objects whose key starts with prefix ("" = all),
	// sorted by key.
	List(ctx context.Context, prefix string) ([]Object, error)
	// Delete removes keys; a missing key is not an error.
	Delete(ctx context.Context, keys ...string) error
}

// Open returns the Store for s. secret is the decrypted secret access key
// (s3 only; local ignores it).
func Open(s model.Storage, secret string) (Store, error) {
	s = s.Normalized()
	if err := s.Validate(); err != nil {
		return nil, err
	}
	switch s.Type {
	case model.StorageLocal:
		return openLocal(s.Local.Path)
	default:
		return openS3(*s.S3, secret)
	}
}

// cleanKey checks an object key: relative, "/"-separated, already clean,
// and not "." or "..". Leading-dot names (".winterflow-probe-…") are fine.
func cleanKey(key string) (string, error) {
	if key == "" || strings.HasPrefix(key, "/") || strings.ContainsAny(key, "\\\x00") ||
		path.Clean(key) != key || key == "." || key == ".." || strings.HasPrefix(key, "../") {
		return "", fmt.Errorf("invalid object key %q", key)
	}
	return key, nil
}

// cleanPrefix checks a List prefix: "" or a clean key, optionally ending
// in "/".
func cleanPrefix(prefix string) (string, error) {
	if prefix == "" {
		return "", nil
	}
	if _, err := cleanKey(strings.TrimSuffix(prefix, "/")); err != nil {
		return "", fmt.Errorf("invalid prefix %q", prefix)
	}
	return prefix, nil
}
