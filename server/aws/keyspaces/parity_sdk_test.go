package keyspaces_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskeyspaces "github.com/aws/aws-sdk-go-v2/service/keyspaces"
	kstypes "github.com/aws/aws-sdk-go-v2/service/keyspaces/types"

	"github.com/stackshy/cloudemu/v2/config"
	ksprovider "github.com/stackshy/cloudemu/v2/providers/aws/keyspaces"
	ksserver "github.com/stackshy/cloudemu/v2/server/aws/keyspaces"
)

// Tagging a keyspace/table ARN that doesn't exist is ResourceNotFoundException.
func TestSDKTagsOnMissingResource(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateKeyspace(ctx, &awskeyspaces.CreateKeyspaceInput{KeyspaceName: aws.String("app")}); err != nil {
		t.Fatalf("CreateKeyspace: %v", err)
	}

	ghost := aws.String("arn:aws:cassandra:us-east-1:123456789012:/keyspace/ghost/")
	tags := []kstypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}}

	var rnf *kstypes.ResourceNotFoundException

	_, err := client.TagResource(ctx, &awskeyspaces.TagResourceInput{ResourceArn: ghost, Tags: tags})
	if !errors.As(err, &rnf) {
		t.Fatalf("TagResource: got %v, want ResourceNotFoundException", err)
	}

	_, err = client.UntagResource(ctx, &awskeyspaces.UntagResourceInput{ResourceArn: ghost, Tags: tags})
	if !errors.As(err, &rnf) {
		t.Fatalf("UntagResource: got %v, want ResourceNotFoundException", err)
	}

	_, err = client.ListTagsForResource(ctx, &awskeyspaces.ListTagsForResourceInput{ResourceArn: ghost})
	if !errors.As(err, &rnf) {
		t.Fatalf("ListTagsForResource: got %v, want ResourceNotFoundException", err)
	}

	var ve *kstypes.ValidationException

	_, err = client.TagResource(ctx, &awskeyspaces.TagResourceInput{
		ResourceArn: aws.String("arn:aws:dynamodb:us-east-1:123456789012:table/x"), Tags: tags,
	})
	if !errors.As(err, &ve) {
		t.Fatalf("TagResource non-cassandra ARN: got %v, want ValidationException", err)
	}
}

// Tag restrictions over the wire: reserved aws: prefix, empty value, and more
// than 60 tags in one request are ValidationException; the system_schema_mcs
// keyspace exists and can't be deleted.
func TestSDKTagRestrictionsAndSystemKeyspace(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateKeyspace(ctx, &awskeyspaces.CreateKeyspaceInput{KeyspaceName: aws.String("app")})
	if err != nil {
		t.Fatalf("CreateKeyspace: %v", err)
	}

	var ve *kstypes.ValidationException

	for _, tag := range []kstypes.Tag{
		{Key: aws.String("aws:owner"), Value: aws.String("v")},
		{Key: aws.String("k"), Value: aws.String("")},
	} {
		_, err = client.TagResource(ctx, &awskeyspaces.TagResourceInput{ResourceArn: out.ResourceArn, Tags: []kstypes.Tag{tag}})
		if !errors.As(err, &ve) {
			t.Fatalf("TagResource %q: got %v, want ValidationException", aws.ToString(tag.Key), err)
		}
	}

	many := make([]kstypes.Tag, 61)
	for i := range many {
		many[i] = kstypes.Tag{Key: aws.String("k" + strconv.Itoa(i)), Value: aws.String("v")}
	}

	_, err = client.TagResource(ctx, &awskeyspaces.TagResourceInput{ResourceArn: out.ResourceArn, Tags: many})
	if !errors.As(err, &ve) {
		t.Fatalf("TagResource 61 tags: got %v, want ValidationException", err)
	}

	_, err = client.CreateKeyspace(ctx, &awskeyspaces.CreateKeyspaceInput{KeyspaceName: aws.String("app2"), Tags: many})
	if !errors.As(err, &ve) {
		t.Fatalf("CreateKeyspace 61 tags: got %v, want ValidationException", err)
	}

	if _, err := client.GetKeyspace(ctx, &awskeyspaces.GetKeyspaceInput{KeyspaceName: aws.String("system_schema_mcs")}); err != nil {
		t.Fatalf("GetKeyspace system_schema_mcs: %v", err)
	}

	_, err = client.DeleteKeyspace(ctx, &awskeyspaces.DeleteKeyspaceInput{KeyspaceName: aws.String("system_schema_mcs")})
	if !errors.As(err, &ve) {
		t.Fatalf("DeleteKeyspace system_schema_mcs: got %v, want ValidationException", err)
	}
}

// Keyspace/table names must be 1-48 chars matching [a-zA-Z0-9][a-zA-Z0-9_]*;
// a missing parent keyspace is ResourceNotFoundException.
func TestSDKNameValidationAndMissingKeyspace(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	var ve *kstypes.ValidationException

	for _, n := range []string{strings.Repeat("a", 49), "bad-name", "_lead"} {
		_, err := client.CreateKeyspace(ctx, &awskeyspaces.CreateKeyspaceInput{KeyspaceName: aws.String(n)})
		if !errors.As(err, &ve) {
			t.Fatalf("CreateKeyspace(%q): got %v, want ValidationException", n, err)
		}
	}

	if _, err := client.CreateKeyspace(ctx, &awskeyspaces.CreateKeyspaceInput{KeyspaceName: aws.String("1app")}); err != nil {
		t.Fatalf("CreateKeyspace leading digit: %v", err)
	}

	_, err := client.CreateTable(ctx, &awskeyspaces.CreateTableInput{
		KeyspaceName: aws.String("1app"), TableName: aws.String(strings.Repeat("t", 49)), SchemaDefinition: sampleSchema(),
	})
	if !errors.As(err, &ve) {
		t.Fatalf("CreateTable too-long name: got %v, want ValidationException", err)
	}

	var rnf *kstypes.ResourceNotFoundException

	_, err = client.CreateTable(ctx, &awskeyspaces.CreateTableInput{
		KeyspaceName: aws.String("ghost"), TableName: aws.String("t"), SchemaDefinition: sampleSchema(),
	})
	if !errors.As(err, &rnf) {
		t.Fatalf("CreateTable missing keyspace: got %v, want ResourceNotFoundException", err)
	}
}

// GetTable on a PAY_PER_REQUEST table without CDC omits readCapacityUnits,
// writeCapacityUnits, and cdcSpecification.viewType; PROVISIONED keeps them.
func TestGetTableOmitsUnsetFields(t *testing.T) {
	opts := config.NewOptions(config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))
	h := ksserver.New(ksprovider.New(opts))

	call := func(op, body string) map[string]any {
		t.Helper()

		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("X-Amz-Target", "KeyspacesService."+op)
		req.Header.Set("Content-Type", "application/x-amz-json-1.0")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", op, rec.Code, rec.Body.String())
		}

		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: decode: %v", op, err)
		}

		return out
	}

	schema := `"schemaDefinition":{"allColumns":[{"name":"id","type":"int"}],"partitionKeys":[{"name":"id"}]}`

	call("CreateKeyspace", `{"keyspaceName":"app"}`)
	call("CreateTable", `{"keyspaceName":"app","tableName":"ppr",`+schema+`}`)
	call("CreateTable", `{"keyspaceName":"app","tableName":"prov",`+schema+
		`,"capacitySpecification":{"throughputMode":"PROVISIONED","readCapacityUnits":5,"writeCapacityUnits":7}}`)

	ppr := call("GetTable", `{"keyspaceName":"app","tableName":"ppr"}`)

	capSpec, _ := ppr["capacitySpecification"].(map[string]any)
	if capSpec["throughputMode"] != "PAY_PER_REQUEST" {
		t.Fatalf("ppr throughputMode: %v", capSpec)
	}

	for _, k := range []string{"readCapacityUnits", "writeCapacityUnits"} {
		if _, ok := capSpec[k]; ok {
			t.Fatalf("ppr capacitySpecification has %s: %v", k, capSpec)
		}
	}

	cdc, _ := ppr["cdcSpecification"].(map[string]any)
	if cdc["status"] != "DISABLED" {
		t.Fatalf("cdc status: %v", cdc)
	}

	if _, ok := cdc["viewType"]; ok {
		t.Fatalf("cdcSpecification has empty viewType: %v", cdc)
	}

	prov := call("GetTable", `{"keyspaceName":"app","tableName":"prov"}`)

	pcap, _ := prov["capacitySpecification"].(map[string]any)
	if pcap["readCapacityUnits"] != float64(5) || pcap["writeCapacityUnits"] != float64(7) {
		t.Fatalf("provisioned capacity: %v", pcap)
	}
}
