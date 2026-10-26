package keyspaces_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskeyspaces "github.com/aws/aws-sdk-go-v2/service/keyspaces"
	kstypes "github.com/aws/aws-sdk-go-v2/service/keyspaces/types"

	"github.com/stackshy/cloudemu/v2/config"
	ksprovider "github.com/stackshy/cloudemu/v2/providers/aws/keyspaces"
	ksserver "github.com/stackshy/cloudemu/v2/server/aws/keyspaces"
)

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

// GetTable reports ttl and clientSideTimestamps only once they are enabled,
// so a Terraform aws_keyspaces_table that sets neither reads back no block
// and plans no replacement.
func TestSDKGetTableOmitsUnsetTTLAndClientSideTimestamps(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateKeyspace(ctx, &awskeyspaces.CreateKeyspaceInput{KeyspaceName: aws.String("app")}); err != nil {
		t.Fatalf("CreateKeyspace: %v", err)
	}

	if _, err := client.CreateTable(ctx, &awskeyspaces.CreateTableInput{
		KeyspaceName: aws.String("app"), TableName: aws.String("plain"), SchemaDefinition: sampleSchema(),
	}); err != nil {
		t.Fatalf("CreateTable plain: %v", err)
	}

	if _, err := client.CreateTable(ctx, &awskeyspaces.CreateTableInput{
		KeyspaceName: aws.String("app"), TableName: aws.String("on"), SchemaDefinition: sampleSchema(),
		Ttl:                  &kstypes.TimeToLive{Status: kstypes.TimeToLiveStatusEnabled},
		ClientSideTimestamps: &kstypes.ClientSideTimestamps{Status: kstypes.ClientSideTimestampsStatusEnabled},
	}); err != nil {
		t.Fatalf("CreateTable on: %v", err)
	}

	plain, err := client.GetTable(ctx, &awskeyspaces.GetTableInput{KeyspaceName: aws.String("app"), TableName: aws.String("plain")})
	if err != nil {
		t.Fatalf("GetTable plain: %v", err)
	}

	if plain.Ttl != nil || plain.ClientSideTimestamps != nil {
		t.Fatalf("plain table: ttl=%+v clientSideTimestamps=%+v, want both omitted", plain.Ttl, plain.ClientSideTimestamps)
	}

	if plain.PointInTimeRecovery == nil || plain.PointInTimeRecovery.Status != kstypes.PointInTimeRecoveryStatusDisabled {
		t.Fatalf("plain table pointInTimeRecovery: %+v", plain.PointInTimeRecovery)
	}

	on, err := client.GetTable(ctx, &awskeyspaces.GetTableInput{KeyspaceName: aws.String("app"), TableName: aws.String("on")})
	if err != nil {
		t.Fatalf("GetTable on: %v", err)
	}

	if on.Ttl == nil || on.Ttl.Status != kstypes.TimeToLiveStatusEnabled ||
		on.ClientSideTimestamps == nil || on.ClientSideTimestamps.Status != kstypes.ClientSideTimestampsStatusEnabled {
		t.Fatalf("enabled table: ttl=%+v clientSideTimestamps=%+v", on.Ttl, on.ClientSideTimestamps)
	}
}
