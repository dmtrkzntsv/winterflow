package store

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"winterflow/internal/domain/model"
)

// fakeS3 runs an in-process S3 with one bucket and returns its endpoint.
func fakeS3(t *testing.T, bucket string) (string, *s3mem.Backend) {
	t.Helper()
	backend := s3mem.New()
	if err := backend.CreateBucket(bucket); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(gofakes3.New(backend).Server())
	t.Cleanup(srv.Close)
	return srv.URL, backend
}

func openFake(t *testing.T, endpoint, prefix string) Store {
	t.Helper()
	st, err := Open(model.Storage{Name: "fake", Type: model.StorageS3, S3: &model.S3Storage{
		Endpoint: endpoint, Region: "us-east-1", Bucket: "backups", Prefix: prefix,
		AccessKeyID: "AKIA", SecretAccessKey: "unused-ciphertext", PathStyle: true,
	}}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestS3Store(t *testing.T) {
	endpoint, _ := fakeS3(t, "backups")
	exercise(t, openFake(t, endpoint, "tenants"))
}

// TestS3StorePrefixConfinement: two prefixes in one bucket never see each
// other's objects, and keys come back relative to the prefix.
func TestS3StorePrefixConfinement(t *testing.T) {
	endpoint, backend := fakeS3(t, "backups")
	ctx := context.Background()
	a, b, root := openFake(t, endpoint, "a/"), openFake(t, endpoint, "b"), openFake(t, endpoint, "")
	if err := a.Put(ctx, "x", strings.NewReader("in a")); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, "x", strings.NewReader("in b")); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.List(ctx, ""); len(got) != 1 || got[0].Key != "x" {
		t.Fatalf("a sees %+v", got)
	}
	if got, _ := root.List(ctx, ""); len(got) != 2 || got[0].Key != "a/x" || got[1].Key != "b/x" {
		t.Fatalf("the bucket root sees %+v", got)
	}
	obj, err := backend.HeadObject("backups", "b/x")
	if err != nil || obj.Size != 4 {
		t.Fatalf("stored under the prefix: %+v, %v", obj, err)
	}
	if err := a.Delete(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.List(ctx, ""); len(got) != 1 {
		t.Fatalf("deleting in a touched b: %+v", got)
	}
}

// TestS3StoreMultipart streams an object of unknown size larger than one
// part.
func TestS3StoreMultipart(t *testing.T) {
	old := partSize
	partSize = 5 << 20 // S3's minimum part size
	t.Cleanup(func() { partSize = old })
	endpoint, _ := fakeS3(t, "backups")
	st := openFake(t, endpoint, "")
	ctx := context.Background()
	data := bytes.Repeat([]byte("0123456789abcdef"), (11<<20)/16) // 11 MiB: three parts
	stream := io.MultiReader(bytes.NewReader(data))               // hides the length
	if err := st.Put(ctx, "big.tar.zst", stream); err != nil {
		t.Fatal(err)
	}
	rc, err := st.Get(ctx, "big.tar.zst")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, data) {
		t.Fatalf("round trip: %d bytes, want %d", len(got), len(data))
	}
}

func TestS3StoreErrors(t *testing.T) {
	endpoint, _ := fakeS3(t, "backups")
	ctx := context.Background()
	// A bucket that does not exist fails every call.
	st, err := Open(model.Storage{Name: "fake", Type: model.StorageS3, S3: &model.S3Storage{
		Endpoint: endpoint, Bucket: "nope", AccessKeyID: "A", SecretAccessKey: "c", PathStyle: true,
	}}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Put(ctx, "x", strings.NewReader("x")); err == nil {
		t.Error("put into a missing bucket must fail")
	}
	if _, err := st.List(ctx, ""); err == nil {
		t.Error("list of a missing bucket must fail")
	}
	if err := st.Delete(ctx, "x"); err == nil {
		t.Error("delete in a missing bucket must fail")
	}
	// Virtual-hosted style is the default when path_style is off.
	vh, err := Open(model.Storage{Name: "fake", Type: model.StorageS3, S3: &model.S3Storage{
		Endpoint: endpoint, Bucket: "backups", AccessKeyID: "A", SecretAccessKey: "c",
	}}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if vh.(*s3).client.EndpointURL().Host == "" {
		t.Fatal("no endpoint")
	}
}
