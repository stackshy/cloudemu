// pagination_sdk_test.go — real aws-sdk-go-v2 journeys asserting the EFS
// Describe* endpoints paginate: MaxItems/MaxResults caps the page, the
// Marker/NextToken cursor walks a stable order so the SDK paginator visits
// every resource exactly once (no duplicate, no skip) and terminates, a single
// page under the cap emits no cursor, and a malformed cursor is a BadRequest.
package efs_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsefs "github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/aws/smithy-go"
)

const paginationPageSize = 2

// assertBadRequest fails unless err is an EFS BadRequest API error.
func assertBadRequest(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("want BadRequest, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "BadRequest" {
		t.Fatalf("want BadRequest, got %T: %v", err, err)
	}
}

func TestSDKDescribeFileSystemsPaginatorWalksAll(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	want := map[string]bool{}

	for _, tok := range []string{"fs-a", "fs-b", "fs-c", "fs-d", "fs-e"} {
		fs, err := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String(tok)})
		if err != nil {
			t.Fatalf("CreateFileSystem %s: %v", tok, err)
		}

		want[aws.ToString(fs.FileSystemId)] = true
	}

	seen := map[string]int{}

	p := awsefs.NewDescribeFileSystemsPaginator(c, &awsefs.DescribeFileSystemsInput{},
		func(o *awsefs.DescribeFileSystemsPaginatorOptions) { o.Limit = paginationPageSize })
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		if len(out.FileSystems) > paginationPageSize {
			t.Fatalf("page has %d items, want <= %d", len(out.FileSystems), paginationPageSize)
		}

		for i := range out.FileSystems {
			seen[aws.ToString(out.FileSystems[i].FileSystemId)]++
		}
	}

	assertSeenEachOnce(t, want, seen)
}

func TestSDKDescribeFileSystemsSinglePageNoCursor(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	if _, err := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("only")}); err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	out, err := c.DescribeFileSystems(ctx, &awsefs.DescribeFileSystemsInput{})
	if err != nil {
		t.Fatalf("DescribeFileSystems: %v", err)
	}

	if len(out.FileSystems) != 1 {
		t.Fatalf("want 1 file system, got %d", len(out.FileSystems))
	}

	if aws.ToString(out.NextMarker) != "" {
		t.Fatalf("NextMarker = %q, want empty on a single page", aws.ToString(out.NextMarker))
	}
}

func TestSDKDescribeFileSystemsInvalidMarker(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	_, err := c.DescribeFileSystems(ctx, &awsefs.DescribeFileSystemsInput{Marker: aws.String("!!not-base64!!")})
	assertBadRequest(t, err)
}

func TestSDKDescribeMountTargetsPaginatorWalksAll(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	fs, err := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("mt-fs")})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	fsID := aws.ToString(fs.FileSystemId)
	want := map[string]bool{}

	for i := 0; i < 5; i++ {
		mt, err := c.CreateMountTarget(ctx, &awsefs.CreateMountTargetInput{
			FileSystemId: aws.String(fsID),
			SubnetId:     aws.String(fmt.Sprintf("subnet-0abcd1234ef56780%d", i)),
		})
		if err != nil {
			t.Fatalf("CreateMountTarget %d: %v", i, err)
		}

		want[aws.ToString(mt.MountTargetId)] = true
	}

	seen := map[string]int{}

	p := awsefs.NewDescribeMountTargetsPaginator(c,
		&awsefs.DescribeMountTargetsInput{FileSystemId: aws.String(fsID)},
		func(o *awsefs.DescribeMountTargetsPaginatorOptions) { o.Limit = paginationPageSize })
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		if len(out.MountTargets) > paginationPageSize {
			t.Fatalf("page has %d items, want <= %d", len(out.MountTargets), paginationPageSize)
		}

		for i := range out.MountTargets {
			seen[aws.ToString(out.MountTargets[i].MountTargetId)]++
		}
	}

	assertSeenEachOnce(t, want, seen)
}

func TestSDKDescribeMountTargetsInvalidMarker(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	fs, err := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("mt-bad")})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	_, err = c.DescribeMountTargets(ctx, &awsefs.DescribeMountTargetsInput{
		FileSystemId: fs.FileSystemId, Marker: aws.String("%%bad%%"),
	})
	assertBadRequest(t, err)
}

func TestSDKDescribeAccessPointsPaginatorWalksAll(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	fs, err := c.CreateFileSystem(ctx, &awsefs.CreateFileSystemInput{CreationToken: aws.String("ap-fs")})
	if err != nil {
		t.Fatalf("CreateFileSystem: %v", err)
	}

	fsID := aws.ToString(fs.FileSystemId)
	want := map[string]bool{}

	for i := 0; i < 5; i++ {
		ap, err := c.CreateAccessPoint(ctx, &awsefs.CreateAccessPointInput{FileSystemId: aws.String(fsID)})
		if err != nil {
			t.Fatalf("CreateAccessPoint %d: %v", i, err)
		}

		want[aws.ToString(ap.AccessPointId)] = true
	}

	seen := map[string]int{}

	p := awsefs.NewDescribeAccessPointsPaginator(c,
		&awsefs.DescribeAccessPointsInput{FileSystemId: aws.String(fsID)},
		func(o *awsefs.DescribeAccessPointsPaginatorOptions) { o.Limit = paginationPageSize })
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		if len(out.AccessPoints) > paginationPageSize {
			t.Fatalf("page has %d items, want <= %d", len(out.AccessPoints), paginationPageSize)
		}

		for i := range out.AccessPoints {
			seen[aws.ToString(out.AccessPoints[i].AccessPointId)]++
		}
	}

	assertSeenEachOnce(t, want, seen)
}

func TestSDKDescribeAccessPointsInvalidNextToken(t *testing.T) {
	ctx := context.Background()
	c := newEFSClient(t)

	_, err := c.DescribeAccessPoints(ctx, &awsefs.DescribeAccessPointsInput{NextToken: aws.String("!!bad!!")})
	assertBadRequest(t, err)
}

// assertSeenEachOnce checks the paginator visited exactly the wanted ids, each
// exactly once — proving stable-sorted offset paging neither duplicates nor
// skips an item across page boundaries.
func assertSeenEachOnce(t *testing.T, want map[string]bool, seen map[string]int) {
	t.Helper()

	if len(seen) != len(want) {
		t.Fatalf("saw %d distinct ids, want %d (%v)", len(seen), len(want), seen)
	}

	for id := range want {
		switch seen[id] {
		case 1:
		case 0:
			t.Fatalf("id %s was skipped", id)
		default:
			t.Fatalf("id %s seen %d times (duplicated)", id, seen[id])
		}
	}
}
