package secretsmanager_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newClockedSecretsClient returns an SDK client bound to a wire server whose
// AWS provider runs on a fake clock, so tests can space out CreatedDate /
// LastAccessedDate deterministically.
func newClockedSecretsClient(t *testing.T) (*awssm.Client, *config.FakeClock) {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 3, 10, 9, 30, 0, 0, time.UTC))
	cloud := cloudemu.NewAWS(config.WithClock(fc))
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{SecretsManager: cloud.SecretsManager}))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awssm.NewFromConfig(cfg, func(o *awssm.Options) { o.BaseEndpoint = aws.String(ts.URL) }), fc
}

func mustCreate(t *testing.T, c *awssm.Client, in *awssm.CreateSecretInput) *awssm.CreateSecretOutput {
	t.Helper()

	out, err := c.CreateSecret(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateSecret %s: %v", aws.ToString(in.Name), err)
	}

	return out
}

func TestSDKListSecretsIncludePlannedDeletion(t *testing.T) {
	client, _ := newClockedSecretsClient(t)
	ctx := context.Background()

	mustCreate(t, client, &awssm.CreateSecretInput{Name: aws.String("live"), SecretString: aws.String("x")})
	mustCreate(t, client, &awssm.CreateSecretInput{Name: aws.String("gone"), SecretString: aws.String("x")})

	if _, err := client.DeleteSecret(ctx, &awssm.DeleteSecretInput{SecretId: aws.String("gone")}); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	def, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{})
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}

	if got := secretNames(def.SecretList); len(got) != 1 || got[0] != "live" {
		t.Fatalf("default ListSecrets = %v, want [live]", got)
	}

	all, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{IncludePlannedDeletion: aws.Bool(true)})
	if err != nil {
		t.Fatalf("ListSecrets IncludePlannedDeletion: %v", err)
	}

	if len(all.SecretList) != 2 {
		t.Fatalf("IncludePlannedDeletion listed %v, want both secrets", secretNames(all.SecretList))
	}

	for _, e := range all.SecretList {
		deleted := e.DeletedDate != nil
		if wantDeleted := aws.ToString(e.Name) == "gone"; deleted != wantDeleted {
			t.Fatalf("%s DeletedDate set=%v, want %v", aws.ToString(e.Name), deleted, wantDeleted)
		}
	}
}

func TestSDKListSecretsSortOrder(t *testing.T) {
	client, fc := newClockedSecretsClient(t)
	ctx := context.Background()

	// Created b, then c, then a: CreatedDate order differs from name order.
	for _, n := range []string{"b", "c", "a"} {
		mustCreate(t, client, &awssm.CreateSecretInput{Name: aws.String(n), SecretString: aws.String("x")})
		fc.Advance(time.Minute)
	}

	// Change "b" last so last-changed-date order differs again.
	if _, err := client.PutSecretValue(ctx, &awssm.PutSecretValueInput{
		SecretId: aws.String("b"), SecretString: aws.String("y"),
	}); err != nil {
		t.Fatalf("PutSecretValue: %v", err)
	}

	cases := []struct {
		name  string
		in    awssm.ListSecretsInput
		order []string
	}{
		{"created asc (SortOrder only)", awssm.ListSecretsInput{SortOrder: smtypes.SortOrderTypeAsc}, []string{"b", "c", "a"}},
		{"created desc (SortOrder only)", awssm.ListSecretsInput{SortOrder: smtypes.SortOrderTypeDesc}, []string{"a", "c", "b"}},
		{"name desc", awssm.ListSecretsInput{SortBy: smtypes.SortByTypeName, SortOrder: smtypes.SortOrderTypeDesc}, []string{"c", "b", "a"}},
		{"last-changed desc", awssm.ListSecretsInput{
			SortBy: smtypes.SortByTypeLastChangedDate, SortOrder: smtypes.SortOrderTypeDesc,
		}, []string{"b", "a", "c"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in

			out, err := client.ListSecrets(ctx, &in)
			if err != nil {
				t.Fatalf("ListSecrets: %v", err)
			}

			got := secretNames(out.SecretList)
			if len(got) != len(tc.order) {
				t.Fatalf("got %v, want %v", got, tc.order)
			}

			for i := range got {
				if got[i] != tc.order[i] {
					t.Fatalf("got %v, want %v", got, tc.order)
				}
			}
		})
	}

	// Pagination stays consistent with the requested order.
	page1, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{SortOrder: smtypes.SortOrderTypeDesc, MaxResults: aws.Int32(2)})
	if err != nil {
		t.Fatalf("ListSecrets page1: %v", err)
	}

	page2, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{
		SortOrder: smtypes.SortOrderTypeDesc, MaxResults: aws.Int32(2), NextToken: page1.NextToken,
	})
	if err != nil {
		t.Fatalf("ListSecrets page2: %v", err)
	}

	if got := append(secretNames(page1.SecretList), secretNames(page2.SecretList)...); len(got) != 3 || got[2] != "b" {
		t.Fatalf("paged desc order = %v, want [a c b]", got)
	}
}

func TestSDKDescribeSecretAccessAndRegionFields(t *testing.T) {
	client, _ := newClockedSecretsClient(t)
	ctx := context.Background()

	mustCreate(t, client, &awssm.CreateSecretInput{Name: aws.String("s"), SecretString: aws.String("x")})

	before, err := client.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("s")})
	if err != nil {
		t.Fatalf("DescribeSecret: %v", err)
	}

	if before.LastAccessedDate != nil {
		t.Fatalf("LastAccessedDate = %v before any GetSecretValue, want omitted", before.LastAccessedDate)
	}

	if aws.ToString(before.PrimaryRegion) != "us-east-1" {
		t.Fatalf("PrimaryRegion = %q, want us-east-1", aws.ToString(before.PrimaryRegion))
	}

	if before.OwningService != nil {
		t.Fatalf("OwningService = %q, want omitted for a user-created secret", aws.ToString(before.OwningService))
	}

	if _, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("s")}); err != nil {
		t.Fatalf("GetSecretValue: %v", err)
	}

	after, err := client.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("s")})
	if err != nil {
		t.Fatalf("DescribeSecret: %v", err)
	}

	want := time.Date(2025, 3, 10, 0, 0, 0, 0, time.UTC)
	if after.LastAccessedDate == nil || !after.LastAccessedDate.Equal(want) {
		t.Fatalf("LastAccessedDate = %v, want %v (day resolution)", after.LastAccessedDate, want)
	}

	list, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{})
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}

	if e := list.SecretList[0]; e.LastAccessedDate == nil || aws.ToString(e.PrimaryRegion) != "us-east-1" {
		t.Fatalf("ListSecrets entry LastAccessedDate=%v PrimaryRegion=%q", e.LastAccessedDate, aws.ToString(e.PrimaryRegion))
	}
}

func TestSDKListSecretsRegionAndOwningServiceFilters(t *testing.T) {
	client, _ := newClockedSecretsClient(t)
	ctx := context.Background()

	mustCreate(t, client, &awssm.CreateSecretInput{Name: aws.String("s"), SecretString: aws.String("x")})

	cases := []struct {
		key   smtypes.FilterNameStringType
		value string
		want  int
	}{
		{smtypes.FilterNameStringTypePrimaryRegion, "us-east", 1},
		{smtypes.FilterNameStringTypePrimaryRegion, "eu-", 0},
		{smtypes.FilterNameStringTypePrimaryRegion, "US-EAST", 0}, // case-sensitive
		{smtypes.FilterNameStringTypeOwningService, "rds", 0},
	}

	for _, tc := range cases {
		out, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{
			Filters: []smtypes.Filter{{Key: tc.key, Values: []string{tc.value}}},
		})
		if err != nil {
			t.Fatalf("ListSecrets %s=%s: %v", tc.key, tc.value, err)
		}

		if len(out.SecretList) != tc.want {
			t.Fatalf("%s=%s matched %v, want %d", tc.key, tc.value, secretNames(out.SecretList), tc.want)
		}
	}
}

func TestSDKReplicationLifecycle(t *testing.T) {
	client, _ := newClockedSecretsClient(t)
	ctx := context.Background()

	created := mustCreate(t, client, &awssm.CreateSecretInput{
		Name: aws.String("rep"), SecretString: aws.String("x"),
		AddReplicaRegions: []smtypes.ReplicaRegionType{{Region: aws.String("eu-west-3")}},
	})

	if len(created.ReplicationStatus) != 1 || created.ReplicationStatus[0].Status != smtypes.StatusTypeInSync ||
		aws.ToString(created.ReplicationStatus[0].KmsKeyId) != "alias/aws/secretsmanager" {
		t.Fatalf("CreateSecret ReplicationStatus = %+v", created.ReplicationStatus)
	}

	rep, err := client.ReplicateSecretToRegions(ctx, &awssm.ReplicateSecretToRegionsInput{
		SecretId:          created.ARN,
		AddReplicaRegions: []smtypes.ReplicaRegionType{{Region: aws.String("ap-south-1"), KmsKeyId: aws.String("alias/k")}},
	})
	if err != nil {
		t.Fatalf("ReplicateSecretToRegions: %v", err)
	}

	if aws.ToString(rep.ARN) != aws.ToString(created.ARN) || len(rep.ReplicationStatus) != 2 {
		t.Fatalf("ReplicateSecretToRegions = ARN %q, %+v", aws.ToString(rep.ARN), rep.ReplicationStatus)
	}

	desc, err := client.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("rep")})
	if err != nil {
		t.Fatalf("DescribeSecret: %v", err)
	}

	if len(desc.ReplicationStatus) != 2 || aws.ToString(desc.ReplicationStatus[1].KmsKeyId) != "alias/k" {
		t.Fatalf("DescribeSecret ReplicationStatus = %+v", desc.ReplicationStatus)
	}

	// Duplicate replica Region → InvalidParameterException.
	var ipe *smtypes.InvalidParameterException
	if _, err := client.ReplicateSecretToRegions(ctx, &awssm.ReplicateSecretToRegionsInput{
		SecretId: aws.String("rep"), AddReplicaRegions: []smtypes.ReplicaRegionType{{Region: aws.String("eu-west-3")}},
	}); !errors.As(err, &ipe) {
		t.Fatalf("duplicate replica err = %v, want InvalidParameterException", err)
	}

	// A replicated primary can't be deleted → InvalidRequestException.
	var ire *smtypes.InvalidRequestException
	if _, err := client.DeleteSecret(ctx, &awssm.DeleteSecretInput{SecretId: aws.String("rep")}); !errors.As(err, &ire) {
		t.Fatalf("DeleteSecret replicated primary err = %v, want InvalidRequestException", err)
	}

	// The secret here is the primary, not a replica → InvalidRequestException.
	if _, err := client.StopReplicationToReplica(ctx, &awssm.StopReplicationToReplicaInput{
		SecretId: aws.String("rep"),
	}); !errors.As(err, &ire) {
		t.Fatalf("StopReplicationToReplica on primary err = %v, want InvalidRequestException", err)
	}

	var rnf *smtypes.ResourceNotFoundException
	if _, err := client.StopReplicationToReplica(ctx, &awssm.StopReplicationToReplicaInput{
		SecretId: aws.String("missing"),
	}); !errors.As(err, &rnf) {
		t.Fatalf("StopReplicationToReplica missing err = %v, want ResourceNotFoundException", err)
	}

	removed, err := client.RemoveRegionsFromReplication(ctx, &awssm.RemoveRegionsFromReplicationInput{
		SecretId: aws.String("rep"), RemoveReplicaRegions: []string{"eu-west-3", "ap-south-1"},
	})
	if err != nil {
		t.Fatalf("RemoveRegionsFromReplication: %v", err)
	}

	if len(removed.ReplicationStatus) != 0 {
		t.Fatalf("after removal ReplicationStatus = %+v, want empty", removed.ReplicationStatus)
	}

	if _, err := client.DeleteSecret(ctx, &awssm.DeleteSecretInput{SecretId: aws.String("rep")}); err != nil {
		t.Fatalf("DeleteSecret after removing replicas: %v", err)
	}
}

func TestSDKScheduledForDeletionMessage(t *testing.T) {
	client, _ := newClockedSecretsClient(t)
	ctx := context.Background()

	mustCreate(t, client, &awssm.CreateSecretInput{Name: aws.String("s"), SecretString: aws.String("x")})

	if _, err := client.DeleteSecret(ctx, &awssm.DeleteSecretInput{SecretId: aws.String("s")}); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	var ire *smtypes.InvalidRequestException

	_, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("s")})
	if !errors.As(err, &ire) ||
		ire.ErrorMessage() != "You can't perform this operation on the secret because it was marked for deletion." {
		t.Fatalf("GetSecretValue on deleted secret err = %v", err)
	}

	_, err = client.CreateSecret(ctx, &awssm.CreateSecretInput{Name: aws.String("s"), SecretString: aws.String("x")})
	if !errors.As(err, &ire) || ire.ErrorMessage() !=
		"You can't create this secret because a secret with this name is already scheduled for deletion." {
		t.Fatalf("CreateSecret over deleted name err = %v", err)
	}
}
