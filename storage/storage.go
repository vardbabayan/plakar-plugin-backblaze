/*
 * Plakar storage connector for Backblaze B2, via its S3-compatible API
 */

package storage

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"

	"github.com/PlakarKorp/kloset/connectors/storage"
	"github.com/PlakarKorp/kloset/location"
	"github.com/PlakarKorp/kloset/objects"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type Store struct {
	client *s3.Client

	bufPool sync.Pool

	endpoint  string
	bucket    string
	prefixDir string
}

func init() {
	storage.Register("b2", 0, NewStore)
}

func NewStore(ctx context.Context, proto string, storeConfig map[string]string) (storage.Store, error) {
	cfg, err := parseConfig(storeConfig)
	if err != nil {
		return nil, err
	}

	client := newClient(cfg)

	return &Store{
		client: client,
		bufPool: sync.Pool{
			New: func() any { return &bytes.Buffer{} },
		},
		endpoint:  cfg.endpoint,
		bucket:    cfg.bucket,
		prefixDir: cfg.prefixDir,
	}, nil
}

func (s *Store) realpath(p string) string {
	return strings.TrimPrefix(s.prefixDir+p, "/")
}

// key maps a (resource, MAC) pair onto an object key
func (s *Store) key(res storage.StorageResource, mac objects.MAC) (string, error) {
	switch res {
	case storage.StorageResourcePackfile:
		return s.realpath(fmt.Sprintf("packfiles/%02x/%016x", mac[0], mac)), nil
	case storage.StorageResourceState:
		return s.realpath(fmt.Sprintf("states/%02x/%016x", mac[0], mac)), nil
	case storage.StorageResourceLock:
		return s.realpath(fmt.Sprintf("locks/%016x", mac)), nil
	default:
		return "", errors.ErrUnsupported
	}
}

func isNotFound(err error) bool {
	var noSuchKey *types.NoSuchKey
	var notFound *types.NotFound
	if errors.As(err, &noSuchKey) || errors.As(err, &notFound) {
		return true
	}
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) && respErr.HTTPStatusCode() == 404 {
		return true
	}
	return false
}

func (s *Store) Create(ctx context.Context, config []byte) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	if err != nil {
		if !isNotFound(err) {
			return fmt.Errorf("check if bucket exists: %w", err)
		}
		if _, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket: aws.String(s.bucket),
		}); err != nil {
			return fmt.Errorf("make bucket: %w", err)
		}
	}

	configKey := s.realpath("CONFIG")
	_, err = s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(configKey),
	})
	if err == nil {
		return fmt.Errorf("bucket already initialized")
	}
	if !isNotFound(err) {
		return fmt.Errorf("stat object CONFIG: %w", err)
	}

	if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(configKey),
		Body:          bytes.NewReader(config),
		ContentLength: aws.Int64(int64(len(config))),
	}); err != nil {
		return fmt.Errorf("put object CONFIG: %w", err)
	}

	return nil
}

func (s *Store) Open(ctx context.Context) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.realpath("CONFIG")),
	})
	if err != nil {
		return nil, fmt.Errorf("get object CONFIG: %w", err)
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("read object CONFIG: %w", err)
	}
	return data, nil
}

func (s *Store) Ping(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	if err != nil {
		if isNotFound(err) {
			return fmt.Errorf("bucket does not exist")
		}
		return err
	}
	return nil
}

func (s *Store) Origin() string        { return s.endpoint }
func (s *Store) Type() string          { return "b2" }
func (s *Store) Root() string          { return path.Join("/", s.bucket, s.prefixDir) }
func (s *Store) Flags() location.Flags { return 0 }

func (s *Store) Mode(ctx context.Context) (storage.Mode, error) {
	return storage.ModeRead | storage.ModeWrite, nil
}

func (s *Store) Size(ctx context.Context) (int64, error) {
	return -1, nil
}

func (s *Store) List(ctx context.Context, res storage.StorageResource) ([]objects.MAC, error) {
	var prefix string
	var prefixSize int

	switch res {
	case storage.StorageResourcePackfile:
		prefix = s.realpath("packfiles/")
		prefixSize = len(prefix) + 3 // + len("xx/")
	case storage.StorageResourceState:
		prefix = s.realpath("states/")
		prefixSize = len(prefix) + 3 // + len("xx/")
	case storage.StorageResourceLock:
		prefix = s.realpath("locks/")
		prefixSize = len(prefix)
	default:
		return nil, errors.ErrUnsupported
	}

	ret := make([]objects.MAC, 0)
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list %s objects: %w", res, err)
		}
		for _, object := range page.Contents {
			key := aws.ToString(object.Key)
			if !strings.HasPrefix(key, prefix) || len(key) < prefixSize {
				continue
			}
			mac, err := hex.DecodeString(key[prefixSize:])
			if err != nil || len(mac) != 32 {
				continue
			}
			ret = append(ret, objects.MAC(mac))
		}
	}
	return ret, nil
}

// Put buffers the whole body before uploading.
// One packfile (~64 MiB) held per in-flight Put
func (s *Store) Put(ctx context.Context, res storage.StorageResource, mac objects.MAC, rd io.Reader) (int64, error) {
	key, err := s.key(res, mac)
	if err != nil {
		return -1, err
	}

	buf := s.bufPool.Get().(*bytes.Buffer)
	defer func() {
		buf.Reset()
		s.bufPool.Put(buf)
	}()

	n, err := io.Copy(buf, rd)
	if err != nil {
		return 0, fmt.Errorf("read %s object: %w", res, err)
	}

	if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(buf.Bytes()),
		ContentLength: aws.Int64(n),
	}); err != nil {
		return 0, fmt.Errorf("put %s object: %w", res, err)
	}
	return n, nil
}

type padReader struct {
	rc        io.ReadCloser
	remaining int64
	eof       bool
}

func (p *padReader) Read(b []byte) (int, error) {
	if p.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(b)) > p.remaining {
		b = b[:p.remaining]
	}

	if !p.eof {
		n, err := p.rc.Read(b)
		p.remaining -= int64(n)
		if err == nil {
			return n, nil
		}
		if err != io.EOF {
			return n, err
		}
		p.eof = true
		if n > 0 {
			return n, nil
		}
	}

	for i := range b {
		b[i] = 0
	}
	p.remaining -= int64(len(b))
	return len(b), nil
}

func (p *padReader) Close() error { return p.rc.Close() }

func (s *Store) Get(ctx context.Context, res storage.StorageResource, mac objects.MAC, rg *storage.Range) (io.ReadCloser, error) {
	key, err := s.key(res, mac)
	if err != nil {
		return nil, err
	}

	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}
	if rg != nil {
		last := rg.Offset + uint64(rg.Length) - 1
		input.Range = aws.String(fmt.Sprintf("bytes=%d-%d", rg.Offset, last))
	}

	out, err := s.client.GetObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("get %s object: %w", res, err)
	}

	if rg != nil {
		return &padReader{rc: out.Body, remaining: int64(rg.Length)}, nil
	}
	return out.Body, nil
}

func (s *Store) Delete(ctx context.Context, res storage.StorageResource, mac objects.MAC) error {
	key, err := s.key(res, mac)
	if err != nil {
		return err
	}

	// Idempotent deletes
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}); err != nil && !isNotFound(err) {
		return fmt.Errorf("remove %s object: %w", res, err)
	}
	return nil
}

func (s *Store) Close(ctx context.Context) error {
	return nil
}
