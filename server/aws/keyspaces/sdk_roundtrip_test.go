package keyspaces_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awskeyspaces "github.com/aws/aws-sdk-go-v2/service/keyspaces"
	kstypes "github.com/aws/aws-sdk-go-v2/service/keyspaces/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2/config"
	ksprovider "github.com/stackshy/cloudemu/v2/providers/aws/keyspaces"
	ksserver "github.com/stackshy/cloudemu/v2/server/aws/keyspaces"
)

func newSDKClient(t *testing.T) *awskeyspaces.Client {
	t.Helper()

	opts := config.NewOptions(config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))
	srv := httptest.NewServer(ksserver.New(ksprovider.New(opts)))
	t.Cleanup(srv.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awskeyspaces.NewFromConfig(cfg, func(o *awskeyspaces.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
	})
}

func sampleSchema() *kstypes.SchemaDefinition {
	return &kstypes.SchemaDefinition{
		AllColumns: []kstypes.ColumnDefinition{
			{Name: aws.String("id"), Type: aws.String("uuid")},
			{Name: aws.String("ts"), Type: aws.String("timestamp")},
			{Name: aws.String("val"), Type: aws.String("text")},
		},
		PartitionKeys:  []kstypes.PartitionKey{{Name: aws.String("id")}},
		ClusteringKeys: []kstypes.ClusteringKey{{Name: aws.String("ts"), OrderBy: kstypes.SortOrderDesc}},
	}
}

func TestSDKKeyspaceLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateKeyspace(ctx, &awskeyspaces.CreateKeyspaceInput{
		KeyspaceName: aws.String("app"),
		Tags:         []kstypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	}); err != nil {
		t.Fatalf("CreateKeyspace: %v", err)
	}

	got, err := client.GetKeyspace(ctx, &awskeyspaces.GetKeyspaceInput{KeyspaceName: aws.String("app")})
	if err != nil {
		t.Fatalf("GetKeyspace: %v", err)
	}

	if aws.ToString(got.KeyspaceName) != "app" || got.ReplicationStrategy != kstypes.RsSingleRegion {
		t.Fatalf("keyspace wrong: %+v", got)
	}

	list, err := client.ListKeyspaces(ctx, &awskeyspaces.ListKeyspacesInput{})
	if err != nil {
		t.Fatalf("ListKeyspaces: %v", err)
	}

	if len(list.Keyspaces) != 4 { // 3 system + app
		t.Fatalf("expected 4 keyspaces, got %d", len(list.Keyspaces))
	}

	if _, err := client.DeleteKeyspace(ctx, &awskeyspaces.DeleteKeyspaceInput{KeyspaceName: aws.String("app")}); err != nil {
		t.Fatalf("DeleteKeyspace: %v", err)
	}

	_, err = client.GetKeyspace(ctx, &awskeyspaces.GetKeyspaceInput{KeyspaceName: aws.String("app")})

	var nf *kstypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("get after delete: got %v, want ResourceNotFoundException", err)
	}
}

func TestSDKTableLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateKeyspace(ctx, &awskeyspaces.CreateKeyspaceInput{KeyspaceName: aws.String("app")}); err != nil {
		t.Fatalf("CreateKeyspace: %v", err)
	}

	if _, err := client.CreateTable(ctx, &awskeyspaces.CreateTableInput{
		KeyspaceName:     aws.String("app"),
		TableName:        aws.String("events"),
		SchemaDefinition: sampleSchema(),
		Comment:          &kstypes.Comment{Message: aws.String("event log")},
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	got, err := client.GetTable(ctx, &awskeyspaces.GetTableInput{
		KeyspaceName: aws.String("app"), TableName: aws.String("events"),
	})
	if err != nil {
		t.Fatalf("GetTable: %v", err)
	}

	if got.Status != kstypes.TableStatusActive || len(got.SchemaDefinition.PartitionKeys) != 1 {
		t.Fatalf("table wrong: status=%s schema=%+v", got.Status, got.SchemaDefinition)
	}

	if got.CapacitySpecification == nil || got.CapacitySpecification.ThroughputMode != kstypes.ThroughputModePayPerRequest {
		t.Fatalf("capacity wrong: %+v", got.CapacitySpecification)
	}

	if aws.ToString(got.Comment.Message) != "event log" {
		t.Fatalf("comment lost: %+v", got.Comment)
	}

	if got.CreationTimestamp == nil || got.CreationTimestamp.IsZero() {
		t.Fatalf("CreationTimestamp missing: %+v", got.CreationTimestamp)
	}

	// Update: enable PITR + switch to provisioned.
	if _, err := client.UpdateTable(ctx, &awskeyspaces.UpdateTableInput{
		KeyspaceName:        aws.String("app"),
		TableName:           aws.String("events"),
		PointInTimeRecovery: &kstypes.PointInTimeRecovery{Status: kstypes.PointInTimeRecoveryStatusEnabled},
		CapacitySpecification: &kstypes.CapacitySpecification{
			ThroughputMode: kstypes.ThroughputModeProvisioned, ReadCapacityUnits: aws.Int64(5), WriteCapacityUnits: aws.Int64(5),
		},
	}); err != nil {
		t.Fatalf("UpdateTable: %v", err)
	}

	got, _ = client.GetTable(ctx, &awskeyspaces.GetTableInput{KeyspaceName: aws.String("app"), TableName: aws.String("events")})
	if got.PointInTimeRecovery.Status != kstypes.PointInTimeRecoveryStatusEnabled ||
		got.CapacitySpecification.ThroughputMode != kstypes.ThroughputModeProvisioned {
		t.Fatalf("update not applied: %+v %+v", got.PointInTimeRecovery, got.CapacitySpecification)
	}

	// Restore into a new table.
	if _, err := client.RestoreTable(ctx, &awskeyspaces.RestoreTableInput{
		SourceKeyspaceName: aws.String("app"), SourceTableName: aws.String("events"),
		TargetKeyspaceName: aws.String("app"), TargetTableName: aws.String("events_restored"),
		RestoreTimestamp: aws.Time(time.Unix(1735689600, 0)),
	}); err != nil {
		t.Fatalf("RestoreTable: %v", err)
	}

	if _, err := client.DeleteTable(ctx, &awskeyspaces.DeleteTableInput{
		KeyspaceName: aws.String("app"), TableName: aws.String("events"),
	}); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}
}

func TestSDKUserDefinedTypes(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateKeyspace(ctx, &awskeyspaces.CreateKeyspaceInput{KeyspaceName: aws.String("app")}); err != nil {
		t.Fatalf("CreateKeyspace: %v", err)
	}

	if _, err := client.CreateType(ctx, &awskeyspaces.CreateTypeInput{
		KeyspaceName: aws.String("app"), TypeName: aws.String("address"),
		FieldDefinitions: []kstypes.FieldDefinition{
			{Name: aws.String("street"), Type: aws.String("text")},
			{Name: aws.String("zip"), Type: aws.String("int")},
		},
	}); err != nil {
		t.Fatalf("CreateType: %v", err)
	}

	got, err := client.GetType(ctx, &awskeyspaces.GetTypeInput{KeyspaceName: aws.String("app"), TypeName: aws.String("address")})
	if err != nil {
		t.Fatalf("GetType: %v", err)
	}

	if len(got.FieldDefinitions) != 2 || got.Status != kstypes.TypeStatusActive {
		t.Fatalf("type wrong: %+v", got)
	}

	types, err := client.ListTypes(ctx, &awskeyspaces.ListTypesInput{KeyspaceName: aws.String("app")})
	if err != nil {
		t.Fatalf("ListTypes: %v", err)
	}

	if len(types.Types) != 1 || types.Types[0] != "address" {
		t.Fatalf("list types wrong: %+v", types.Types)
	}

	if _, err := client.DeleteType(ctx, &awskeyspaces.DeleteTypeInput{
		KeyspaceName: aws.String("app"), TypeName: aws.String("address"),
	}); err != nil {
		t.Fatalf("DeleteType: %v", err)
	}
}

func TestSDKTagsAndAutoScaling(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateKeyspace(ctx, &awskeyspaces.CreateKeyspaceInput{KeyspaceName: aws.String("app")}); err != nil {
		t.Fatalf("CreateKeyspace: %v", err)
	}

	created, err := client.CreateTable(ctx, &awskeyspaces.CreateTableInput{
		KeyspaceName: aws.String("app"), TableName: aws.String("t"), SchemaDefinition: sampleSchema(),
		CapacitySpecification: &kstypes.CapacitySpecification{ThroughputMode: kstypes.ThroughputModeProvisioned},
		AutoScalingSpecification: &kstypes.AutoScalingSpecification{
			ReadCapacityAutoScaling: &kstypes.AutoScalingSettings{
				MinimumUnits: aws.Int64(1), MaximumUnits: aws.Int64(10),
				ScalingPolicy: &kstypes.AutoScalingPolicy{
					TargetTrackingScalingPolicyConfiguration: &kstypes.TargetTrackingScalingPolicyConfiguration{TargetValue: 70},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	arn := aws.ToString(created.ResourceArn)
	if _, err := client.TagResource(ctx, &awskeyspaces.TagResourceInput{
		ResourceArn: aws.String(arn), Tags: []kstypes.Tag{{Key: aws.String("team"), Value: aws.String("data")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := client.ListTagsForResource(ctx, &awskeyspaces.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(tags.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(tags.Tags))
	}

	as, err := client.GetTableAutoScalingSettings(ctx, &awskeyspaces.GetTableAutoScalingSettingsInput{
		KeyspaceName: aws.String("app"), TableName: aws.String("t"),
	})
	if err != nil {
		t.Fatalf("GetTableAutoScalingSettings: %v", err)
	}

	if as.AutoScalingSpecification == nil || as.AutoScalingSpecification.ReadCapacityAutoScaling == nil ||
		aws.ToInt64(as.AutoScalingSpecification.ReadCapacityAutoScaling.MaximumUnits) != 10 {
		t.Fatalf("autoscaling not returned: %+v", as.AutoScalingSpecification)
	}
}

func TestSDKPaginationAndFaults(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateKeyspace(ctx, &awskeyspaces.CreateKeyspaceInput{KeyspaceName: aws.String("app")}); err != nil {
		t.Fatalf("CreateKeyspace: %v", err)
	}

	for _, n := range []string{"t1", "t2", "t3"} {
		if _, err := client.CreateTable(ctx, &awskeyspaces.CreateTableInput{
			KeyspaceName: aws.String("app"), TableName: aws.String(n), SchemaDefinition: sampleSchema(),
		}); err != nil {
			t.Fatalf("CreateTable %s: %v", n, err)
		}
	}

	first, err := client.ListTables(ctx, &awskeyspaces.ListTablesInput{KeyspaceName: aws.String("app"), MaxResults: aws.Int32(2)})
	if err != nil {
		t.Fatalf("ListTables page 1: %v", err)
	}

	if len(first.Tables) != 2 || first.NextToken == nil {
		t.Fatalf("page 1: got %d tables, token=%v", len(first.Tables), first.NextToken)
	}

	second, err := client.ListTables(ctx, &awskeyspaces.ListTablesInput{
		KeyspaceName: aws.String("app"), MaxResults: aws.Int32(2), NextToken: first.NextToken,
	})
	if err != nil {
		t.Fatalf("ListTables page 2: %v", err)
	}

	if len(second.Tables) != 1 || second.NextToken != nil {
		t.Fatalf("page 2: got %d tables, token=%v", len(second.Tables), second.NextToken)
	}

	// Duplicate keyspace → ConflictException.
	_, err = client.CreateKeyspace(ctx, &awskeyspaces.CreateKeyspaceInput{KeyspaceName: aws.String("app")})

	var conflict *kstypes.ConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("duplicate keyspace: got %v, want ConflictException", err)
	}

	// Bad token → ValidationException.
	_, err = client.ListTables(ctx, &awskeyspaces.ListTablesInput{
		KeyspaceName: aws.String("app"), NextToken: aws.String("!!bad!!"),
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationException" {
		t.Fatalf("bad token: got %v, want ValidationException", err)
	}
}

func TestSDKUnknownOperation(t *testing.T) {
	opts := config.NewOptions(config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))
	h := ksserver.New(ksprovider.New(opts))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Amz-Target", "KeyspacesService.CreateKeyspace")

	if !h.Matches(req) {
		t.Fatal("expected Matches to claim a KeyspacesService target")
	}

	other := httptest.NewRequest(http.MethodPost, "/", nil)
	other.Header.Set("X-Amz-Target", "AmazonMemoryDB.CreateCluster")

	if h.Matches(other) {
		t.Fatal("expected Matches to reject a non-Keyspaces target")
	}
}
