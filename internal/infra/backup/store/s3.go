package store

import (
	"context"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"winterflow/internal/domain/model"
)

// partSize is the multipart chunk of a streamed Put. minio-go buffers one
// part in memory; without an explicit size it would size parts for a 5 TiB
// object (hundreds of MiB). 16 MiB × 10,000 parts caps an object at ~160 GiB.
var partSize uint64 = 16 << 20

// s3 stores objects under prefix in bucket.
type s3 struct {
	client *minio.Client
	bucket string
	prefix string
}

func openS3(c model.S3Storage, secret string) (*s3, error) {
	u, err := url.Parse(c.Endpoint)
	if err != nil {
		return nil, err
	}
	lookup := minio.BucketLookupDNS
	if c.PathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(u.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(c.AccessKeyID, secret, ""),
		Secure:       u.Scheme == "https",
		Region:       c.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, err
	}
	return &s3{client: client, bucket: c.Bucket, prefix: c.Prefix}, nil
}

func (s *s3) Put(ctx context.Context, key string, r io.Reader) error {
	key, err := cleanKey(key)
	if err != nil {
		return err
	}
	_, err = s.client.PutObject(ctx, s.bucket, s.prefix+key, r, -1, minio.PutObjectOptions{
		PartSize:    partSize,
		ContentType: "application/octet-stream",
		// No per-chunk payload signing: over https minio-go already sends
		// UNSIGNED-PAYLOAD (TLS protects the body), and hashing every chunk
		// costs CPU the target boxes don't have. Plain http is allowed only
		// for loopback/private endpoints.
		DisableContentSha256: true,
	})
	return err
}

func (s *s3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	key, err := cleanKey(key)
	if err != nil {
		return nil, err
	}
	obj, err := s.client.GetObject(ctx, s.bucket, s.prefix+key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	// GetObject is lazy: stat now so a missing key fails here, not on Read.
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		return nil, err
	}
	return obj, nil
}

func (s *s3) List(ctx context.Context, prefix string) ([]Object, error) {
	prefix, err := cleanPrefix(prefix)
	if err != nil {
		return nil, err
	}
	var out []Object
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: s.prefix + prefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		key, ok := strings.CutPrefix(obj.Key, s.prefix)
		if !ok || key == "" {
			continue
		}
		out = append(out, Object{Key: key, Size: obj.Size, ModTime: obj.LastModified})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (s *s3) Delete(ctx context.Context, keys ...string) error {
	for _, key := range keys {
		key, err := cleanKey(key)
		if err != nil {
			return err
		}
		err = s.client.RemoveObject(ctx, s.bucket, s.prefix+key, minio.RemoveObjectOptions{})
		if err != nil && minio.ToErrorResponse(err).Code != "NoSuchKey" {
			return err
		}
	}
	return nil
}
