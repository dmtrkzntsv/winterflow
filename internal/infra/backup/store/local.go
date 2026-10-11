package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// tmpSuffix marks a Put in progress; List never reports such files.
const tmpSuffix = ".tmp"

// local stores objects as files under root: directories 0700, files 0600,
// each Put written to <key>.tmp, fsynced and renamed into place.
type local struct{ root string }

// openLocal requires root to be an existing folder: winterflow never creates
// it, so an unmounted disk fails here instead of filling the root
// filesystem.
func openLocal(root string) (*local, error) {
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("the folder %s does not exist (is the disk mounted?)", root)
		}
		return nil, fmt.Errorf("cannot open %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a folder", root)
	}
	return &local{root: root}, nil
}

func (l *local) file(key string) string { return filepath.Join(l.root, filepath.FromSlash(key)) }

func (l *local) Put(ctx context.Context, key string, r io.Reader) error {
	key, err := cleanKey(key)
	if err != nil {
		return err
	}
	dst := l.file(key)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp := dst + tmpSuffix
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, ctxReader{ctx, r})
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (l *local) Get(_ context.Context, key string) (io.ReadCloser, error) {
	key, err := cleanKey(key)
	if err != nil {
		return nil, err
	}
	return os.Open(l.file(key))
}

func (l *local) List(ctx context.Context, prefix string) ([]Object, error) {
	prefix, err := cleanPrefix(prefix)
	if err != nil {
		return nil, err
	}
	// Walk only the folder the prefix lives in.
	start := l.root
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		start = l.file(prefix[:i])
	}
	var out []Object
	err = filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() || strings.HasSuffix(p, tmpSuffix) {
			return nil
		}
		rel, err := filepath.Rel(l.root, p)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil // removed meanwhile
		}
		out = append(out, Object{Key: key, Size: info.Size(), ModTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Delete removes the files and then the folders left empty, up to root.
func (l *local) Delete(ctx context.Context, keys ...string) error {
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, err := cleanKey(key)
		if err != nil {
			return err
		}
		if err := os.Remove(l.file(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		for dir := path.Dir(key); dir != "."; dir = path.Dir(dir) {
			if os.Remove(l.file(dir)) != nil {
				break
			}
		}
	}
	return nil
}

// ctxReader stops a copy once ctx is done.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
