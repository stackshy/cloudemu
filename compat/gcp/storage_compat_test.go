package gcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"google.golang.org/api/iterator"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestGCPStorageCompat drives a GCS bucket + object lifecycle through the real
// cloud.google.com/go/storage client. GCS buckets/objects map onto the
// portable "storage" driver, so operation names match S3's in
// docs/coverage/coverage.json.
func TestGCPStorageCompat(t *testing.T) {
	provider := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{Storage: provider.GCS})
	ctx := context.Background()

	client, err := sess.StorageClient(ctx)
	if err != nil {
		t.Fatalf("storage client: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	const (
		svc    = "storage"
		bucket = "compat-bucket"
		object = "greeting.txt"
	)

	body := []byte("hello cloudemu")
	bkt := client.Bucket(bucket)

	sess.Op(svc, "CreateBucket", func() error {
		return bkt.Create(ctx, compat.GCPProject, nil)
	})

	sess.Op(svc, "ListBuckets", func() error {
		it := client.Buckets(ctx, compat.GCPProject)

		_, err := it.Next()
		if err != nil && !errors.Is(err, iterator.Done) {
			return err
		}

		return nil
	})

	sess.Op(svc, "PutObject", func() error {
		w := bkt.Object(object).NewWriter(ctx)
		if _, err := w.Write(body); err != nil {
			return err
		}

		return w.Close()
	})

	sess.Op(svc, "GetObject", func() error {
		r, err := bkt.Object(object).NewReader(ctx)
		if err != nil {
			return err
		}
		defer r.Close()

		got, err := io.ReadAll(r)
		if err != nil {
			return err
		}

		if !bytes.Equal(got, body) {
			return fmt.Errorf("object round-trip mismatch: got %q want %q", got, body)
		}

		return nil
	})

	sess.Op(svc, "HeadObject", func() error {
		_, err := bkt.Object(object).Attrs(ctx)
		return err
	})

	sess.Op(svc, "ListObjects", func() error {
		it := bkt.Objects(ctx, nil)
		count := 0

		for {
			_, err := it.Next()
			if errors.Is(err, iterator.Done) {
				break
			}

			if err != nil {
				return err
			}

			count++
		}

		if count != 1 {
			return fmt.Errorf("expected 1 object, got %d", count)
		}

		return nil
	})

	sess.Op(svc, "DeleteObject", func() error {
		return bkt.Object(object).Delete(ctx)
	})

	sess.Op(svc, "DeleteBucket", func() error {
		return bkt.Delete(ctx)
	})
}
