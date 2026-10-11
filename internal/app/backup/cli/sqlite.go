package cli

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite" // the pure-Go "sqlite" driver: the binary stays static
)

// busyTimeout bounds how long the copy waits for the app's writer.
const busyTimeout = 30 * time.Second

// dumpSQLite copies the database at path with VACUUM INTO (a consistent
// snapshot that, in WAL mode, doesn't block the app's writer) into a temp
// file in tmpDir, checks it, and writes it to stdout. The fingerprint is
// the size and mtime of the database and its -wal file, taken before the
// copy: equal to since means unchanged.
func dumpSQLite(path, since, tmpDir string, stdout io.Writer) (Result, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Result{}, fmt.Errorf("database file not found: %s: %w", path, errMissing)
	}
	if err != nil {
		return Result{}, err
	}
	if !info.Mode().IsRegular() {
		return Result{}, fmt.Errorf("%s is not a file", path)
	}
	fp := sqliteFingerprint(path, info)
	if since != "" && fp == since {
		return Result{}, errUnchanged
	}
	// The helper runs as root; open the database as its owner, so the -wal
	// and -shm files SQLite may create belong to the app, not to root.
	if err := becomeOwner(info); err != nil {
		return Result{}, err
	}
	snap, err := os.CreateTemp(tmpDir, "wf-snap-*.db")
	if err != nil {
		return Result{}, err
	}
	snapPath := snap.Name()
	_ = snap.Close()
	_ = os.Remove(snapPath) // VACUUM INTO needs a new file
	defer os.Remove(snapPath)
	if err := vacuumInto(path, snapPath); err != nil {
		return Result{}, err
	}
	if err := checkSnapshot(snapPath); err != nil {
		return Result{}, err
	}
	f, err := os.Open(snapPath)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	n, err := io.Copy(stdout, f)
	if err != nil {
		return Result{}, fmt.Errorf("write the copy: %w", err)
	}
	return Result{Fingerprint: fp, RawSize: n}, nil
}

// sqliteFingerprint hashes the size and mtime of the database and its -wal
// file (absent: "-").
func sqliteFingerprint(path string, info fs.FileInfo) string {
	parts := []string{fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())}
	if wal, err := os.Stat(path + "-wal"); err == nil {
		parts = append(parts, fmt.Sprintf("%d:%d", wal.Size(), wal.ModTime().UnixNano()))
	} else {
		parts = append(parts, "-")
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "/")))
	return "sqlite:" + hex.EncodeToString(sum[:16])
}

// sqliteDSN opens an existing file (mode=rw: never creates one) with the
// busy timeout.
func sqliteDSN(path string, extra ...string) string {
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()))
	for _, e := range extra {
		k, v, _ := strings.Cut(e, "=")
		q.Add(k, v)
	}
	escaped := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(filepath.Clean(path))
	return "file:" + escaped + "?" + q.Encode()
}

func vacuumInto(path, dst string) error {
	db, err := sql.Open("sqlite", sqliteDSN(path, "mode=rw"))
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	if _, err := db.ExecContext(ctx, "VACUUM INTO '"+strings.ReplaceAll(dst, "'", "''")+"'"); err != nil {
		return fmt.Errorf("copy the database: %w", err)
	}
	return nil
}

// checkSnapshot requires an intact copy with at least one schema object.
func checkSnapshot(path string) error {
	db, err := sql.Open("sqlite", sqliteDSN(path, "mode=ro"))
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&result); err != nil {
		return fmt.Errorf("check the copy: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("the copy failed its integrity check: %s", result)
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master").Scan(&n); err != nil {
		return fmt.Errorf("check the copy: %w", err)
	}
	if n == 0 {
		return errors.New("the database is empty (no tables)")
	}
	return nil
}

// becomeOwner drops root for the file's owner. A non-root process (tests,
// a rootless helper) keeps its identity.
func becomeOwner(info fs.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || os.Geteuid() != 0 || st.Uid == 0 {
		return nil
	}
	if err := syscall.Setgroups(nil); err != nil {
		return fmt.Errorf("drop supplementary groups: %w", err)
	}
	if err := syscall.Setgid(int(st.Gid)); err != nil {
		return fmt.Errorf("switch to group %d: %w", st.Gid, err)
	}
	if err := syscall.Setuid(int(st.Uid)); err != nil {
		return fmt.Errorf("switch to user %d: %w", st.Uid, err)
	}
	return nil
}
