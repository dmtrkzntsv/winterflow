package store

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
)

func TestCleanKey(t *testing.T) {
	for _, ok := range []string{"a", "a/b.db.zst", ".winterflow-probe-1", "app/.deleted"} {
		if got, err := cleanKey(ok); err != nil || got != ok {
			t.Errorf("%q: %q, %v", ok, got, err)
		}
	}
	for _, bad := range []string{"", "/a", "../a", "..", ".", "a/../b", "a//b", "a/", "a\\b", "a\x00b", "./a"} {
		if _, err := cleanKey(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	for _, ok := range []string{"", "app/", "app/db/", "app"} {
		if _, err := cleanPrefix(ok); err != nil {
			t.Errorf("prefix %q: %v", ok, err)
		}
	}
	for _, bad := range []string{"/", "../", "a//", "/a/"} {
		if _, err := cleanPrefix(bad); err == nil {
			t.Errorf("prefix %q should be refused", bad)
		}
	}
}

func TestOpenValidates(t *testing.T) {
	if _, err := Open(model.Storage{Name: "x", Type: "ftp"}, ""); err == nil {
		t.Fatal("an invalid storage must not open")
	}
	dir := t.TempDir()
	st, err := Open(model.Storage{Name: "x", Type: model.StorageLocal, Local: &model.LocalStorage{Path: dir + "/"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if st.(*local).root != dir {
		t.Fatalf("root = %q, want the normalized %q", st.(*local).root, dir)
	}
}

// failingReader yields some bytes, then an error, the way a dump that dies
// half-way does.
type failingReader struct{ sent bool }

func (f *failingReader) Read(p []byte) (int, error) {
	if !f.sent {
		f.sent = true
		return copy(p, "partial"), nil
	}
	return 0, errors.New("dump died")
}

// exercise runs the contract every driver must keep.
func exercise(t *testing.T, st Store) {
	t.Helper()
	ctx := context.Background()
	for key, body := range map[string]string{
		"app-1/db/20261010T000000Z-scheduled.db.zst": "one",
		"app-1/db/20261011T000000Z-manual.db.zst":    "two!",
		"app-2/files/x.tar.zst":                      "three",
		".winterflow-probe-abc":                      "probe",
	} {
		if err := st.Put(ctx, key, strings.NewReader(body)); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}
	all, err := st.List(ctx, "")
	if err != nil || len(all) != 4 || all[0].Key != ".winterflow-probe-abc" || all[1].Key != "app-1/db/20261010T000000Z-scheduled.db.zst" {
		t.Fatalf("list all = %+v, %v", all, err)
	}
	app1, err := st.List(ctx, "app-1/")
	if err != nil || len(app1) != 2 || app1[1].Size != 4 || app1[1].ModTime.IsZero() {
		t.Fatalf("list app-1/ = %+v, %v", app1, err)
	}
	// A prefix is a key prefix, not only a folder.
	if got, _ := st.List(ctx, "app-1/db/20261011"); len(got) != 1 {
		t.Fatalf("list by partial name = %+v", got)
	}
	if got, err := st.List(ctx, "nothing/"); err != nil || len(got) != 0 {
		t.Fatalf("list missing = %+v, %v", got, err)
	}

	rc, err := st.Get(ctx, "app-1/db/20261011T000000Z-manual.db.zst")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(body) != "two!" {
		t.Fatalf("get = %q", body)
	}
	if _, err := st.Get(ctx, "app-1/missing"); err == nil {
		t.Fatal("get of a missing key must fail")
	}

	// Replacing keeps one object.
	if err := st.Put(ctx, ".winterflow-probe-abc", strings.NewReader("again")); err != nil {
		t.Fatal(err)
	}
	// A reader that fails leaves no object behind.
	if err := st.Put(ctx, "app-3/broken", &failingReader{}); err == nil {
		t.Fatal("a failing reader must fail the put")
	}
	if got, _ := st.List(ctx, "app-3/"); len(got) != 0 {
		t.Fatalf("a failed put left %+v", got)
	}

	if err := st.Delete(ctx, ".winterflow-probe-abc", "app-1/db/20261010T000000Z-scheduled.db.zst", "app-1/never-existed"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.List(ctx, ""); len(got) != 2 {
		t.Fatalf("after delete = %+v", got)
	}

	// Keys never escape the folder or prefix.
	for _, bad := range []string{"../escape", "/abs", "a/../../b"} {
		if err := st.Put(ctx, bad, strings.NewReader("x")); err == nil {
			t.Errorf("put %q must be refused", bad)
		}
		if _, err := st.Get(ctx, bad); err == nil {
			t.Errorf("get %q must be refused", bad)
		}
		if err := st.Delete(ctx, bad); err == nil {
			t.Errorf("delete %q must be refused", bad)
		}
	}
	if _, err := st.List(ctx, "../"); err == nil {
		t.Error("list ../ must be refused")
	}

	// A done context stops every call.
	done, cancel := context.WithCancel(ctx)
	cancel()
	if err := st.Put(done, "late", strings.NewReader("x")); err == nil {
		t.Error("put on a done context must fail")
	}
	if _, err := st.List(done, ""); err == nil {
		t.Error("list on a done context must fail")
	}
	if err := st.Delete(done, "app-2/files/x.tar.zst"); err == nil {
		t.Error("delete on a done context must fail")
	}
}
