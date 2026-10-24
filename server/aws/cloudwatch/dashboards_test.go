package cloudwatch_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// TestSDKDashboardLifecycle drives the aws-sdk-go-v2 client through the
// aws_cloudwatch_dashboard flow: PutDashboard, GetDashboard, ListDashboards
// (with a name prefix), and DeleteDashboards.
func TestSDKDashboardLifecycle(t *testing.T) {
	client, ctx := newCWClient(t)

	body := `{"widgets":[{"type":"metric","x":0,"y":0,"width":6,"height":6}]}`

	if _, err := client.PutDashboard(ctx, &awscw.PutDashboardInput{
		DashboardName: aws.String("ops"),
		DashboardBody: aws.String(body),
	}); err != nil {
		t.Fatalf("PutDashboard: %v", err)
	}

	got, err := client.GetDashboard(ctx, &awscw.GetDashboardInput{DashboardName: aws.String("ops")})
	if err != nil {
		t.Fatalf("GetDashboard: %v", err)
	}

	if aws.ToString(got.DashboardName) != "ops" {
		t.Fatalf("DashboardName = %q, want ops", aws.ToString(got.DashboardName))
	}
	if aws.ToString(got.DashboardBody) != body {
		t.Fatalf("DashboardBody = %q, want %q", aws.ToString(got.DashboardBody), body)
	}
	if aws.ToString(got.DashboardArn) == "" {
		t.Fatal("DashboardArn is empty")
	}

	// A second dashboard so the prefix filter has something to exclude.
	if _, err := client.PutDashboard(ctx, &awscw.PutDashboardInput{
		DashboardName: aws.String("other"),
		DashboardBody: aws.String(`{}`),
	}); err != nil {
		t.Fatalf("PutDashboard other: %v", err)
	}

	list, err := client.ListDashboards(ctx, &awscw.ListDashboardsInput{DashboardNamePrefix: aws.String("ops")})
	if err != nil {
		t.Fatalf("ListDashboards: %v", err)
	}
	if len(list.DashboardEntries) != 1 {
		t.Fatalf("DashboardEntries = %d, want 1 (prefix ops)", len(list.DashboardEntries))
	}
	e := list.DashboardEntries[0]
	if aws.ToString(e.DashboardName) != "ops" {
		t.Fatalf("entry name = %q, want ops", aws.ToString(e.DashboardName))
	}
	if e.LastModified == nil {
		t.Fatal("entry LastModified is nil")
	}
	if aws.ToInt64(e.Size) != int64(len(body)) {
		t.Fatalf("entry Size = %d, want %d", aws.ToInt64(e.Size), len(body))
	}

	if _, err := client.DeleteDashboards(ctx, &awscw.DeleteDashboardsInput{
		DashboardNames: []string{"ops"},
	}); err != nil {
		t.Fatalf("DeleteDashboards: %v", err)
	}

	if _, err := client.GetDashboard(ctx, &awscw.GetDashboardInput{DashboardName: aws.String("ops")}); err == nil {
		t.Fatal("GetDashboard after delete: expected error, got nil")
	}
}

// TestSDKPutDashboardInvalidJSON confirms a non-JSON body is rejected, matching
// CloudWatch's server-side dashboard-body validation.
func TestSDKPutDashboardInvalidJSON(t *testing.T) {
	client, ctx := newCWClient(t)

	_, err := client.PutDashboard(ctx, &awscw.PutDashboardInput{
		DashboardName: aws.String("bad"),
		DashboardBody: aws.String("this is not json"),
	})
	if err == nil {
		t.Fatal("PutDashboard with invalid JSON: expected error, got nil")
	}
}

// TestSDKGetDashboardNotFound confirms an unknown dashboard is a client error.
func TestSDKGetDashboardNotFound(t *testing.T) {
	client, ctx := newCWClient(t)

	if _, err := client.GetDashboard(ctx, &awscw.GetDashboardInput{
		DashboardName: aws.String("missing"),
	}); err == nil {
		t.Fatal("GetDashboard for unknown name: expected error, got nil")
	}
}

// assertDashboardNotFound checks err is the model's DashboardNotFoundError:
// the SDK maps it to types.DashboardNotFoundError, its code is the awsQuery
// code ResourceNotFound and the status is 404, as in the CloudWatch API
// reference and the service model (error code ResourceNotFound, 404).
func assertDashboardNotFound(t *testing.T, op string, err error) {
	t.Helper()

	var nf *cwtypes.DashboardNotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("%s: error %T (%v) is not *types.DashboardNotFoundError", op, err, err)
	}

	if code := nf.ErrorCode(); code != "ResourceNotFound" {
		t.Fatalf("%s: ErrorCode = %q, want ResourceNotFound", op, code)
	}

	var re *awshttp.ResponseError
	if !errors.As(err, &re) || re.HTTPStatusCode() != http.StatusNotFound {
		t.Fatalf("%s: want HTTP 404, got %v", op, err)
	}
}

// TestSDKDashboardNotFoundShape covers GetDashboard and DeleteDashboards on an
// unknown dashboard. DeleteDashboards is all-or-nothing, so the existing
// dashboard named with the missing one must survive.
func TestSDKDashboardNotFoundShape(t *testing.T) {
	client, ctx := newCWClient(t)

	_, err := client.GetDashboard(ctx, &awscw.GetDashboardInput{DashboardName: aws.String("missing")})
	assertDashboardNotFound(t, "GetDashboard", err)

	if _, err := client.PutDashboard(ctx, &awscw.PutDashboardInput{
		DashboardName: aws.String("keep"), DashboardBody: aws.String(`{}`),
	}); err != nil {
		t.Fatalf("PutDashboard: %v", err)
	}

	_, err = client.DeleteDashboards(ctx, &awscw.DeleteDashboardsInput{DashboardNames: []string{"keep", "missing"}})
	assertDashboardNotFound(t, "DeleteDashboards", err)

	if _, err := client.GetDashboard(ctx, &awscw.GetDashboardInput{DashboardName: aws.String("keep")}); err != nil {
		t.Fatalf("GetDashboard keep after failed delete: %v", err)
	}
}
