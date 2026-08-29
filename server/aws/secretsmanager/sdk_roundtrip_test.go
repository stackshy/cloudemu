package secretsmanager_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newSecretsClient(t *testing.T) *awssm.Client {
	t.Helper()

	client, _ := newSecretsClientWithCloud(t)

	return client
}

// newSecretsClientWithCloud returns a Secrets Manager SDK client bound to a live
// wire server plus the backing provider, so a test can seed cross-service state
// (e.g. create a KMS key that a secret then references) via the Go API.
func newSecretsClientWithCloud(t *testing.T) (*awssm.Client, *awsprovider.Provider) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{SecretsManager: cloud.SecretsManager})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awssm.NewFromConfig(cfg, func(o *awssm.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	}), cloud
}

func TestSDKSecretLifecycle(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	created, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name:         aws.String("db-password"),
		Description:  aws.String("primary database password"),
		SecretString: aws.String("hunter2"),
		Tags:         []smtypes.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	if aws.ToString(created.Name) != "db-password" {
		t.Fatalf("got secret name %q, want db-password", aws.ToString(created.Name))
	}

	if aws.ToString(created.ARN) == "" || aws.ToString(created.VersionId) == "" {
		t.Fatalf("CreateSecret returned empty ARN or VersionId: %+v", created)
	}

	desc, err := client.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("db-password")})
	if err != nil {
		t.Fatalf("DescribeSecret: %v", err)
	}

	if aws.ToString(desc.Description) != "primary database password" {
		t.Fatalf("got description %q", aws.ToString(desc.Description))
	}

	if len(desc.Tags) != 1 || aws.ToString(desc.Tags[0].Key) != "env" {
		t.Fatalf("DescribeSecret tags = %+v, want env tag", desc.Tags)
	}

	list, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{})
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}

	if len(list.SecretList) != 1 || aws.ToString(list.SecretList[0].Name) != "db-password" {
		t.Fatalf("ListSecrets = %+v, want one entry db-password", list.SecretList)
	}

	deleted, err := client.DeleteSecret(ctx, &awssm.DeleteSecretInput{SecretId: aws.String("db-password")})
	if err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	if aws.ToString(deleted.Name) != "db-password" {
		t.Fatalf("DeleteSecret echoed name %q", aws.ToString(deleted.Name))
	}

	// DescribeSecret keeps working after DeleteSecret and reports DeletedDate;
	// only a never-created secret is ResourceNotFoundException.
	descDeleted, err := client.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("db-password")})
	if err != nil {
		t.Fatalf("DescribeSecret after delete: %v", err)
	}

	if descDeleted.DeletedDate == nil {
		t.Fatal("DescribeSecret after delete: DeletedDate not set")
	}
}

// TestSDKUpdateSecretAndTagging is a regression guard for issue #319:
// UpdateSecret, TagResource, and UntagResource were unimplemented.
func TestSDKUpdateSecretAndTagging(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("s"), Description: aws.String("d1"), SecretString: aws.String("v1"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	// UpdateSecret changes description and value.
	if _, err := client.UpdateSecret(ctx, &awssm.UpdateSecretInput{
		SecretId: aws.String("s"), Description: aws.String("d2"), SecretString: aws.String("v2"),
	}); err != nil {
		t.Fatalf("UpdateSecret: %v", err)
	}

	val, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("s")})
	if err != nil {
		t.Fatalf("GetSecretValue: %v", err)
	}

	if aws.ToString(val.SecretString) != "v2" {
		t.Fatalf("value = %q, want v2", aws.ToString(val.SecretString))
	}

	// TagResource then UntagResource.
	if _, err := client.TagResource(ctx, &awssm.TagResourceInput{
		SecretId: aws.String("s"), Tags: []smtypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	desc, err := client.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("s")})
	if err != nil {
		t.Fatalf("DescribeSecret: %v", err)
	}

	if aws.ToString(desc.Description) != "d2" || len(desc.Tags) != 1 {
		t.Fatalf("after update+tag: description=%q tags=%+v", aws.ToString(desc.Description), desc.Tags)
	}

	if _, err := client.UntagResource(ctx, &awssm.UntagResourceInput{
		SecretId: aws.String("s"), TagKeys: []string{"env"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	desc, err = client.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("s")})
	if err != nil {
		t.Fatalf("DescribeSecret after untag: %v", err)
	}

	if len(desc.Tags) != 0 {
		t.Fatalf("tags after untag = %+v, want none", desc.Tags)
	}
}

func TestSDKSecretValueVersioning(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	created, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name:         aws.String("api-key"),
		SecretString: aws.String("v1-value"),
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	got, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("api-key")})
	if err != nil {
		t.Fatalf("GetSecretValue: %v", err)
	}

	if aws.ToString(got.SecretString) != "v1-value" {
		t.Fatalf("got value %q, want v1-value", aws.ToString(got.SecretString))
	}

	if len(got.VersionStages) != 1 || got.VersionStages[0] != "AWSCURRENT" {
		t.Fatalf("got stages %v, want [AWSCURRENT]", got.VersionStages)
	}

	put, err := client.PutSecretValue(ctx, &awssm.PutSecretValueInput{
		SecretId:     aws.String("api-key"),
		SecretString: aws.String("v2-value"),
	})
	if err != nil {
		t.Fatalf("PutSecretValue: %v", err)
	}

	if aws.ToString(put.VersionId) == aws.ToString(created.VersionId) {
		t.Fatal("PutSecretValue reused the initial VersionId")
	}

	current, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("api-key")})
	if err != nil {
		t.Fatalf("GetSecretValue(current): %v", err)
	}

	if aws.ToString(current.SecretString) != "v2-value" {
		t.Fatalf("current value = %q, want v2-value", aws.ToString(current.SecretString))
	}

	old, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{
		SecretId:  aws.String("api-key"),
		VersionId: created.VersionId,
	})
	if err != nil {
		t.Fatalf("GetSecretValue(v1): %v", err)
	}

	if aws.ToString(old.SecretString) != "v1-value" {
		t.Fatalf("v1 value = %q, want v1-value", aws.ToString(old.SecretString))
	}

	versions, err := client.ListSecretVersionIds(ctx, &awssm.ListSecretVersionIdsInput{SecretId: aws.String("api-key")})
	if err != nil {
		t.Fatalf("ListSecretVersionIds: %v", err)
	}

	if len(versions.Versions) != 2 {
		t.Fatalf("got %d versions, want 2", len(versions.Versions))
	}
}

func TestSDKSecretByARN(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	created, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name:         aws.String("arn-lookup"),
		SecretString: aws.String("value"),
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	got, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: created.ARN})
	if err != nil {
		t.Fatalf("GetSecretValue(by ARN): %v", err)
	}

	if aws.ToString(got.Name) != "arn-lookup" {
		t.Fatalf("got name %q, want arn-lookup", aws.ToString(got.Name))
	}
}

func TestSDKSecretErrors(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name:         aws.String("dup"),
		SecretString: aws.String("x"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	_, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name:         aws.String("dup"),
		SecretString: aws.String("y"),
	})

	var exists *smtypes.ResourceExistsException
	if !errors.As(err, &exists) {
		t.Fatalf("duplicate CreateSecret: got %v, want ResourceExistsException", err)
	}

	_, err = client.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("missing")})

	var notFound *smtypes.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("GetSecretValue(missing): got %v, want ResourceNotFoundException", err)
	}

	_, err = client.DeleteSecret(ctx, &awssm.DeleteSecretInput{SecretId: aws.String("missing")})
	if !errors.As(err, &notFound) {
		t.Fatalf("DeleteSecret(missing): got %v, want ResourceNotFoundException", err)
	}
}

// TestSDKGetResourcePolicy guards the read path the aws_secretsmanager_secret
// resource uses: GetResourcePolicy must return the secret's ARN/Name and no
// ResourcePolicy (none is modeled), which the SDK reads as "no policy".
func TestSDKGetResourcePolicy(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	created, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name:         aws.String("with-policy"),
		SecretString: aws.String("s3cr3t"),
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	out, err := client.GetResourcePolicy(ctx, &awssm.GetResourcePolicyInput{
		SecretId: created.ARN,
	})
	if err != nil {
		t.Fatalf("GetResourcePolicy: %v", err)
	}

	if aws.ToString(out.ARN) != aws.ToString(created.ARN) {
		t.Errorf("ARN = %q, want %q", aws.ToString(out.ARN), aws.ToString(created.ARN))
	}

	if aws.ToString(out.Name) != "with-policy" {
		t.Errorf("Name = %q, want with-policy", aws.ToString(out.Name))
	}

	if out.ResourcePolicy != nil {
		t.Errorf("ResourcePolicy = %q, want nil (none modeled)", aws.ToString(out.ResourcePolicy))
	}
}
