// Package storage provides a portable storage bucket API with cross-cutting concerns.
package storage

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stackshy/cloudemu/storage/driver"
)

// Bucket is the portable storage type wrapping a driver with cross-cutting concerns.
type Bucket struct {
	driver   driver.Bucket
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewBucket creates a new portable Bucket wrapping the given driver.
func NewBucket(d driver.Bucket, opts ...Option) *Bucket {
	b := &Bucket{driver: d}
	for _, opt := range opts {
		opt(b)
	}

	return b
}

// Option configures a portable Bucket.
type Option func(*Bucket)

// WithRecorder sets the recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(b *Bucket) { b.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(b *Bucket) { b.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(b *Bucket) { b.limiter = l } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(b *Bucket) { b.injector = i } }

// WithLatency sets simulated latency.
func WithLatency(d time.Duration) Option { return func(b *Bucket) { b.latency = d } }

func (b *Bucket) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if b.injector != nil {
		if err := b.injector.Check("storage", op); err != nil {
			b.record(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if b.limiter != nil {
		if err := b.limiter.Allow(); err != nil {
			b.record(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if b.latency > 0 {
		time.Sleep(b.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if b.metrics != nil {
		labels := map[string]string{"service": "storage", "operation": op}
		b.metrics.Counter("calls_total", 1, labels)
		b.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			b.metrics.Counter("errors_total", 1, labels)
		}
	}

	b.record(op, input, out, err, dur)

	return out, err
}

func (b *Bucket) record(op string, input, output any, err error, dur time.Duration) {
	if b.recorder != nil {
		b.recorder.Record("storage", op, input, output, err, dur)
	}
}

// CreateBucket creates a new storage bucket.
func (b *Bucket) CreateBucket(ctx context.Context, name string) error {
	_, err := b.do(ctx, "CreateBucket", name, func() (any, error) {
		return nil, b.driver.CreateBucket(ctx, name)
	})

	return err
}

// DeleteBucket deletes a storage bucket.
func (b *Bucket) DeleteBucket(ctx context.Context, name string) error {
	_, err := b.do(ctx, "DeleteBucket", name, func() (any, error) {
		return nil, b.driver.DeleteBucket(ctx, name)
	})

	return err
}

// ListBuckets lists all buckets.
func (b *Bucket) ListBuckets(ctx context.Context) ([]driver.BucketInfo, error) {
	out, err := b.do(ctx, "ListBuckets", nil, func() (any, error) {
		return b.driver.ListBuckets(ctx)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.BucketInfo), nil
}

// PutObject stores an object in a bucket.
func (b *Bucket) PutObject(ctx context.Context, bucket, key string, data []byte, contentType string, metadata map[string]string) error {
	_, err := b.do(ctx, "PutObject", map[string]string{"bucket": bucket, "key": key}, func() (any, error) {
		return nil, b.driver.PutObject(ctx, bucket, key, data, contentType, metadata)
	})

	return err
}

// GetObject retrieves an object from a bucket.
func (b *Bucket) GetObject(ctx context.Context, bucket, key string) (*driver.Object, error) {
	out, err := b.do(ctx, "GetObject", map[string]string{"bucket": bucket, "key": key}, func() (any, error) {
		return b.driver.GetObject(ctx, bucket, key)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.Object), nil
}

// DeleteObject deletes an object from a bucket.
func (b *Bucket) DeleteObject(ctx context.Context, bucket, key string) error {
	_, err := b.do(ctx, "DeleteObject", map[string]string{"bucket": bucket, "key": key}, func() (any, error) {
		return nil, b.driver.DeleteObject(ctx, bucket, key)
	})

	return err
}

// HeadObject retrieves object metadata without the body.
func (b *Bucket) HeadObject(ctx context.Context, bucket, key string) (*driver.ObjectInfo, error) {
	out, err := b.do(ctx, "HeadObject", map[string]string{"bucket": bucket, "key": key}, func() (any, error) {
		return b.driver.HeadObject(ctx, bucket, key)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.ObjectInfo), nil
}

// ListObjects lists objects in a bucket.
func (b *Bucket) ListObjects(ctx context.Context, bucket string, opts driver.ListOptions) (*driver.ListResult, error) {
	out, err := b.do(ctx, "ListObjects", map[string]any{"bucket": bucket, "opts": opts}, func() (any, error) {
		return b.driver.ListObjects(ctx, bucket, opts)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.ListResult), nil
}

// CopyObject copies an object between buckets.
func (b *Bucket) CopyObject(ctx context.Context, dstBucket, dstKey string, src driver.CopySource) error {
	_, err := b.do(ctx, "CopyObject", map[string]any{"dstBucket": dstBucket, "dstKey": dstKey, "src": src}, func() (any, error) {
		return nil, b.driver.CopyObject(ctx, dstBucket, dstKey, src)
	})

	return err
}

// GeneratePresignedURL generates a presigned URL for an object.
func (b *Bucket) GeneratePresignedURL(ctx context.Context, req driver.PresignedURLRequest) (*driver.PresignedURL, error) {
	out, err := b.do(ctx, "GeneratePresignedURL", req, func() (any, error) {
		return b.driver.GeneratePresignedURL(ctx, req)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.PresignedURL), nil
}

// PutLifecycleConfig sets a lifecycle configuration on a bucket.
func (b *Bucket) PutLifecycleConfig(ctx context.Context, bucket string, config driver.LifecycleConfig) error {
	_, err := b.do(ctx, "PutLifecycleConfig", map[string]any{"bucket": bucket}, func() (any, error) {
		return nil, b.driver.PutLifecycleConfig(ctx, bucket, config)
	})

	return err
}

// GetLifecycleConfig retrieves the lifecycle configuration for a bucket.
func (b *Bucket) GetLifecycleConfig(ctx context.Context, bucket string) (*driver.LifecycleConfig, error) {
	out, err := b.do(ctx, "GetLifecycleConfig", bucket, func() (any, error) {
		return b.driver.GetLifecycleConfig(ctx, bucket)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.LifecycleConfig), nil
}

// EvaluateLifecycle evaluates lifecycle rules and returns keys eligible for expiration.
func (b *Bucket) EvaluateLifecycle(ctx context.Context, bucket string) ([]string, error) {
	out, err := b.do(ctx, "EvaluateLifecycle", bucket, func() (any, error) {
		return b.driver.EvaluateLifecycle(ctx, bucket)
	})
	if err != nil {
		return nil, err
	}

	return out.([]string), nil
}

// CreateMultipartUpload initiates a multipart upload.
func (b *Bucket) CreateMultipartUpload(
	ctx context.Context, bucket, key, contentType string,
) (*driver.MultipartUpload, error) {
	out, err := b.do(ctx, "CreateMultipartUpload", map[string]string{"bucket": bucket, "key": key}, func() (any, error) {
		return b.driver.CreateMultipartUpload(ctx, bucket, key, contentType)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.MultipartUpload), nil
}

// UploadPart uploads a part of a multipart upload.
func (b *Bucket) UploadPart(
	ctx context.Context, bucket, key, uploadID string, partNumber int, data []byte,
) (*driver.UploadPart, error) {
	input := map[string]any{"bucket": bucket, "key": key, "uploadID": uploadID, "partNumber": partNumber}

	out, err := b.do(ctx, "UploadPart", input, func() (any, error) {
		return b.driver.UploadPart(ctx, bucket, key, uploadID, partNumber, data)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.UploadPart), nil
}

// CompleteMultipartUpload completes a multipart upload by assembling all parts.
func (b *Bucket) CompleteMultipartUpload(
	ctx context.Context, bucket, key, uploadID string, parts []driver.UploadPart,
) error {
	input := map[string]any{"bucket": bucket, "key": key, "uploadID": uploadID}
	_, err := b.do(ctx, "CompleteMultipartUpload", input, func() (any, error) {
		return nil, b.driver.CompleteMultipartUpload(ctx, bucket, key, uploadID, parts)
	})

	return err
}

// AbortMultipartUpload aborts a multipart upload and removes all uploaded parts.
func (b *Bucket) AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error {
	input := map[string]any{"bucket": bucket, "key": key, "uploadID": uploadID}
	_, err := b.do(ctx, "AbortMultipartUpload", input, func() (any, error) {
		return nil, b.driver.AbortMultipartUpload(ctx, bucket, key, uploadID)
	})

	return err
}

// ListMultipartUploads lists active multipart uploads for a bucket.
func (b *Bucket) ListMultipartUploads(ctx context.Context, bucket string) ([]driver.MultipartUpload, error) {
	out, err := b.do(ctx, "ListMultipartUploads", bucket, func() (any, error) {
		return b.driver.ListMultipartUploads(ctx, bucket)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.MultipartUpload), nil
}

// SetBucketVersioning enables or disables versioning on a bucket.
func (b *Bucket) SetBucketVersioning(ctx context.Context, bucket string, enabled bool) error {
	input := map[string]any{"bucket": bucket, "enabled": enabled}
	_, err := b.do(ctx, "SetBucketVersioning", input, func() (any, error) {
		return nil, b.driver.SetBucketVersioning(ctx, bucket, enabled)
	})

	return err
}

// GetBucketVersioning returns whether versioning is enabled on a bucket.
func (b *Bucket) GetBucketVersioning(ctx context.Context, bucket string) (bool, error) {
	out, err := b.do(ctx, "GetBucketVersioning", bucket, func() (any, error) {
		return b.driver.GetBucketVersioning(ctx, bucket)
	})
	if err != nil {
		return false, err
	}

	return out.(bool), nil
}

// PutBucketPolicy sets the bucket policy.
func (b *Bucket) PutBucketPolicy(ctx context.Context, bucket string, policy driver.BucketPolicy) error {
	_, err := b.do(ctx, "PutBucketPolicy", bucket, func() (any, error) {
		return nil, b.driver.PutBucketPolicy(ctx, bucket, policy)
	})

	return err
}

// GetBucketPolicy returns the bucket policy.
func (b *Bucket) GetBucketPolicy(ctx context.Context, bucket string) (*driver.BucketPolicy, error) {
	out, err := b.do(ctx, "GetBucketPolicy", bucket, func() (any, error) {
		return b.driver.GetBucketPolicy(ctx, bucket)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.BucketPolicy), nil
}

// DeleteBucketPolicy removes the bucket policy.
func (b *Bucket) DeleteBucketPolicy(ctx context.Context, bucket string) error {
	_, err := b.do(ctx, "DeleteBucketPolicy", bucket, func() (any, error) {
		return nil, b.driver.DeleteBucketPolicy(ctx, bucket)
	})

	return err
}

// PutCORSConfig sets the CORS configuration for a bucket.
func (b *Bucket) PutCORSConfig(ctx context.Context, bucket string, cfg driver.CORSConfig) error {
	_, err := b.do(ctx, "PutCORSConfig", bucket, func() (any, error) {
		return nil, b.driver.PutCORSConfig(ctx, bucket, cfg)
	})

	return err
}

// GetCORSConfig returns the CORS configuration for a bucket.
func (b *Bucket) GetCORSConfig(ctx context.Context, bucket string) (*driver.CORSConfig, error) {
	out, err := b.do(ctx, "GetCORSConfig", bucket, func() (any, error) {
		return b.driver.GetCORSConfig(ctx, bucket)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.CORSConfig), nil
}

// DeleteCORSConfig removes the CORS configuration for a bucket.
func (b *Bucket) DeleteCORSConfig(ctx context.Context, bucket string) error {
	_, err := b.do(ctx, "DeleteCORSConfig", bucket, func() (any, error) {
		return nil, b.driver.DeleteCORSConfig(ctx, bucket)
	})

	return err
}

// PutEncryptionConfig sets the default encryption for a bucket.
func (b *Bucket) PutEncryptionConfig(ctx context.Context, bucket string, cfg driver.EncryptionConfig) error {
	_, err := b.do(ctx, "PutEncryptionConfig", bucket, func() (any, error) {
		return nil, b.driver.PutEncryptionConfig(ctx, bucket, cfg)
	})

	return err
}

// GetEncryptionConfig returns the default encryption for a bucket.
func (b *Bucket) GetEncryptionConfig(ctx context.Context, bucket string) (*driver.EncryptionConfig, error) {
	out, err := b.do(ctx, "GetEncryptionConfig", bucket, func() (any, error) {
		return b.driver.GetEncryptionConfig(ctx, bucket)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.EncryptionConfig), nil
}

// PutObjectTagging sets tags on an object.
func (b *Bucket) PutObjectTagging(ctx context.Context, bucket, key string, tags map[string]string) error {
	_, err := b.do(ctx, "PutObjectTagging", map[string]string{"bucket": bucket, "key": key}, func() (any, error) {
		return nil, b.driver.PutObjectTagging(ctx, bucket, key, tags)
	})

	return err
}

// GetObjectTagging returns tags for an object.
func (b *Bucket) GetObjectTagging(ctx context.Context, bucket, key string) (map[string]string, error) {
	out, err := b.do(ctx, "GetObjectTagging", map[string]string{"bucket": bucket, "key": key}, func() (any, error) {
		return b.driver.GetObjectTagging(ctx, bucket, key)
	})
	if err != nil {
		return nil, err
	}

	return out.(map[string]string), nil
}

// DeleteObjectTagging removes all tags from an object.
func (b *Bucket) DeleteObjectTagging(ctx context.Context, bucket, key string) error {
	_, err := b.do(ctx, "DeleteObjectTagging", map[string]string{"bucket": bucket, "key": key}, func() (any, error) {
		return nil, b.driver.DeleteObjectTagging(ctx, bucket, key)
	})

	return err
}

// PutBucketTagging sets tags on a bucket.
func (b *Bucket) PutBucketTagging(ctx context.Context, bucket string, tags map[string]string) error {
	_, err := b.do(ctx, "PutBucketTagging", bucket, func() (any, error) {
		return nil, b.driver.PutBucketTagging(ctx, bucket, tags)
	})

	return err
}

// GetBucketTagging returns tags for a bucket.
func (b *Bucket) GetBucketTagging(ctx context.Context, bucket string) (map[string]string, error) {
	out, err := b.do(ctx, "GetBucketTagging", bucket, func() (any, error) {
		return b.driver.GetBucketTagging(ctx, bucket)
	})
	if err != nil {
		return nil, err
	}

	return out.(map[string]string), nil
}

// DeleteBucketTagging removes all tags from a bucket.
func (b *Bucket) DeleteBucketTagging(ctx context.Context, bucket string) error {
	_, err := b.do(ctx, "DeleteBucketTagging", bucket, func() (any, error) {
		return nil, b.driver.DeleteBucketTagging(ctx, bucket)
	})

	return err
}
