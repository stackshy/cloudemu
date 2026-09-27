package athena_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsathena "github.com/aws/aws-sdk-go-v2/service/athena"
	athenatypes "github.com/aws/aws-sdk-go-v2/service/athena/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const sdkLambdaARN = "arn:aws:lambda:us-east-1:123456789012:function:conn"

func requireInvalidRequest(t *testing.T, err error, contains string) {
	t.Helper()

	var ire *athenatypes.InvalidRequestException
	if !errors.As(err, &ire) || !strings.Contains(ire.ErrorMessage(), contains) {
		t.Fatalf("error = %v, want InvalidRequestException containing %q", err, contains)
	}
}

func TestSDKDataCatalogLifecycle(t *testing.T) {
	ctx := context.Background()
	ath, _ := newAthenaGlueClients(t)

	created, err := ath.CreateDataCatalog(ctx, &awsathena.CreateDataCatalogInput{
		Name:        aws.String("lam"),
		Type:        athenatypes.DataCatalogTypeLambda,
		Description: aws.String("one"),
		Parameters:  map[string]string{"function": sdkLambdaARN},
		Tags:        []athenatypes.Tag{{Key: aws.String("env"), Value: aws.String("dev")}},
	})
	if err != nil {
		t.Fatalf("CreateDataCatalog: %v", err)
	}

	if created.DataCatalog.Status != athenatypes.DataCatalogStatusCreateComplete {
		t.Fatalf("created status = %q", created.DataCatalog.Status)
	}

	if _, err := ath.UpdateDataCatalog(ctx, &awsathena.UpdateDataCatalogInput{
		Name: aws.String("lam"), Type: athenatypes.DataCatalogTypeLambda, Description: aws.String("two"),
	}); err != nil {
		t.Fatalf("UpdateDataCatalog: %v", err)
	}

	got, err := ath.GetDataCatalog(ctx, &awsathena.GetDataCatalogInput{Name: aws.String("lam")})
	if err != nil {
		t.Fatalf("GetDataCatalog: %v", err)
	}

	if aws.ToString(got.DataCatalog.Description) != "two" || got.DataCatalog.Parameters["function"] != sdkLambdaARN {
		t.Fatalf("got = %+v", got.DataCatalog)
	}

	tags, err := ath.ListTagsForResource(ctx, &awsathena.ListTagsForResourceInput{
		ResourceARN: aws.String("arn:aws:athena:us-east-1:123456789012:datacatalog/lam"),
	})
	if err != nil || len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "env" {
		t.Fatalf("tags = %+v, %v", tags, err)
	}

	list, err := ath.ListDataCatalogs(ctx, &awsathena.ListDataCatalogsInput{})
	if err != nil {
		t.Fatalf("ListDataCatalogs: %v", err)
	}

	if len(list.DataCatalogsSummary) != 2 || aws.ToString(list.DataCatalogsSummary[0].CatalogName) != "AwsDataCatalog" ||
		list.DataCatalogsSummary[1].Status != athenatypes.DataCatalogStatusCreateComplete {
		t.Fatalf("list = %+v", list.DataCatalogsSummary)
	}

	if _, err := ath.DeleteDataCatalog(ctx, &awsathena.DeleteDataCatalogInput{Name: aws.String("lam")}); err != nil {
		t.Fatalf("DeleteDataCatalog: %v", err)
	}

	_, err = ath.GetDataCatalog(ctx, &awsathena.GetDataCatalogInput{Name: aws.String("lam")})
	requireInvalidRequest(t, err, "was not found")

	_, err = ath.DeleteDataCatalog(ctx, &awsathena.DeleteDataCatalogInput{Name: aws.String("lam")})
	requireInvalidRequest(t, err, "was not found")
}

func TestSDKDataCatalogErrors(t *testing.T) {
	ctx := context.Background()
	ath, _ := newAthenaGlueClients(t)

	_, err := ath.DeleteDataCatalog(ctx, &awsathena.DeleteDataCatalogInput{Name: aws.String("AwsDataCatalog")})
	requireInvalidRequest(t, err, "cannot be deleted")

	_, err = ath.UpdateDataCatalog(ctx, &awsathena.UpdateDataCatalogInput{
		Name: aws.String("AwsDataCatalog"), Type: athenatypes.DataCatalogTypeGlue,
		Parameters: map[string]string{"catalog-id": "123456789012"},
	})
	requireInvalidRequest(t, err, "cannot be modified")

	_, err = ath.CreateDataCatalog(ctx, &awsathena.CreateDataCatalogInput{
		Name: aws.String("g"), Type: athenatypes.DataCatalogTypeGlue,
	})
	requireInvalidRequest(t, err, "catalog-id")

	fed, err := ath.CreateDataCatalog(ctx, &awsathena.CreateDataCatalogInput{
		Name: aws.String("fed"), Type: athenatypes.DataCatalogTypeFederated,
		Parameters: map[string]string{"connection-arn": "arn:aws:glue:us-east-1:123456789012:connection/ghost"},
	})
	if err != nil {
		t.Fatalf("create fed: %v", err)
	}

	if fed.DataCatalog.Status != athenatypes.DataCatalogStatusCreateFailed || aws.ToString(fed.DataCatalog.Error) == "" {
		t.Fatalf("fed = %+v", fed.DataCatalog)
	}
}

// TestDataCatalogParametersAlwaysPresent checks the raw body, since the SDK
// decodes a missing map and an empty one the same way.
func TestDataCatalogParametersAlwaysPresent(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{Athena: cloud.Athena}))
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL,
		strings.NewReader(`{"Name":"AwsDataCatalog"}`))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("X-Amz-Target", "AmazonAthena.GetDataCatalog")
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"Parameters":{}`) {
		t.Fatalf("body = %s", body)
	}
}
