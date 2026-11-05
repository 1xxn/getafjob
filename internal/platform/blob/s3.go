package blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3 stores objects in a bucket of an S3-compatible service (AWS, MinIO, Garage, Hetzner, …).
type S3 struct {
	client         *minio.Client
	bucket, prefix string
}

var _ Store = (*S3)(nil)

// newS3 reads s3://bucket/prefix?endpoint=https://host[:port]&region=r. HTTPS is required unless the endpoint is
// plain http on a host without dots (a service in the Compose network).
func newS3(u *url.URL, creds S3Credentials) (*S3, error) {
	bucket := u.Host
	if bucket == "" {
		return nil, errors.New("blob URL: s3:// needs a bucket")
	}
	prefix := strings.Trim(u.Path, "/")
	if prefix != "" && !ValidKey(prefix) {
		return nil, errors.New("blob URL: invalid prefix")
	}
	ep, err := url.Parse(u.Query().Get("endpoint"))
	if err != nil || ep.Host == "" || (ep.Scheme != "https" && (ep.Scheme != "http" || strings.Contains(ep.Hostname(), "."))) {
		return nil, errors.New("blob URL: endpoint must be https://host (http only for a local service name)")
	}
	if creds.AccessKey == "" || creds.SecretKey == "" {
		return nil, errors.New("blob URL: s3 needs GETAFJOB_BLOB_ACCESS_KEY and GETAFJOB_BLOB_SECRET_KEY")
	}
	c, err := newMinio(ep.Host, ep.Scheme == "https", u.Query().Get("region"), creds)
	if err != nil {
		return nil, errors.New("blob URL: invalid s3 settings")
	}
	return &S3{client: c, bucket: bucket, prefix: prefix}, nil
}

func newMinio(host string, secure bool, region string, creds S3Credentials) (*minio.Client, error) {
	return minio.New(host, &minio.Options{
		Creds:        credentials.NewStaticV4(creds.AccessKey.Reveal(), creds.SecretKey.Reveal(), ""),
		Secure:       secure,
		Region:       region,
		BucketLookup: minio.BucketLookupPath,
	})
}

func (s *S3) name(key string) string { return path.Join(s.prefix, key) }

// Put implements Store.
func (s *S3) Put(ctx context.Context, key string, data []byte) error {
	if !ValidKey(key) {
		return ErrBadKey
	}
	if len(data) > MaxObject {
		return ErrTooLarge
	}
	_, err := s.client.PutObject(ctx, s.bucket, s.name(key), bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"})
	return err
}

// Get implements Store.
func (s *S3) Get(ctx context.Context, key string) ([]byte, error) {
	if !ValidKey(key) {
		return nil, ErrBadKey
	}
	obj, err := s.client.GetObject(ctx, s.bucket, s.name(key), minio.GetObjectOptions{})
	if err != nil {
		return nil, notFound(err)
	}
	defer func() { _ = obj.Close() }()
	b, err := io.ReadAll(io.LimitReader(obj, MaxObject+1))
	if err != nil {
		return nil, notFound(err)
	}
	if len(b) > MaxObject {
		return nil, ErrTooLarge
	}
	return b, nil
}

// Delete implements Store.
func (s *S3) Delete(ctx context.Context, key string) error {
	if !ValidKey(key) {
		return ErrBadKey
	}
	err := notFound(s.client.RemoveObject(ctx, s.bucket, s.name(key), minio.RemoveObjectOptions{}))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func notFound(err error) error {
	if err == nil {
		return nil
	}
	if r := minio.ToErrorResponse(err); r.StatusCode == http.StatusNotFound || r.Code == "NoSuchKey" {
		return ErrNotFound
	}
	return err
}
