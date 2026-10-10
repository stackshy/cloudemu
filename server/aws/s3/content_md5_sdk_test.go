// content_md5_sdk_test.go: real aws-sdk-go-v2 tests for the Content-MD5
// integrity check on PutObject and UploadPart (BadDigest / InvalidDigest).
package s3_test

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // Content-MD5 is MD5 by spec
	"encoding/base64"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

func md5B64(data []byte) string {
	sum := md5.Sum(data) //nolint:gosec // Content-MD5 is MD5 by spec
	return base64.StdEncoding.EncodeToString(sum[:])
}

func TestSDKPutObjectContentMD5(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mustCreateBucket(t, client, "md5")

	body := []byte("hello md5")

	put := func(key, digest string) error {
		_, err := client.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String("md5"), Key: aws.String(key), Body: bytes.NewReader(body), ContentMD5: aws.String(digest),
		})

		return err
	}

	requireAPIError(t, put("bad", md5B64([]byte("other"))), 400, "BadDigest")
	requireNoObject(t, client, "md5", "bad")

	for _, malformed := range []string{"__invalid__", "MTIz", "dGVzdC1zdHJpbmc="} {
		requireAPIError(t, put("malformed", malformed), 400, "InvalidDigest")
	}

	requireNoObject(t, client, "md5", "malformed")

	if err := put("good", md5B64(body)); err != nil {
		t.Fatalf("PutObject with a correct Content-MD5: %v", err)
	}
}

func TestSDKUploadPartContentMD5(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mustCreateBucket(t, client, "md5mp")

	mp, err := client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{Bucket: aws.String("md5mp"), Key: aws.String("k")})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}

	part := []byte("part body")

	upload := func(digest string) error {
		_, err := client.UploadPart(ctx, &awss3.UploadPartInput{
			Bucket: aws.String("md5mp"), Key: aws.String("k"), UploadId: mp.UploadId, PartNumber: aws.Int32(1),
			Body: bytes.NewReader(part), ContentMD5: aws.String(digest),
		})

		return err
	}

	requireAPIError(t, upload(md5B64([]byte("nope"))), 400, "BadDigest")
	requireAPIError(t, upload("not base64 encoded checksum"), 400, "InvalidDigest")

	parts, err := client.ListParts(ctx, &awss3.ListPartsInput{Bucket: aws.String("md5mp"), Key: aws.String("k"), UploadId: mp.UploadId})
	if err != nil {
		t.Fatalf("ListParts: %v", err)
	}

	if len(parts.Parts) != 0 {
		t.Fatalf("rejected UploadPart stored %d parts", len(parts.Parts))
	}

	if err := upload(md5B64(part)); err != nil {
		t.Fatalf("UploadPart with a correct Content-MD5: %v", err)
	}
}
