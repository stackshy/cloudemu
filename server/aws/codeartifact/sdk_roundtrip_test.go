package codeartifact_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	ca "github.com/aws/aws-sdk-go-v2/service/codeartifact"
	catypes "github.com/aws/aws-sdk-go-v2/service/codeartifact/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newClient(t *testing.T) *ca.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{CodeArtifact: cloud.CodeArtifact})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return ca.NewFromConfig(cfg, func(o *ca.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func createDomain(t *testing.T, c *ca.Client, name string) *catypes.DomainDescription {
	t.Helper()

	out, err := c.CreateDomain(context.Background(), &ca.CreateDomainInput{
		Domain: aws.String(name),
		Tags:   []catypes.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
	})
	if err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	return out.Domain
}

func TestSDKDomainLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	created := createDomain(t, c, "sdk-domain")

	if aws.ToString(created.Arn) == "" || aws.ToString(created.Owner) == "" {
		t.Fatalf("create returned empty arn/owner: %+v", created)
	}

	if created.Status != catypes.DomainStatusActive {
		t.Fatalf("status = %q, want Active", created.Status)
	}

	d1, err := c.DescribeDomain(ctx, &ca.DescribeDomainInput{Domain: aws.String("sdk-domain")})
	if err != nil {
		t.Fatalf("DescribeDomain: %v", err)
	}

	// Computed fields are stable across create and describe.
	if aws.ToString(d1.Domain.Arn) != aws.ToString(created.Arn) {
		t.Fatal("domain arn drifted between create and describe")
	}

	if !d1.Domain.CreatedTime.Equal(*created.CreatedTime) {
		t.Fatal("createdTime drifted between create and describe")
	}

	if d1.Domain.RepositoryCount != 0 {
		t.Fatalf("repositoryCount = %d, want 0", d1.Domain.RepositoryCount)
	}

	// Second read: still byte-stable.
	d2, _ := c.DescribeDomain(ctx, &ca.DescribeDomainInput{Domain: aws.String("sdk-domain")})
	if aws.ToString(d2.Domain.Arn) != aws.ToString(created.Arn) ||
		aws.ToString(d2.Domain.EncryptionKey) != aws.ToString(created.EncryptionKey) {
		t.Fatal("computed fields drifted across reads")
	}

	list, err := c.ListDomains(ctx, &ca.ListDomainsInput{})
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}

	if len(list.Domains) != 1 || aws.ToString(list.Domains[0].Name) != "sdk-domain" {
		t.Fatalf("Domains = %+v", list.Domains)
	}

	if _, err := c.DeleteDomain(ctx, &ca.DeleteDomainInput{Domain: aws.String("sdk-domain")}); err != nil {
		t.Fatalf("DeleteDomain: %v", err)
	}

	_, err = c.DescribeDomain(ctx, &ca.DescribeDomainInput{Domain: aws.String("sdk-domain")})

	var nf *catypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("DescribeDomain after delete: got %v, want ResourceNotFoundException", err)
	}
}

func TestSDKDuplicateDomainConflict(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	createDomain(t, c, "dup-domain")

	_, err := c.CreateDomain(ctx, &ca.CreateDomainInput{Domain: aws.String("dup-domain")})

	var cf *catypes.ConflictException
	if !errors.As(err, &cf) {
		t.Fatalf("duplicate domain: got %v, want ConflictException", err)
	}
}

func TestSDKRepositoryLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	createDomain(t, c, "repo-domain")

	create, err := c.CreateRepository(ctx, &ca.CreateRepositoryInput{
		Domain:      aws.String("repo-domain"),
		Repository:  aws.String("my-repo"),
		Description: aws.String("initial"),
		Tags:        []catypes.Tag{{Key: aws.String("team"), Value: aws.String("data")}},
	})
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	repo := create.Repository
	if aws.ToString(repo.Arn) == "" || aws.ToString(repo.AdministratorAccount) == "" {
		t.Fatalf("create returned empty arn/administratorAccount: %+v", repo)
	}

	// Domain repositoryCount now reflects the child repository.
	dd, _ := c.DescribeDomain(ctx, &ca.DescribeDomainInput{Domain: aws.String("repo-domain")})
	if dd.Domain.RepositoryCount != 1 {
		t.Fatalf("repositoryCount = %d, want 1", dd.Domain.RepositoryCount)
	}

	// Update description; identity fields must stay stable.
	upd, err := c.UpdateRepository(ctx, &ca.UpdateRepositoryInput{
		Domain:      aws.String("repo-domain"),
		Repository:  aws.String("my-repo"),
		Description: aws.String("updated"),
	})
	if err != nil {
		t.Fatalf("UpdateRepository: %v", err)
	}

	if aws.ToString(upd.Repository.Description) != "updated" {
		t.Fatalf("description = %q, want updated", aws.ToString(upd.Repository.Description))
	}

	if aws.ToString(upd.Repository.Arn) != aws.ToString(repo.Arn) ||
		!upd.Repository.CreatedTime.Equal(*repo.CreatedTime) {
		t.Fatal("computed fields drifted after update")
	}

	list, err := c.ListRepositories(ctx, &ca.ListRepositoriesInput{})
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}

	if len(list.Repositories) != 1 || aws.ToString(list.Repositories[0].Name) != "my-repo" {
		t.Fatalf("Repositories = %+v", list.Repositories)
	}

	inDomain, err := c.ListRepositoriesInDomain(ctx, &ca.ListRepositoriesInDomainInput{
		Domain: aws.String("repo-domain"),
	})
	if err != nil {
		t.Fatalf("ListRepositoriesInDomain: %v", err)
	}

	if len(inDomain.Repositories) != 1 {
		t.Fatalf("in-domain Repositories = %+v", inDomain.Repositories)
	}

	if _, err := c.DeleteRepository(ctx, &ca.DeleteRepositoryInput{
		Domain: aws.String("repo-domain"), Repository: aws.String("my-repo"),
	}); err != nil {
		t.Fatalf("DeleteRepository: %v", err)
	}

	_, err = c.DescribeRepository(ctx, &ca.DescribeRepositoryInput{
		Domain: aws.String("repo-domain"), Repository: aws.String("my-repo"),
	})

	var nf *catypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("DescribeRepository after delete: got %v, want ResourceNotFoundException", err)
	}
}

func TestSDKCreateRepositoryMissingDomain(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	_, err := c.CreateRepository(ctx, &ca.CreateRepositoryInput{
		Domain: aws.String("no-such-domain"), Repository: aws.String("r"),
	})

	var nf *catypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("CreateRepository without domain: got %v, want ResourceNotFoundException", err)
	}
}

func TestSDKDeleteNonEmptyDomainConflict(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	createDomain(t, c, "busy-domain")

	if _, err := c.CreateRepository(ctx, &ca.CreateRepositoryInput{
		Domain: aws.String("busy-domain"), Repository: aws.String("r1"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	_, err := c.DeleteDomain(ctx, &ca.DeleteDomainInput{Domain: aws.String("busy-domain")})

	var cf *catypes.ConflictException
	if !errors.As(err, &cf) {
		t.Fatalf("delete non-empty domain: got %v, want ConflictException", err)
	}
}

func TestSDKExternalConnection(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	createDomain(t, c, "ec-domain")

	if _, err := c.CreateRepository(ctx, &ca.CreateRepositoryInput{
		Domain: aws.String("ec-domain"), Repository: aws.String("npm-repo"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	assoc, err := c.AssociateExternalConnection(ctx, &ca.AssociateExternalConnectionInput{
		Domain:             aws.String("ec-domain"),
		Repository:         aws.String("npm-repo"),
		ExternalConnection: aws.String("public:npmjs"),
	})
	if err != nil {
		t.Fatalf("AssociateExternalConnection: %v", err)
	}

	if len(assoc.Repository.ExternalConnections) != 1 ||
		aws.ToString(assoc.Repository.ExternalConnections[0].ExternalConnectionName) != "public:npmjs" {
		t.Fatalf("externalConnections = %+v", assoc.Repository.ExternalConnections)
	}

	if assoc.Repository.ExternalConnections[0].PackageFormat != catypes.PackageFormatNpm {
		t.Fatalf("packageFormat = %q, want npm", assoc.Repository.ExternalConnections[0].PackageFormat)
	}

	dis, err := c.DisassociateExternalConnection(ctx, &ca.DisassociateExternalConnectionInput{
		Domain:             aws.String("ec-domain"),
		Repository:         aws.String("npm-repo"),
		ExternalConnection: aws.String("public:npmjs"),
	})
	if err != nil {
		t.Fatalf("DisassociateExternalConnection: %v", err)
	}

	if len(dis.Repository.ExternalConnections) != 0 {
		t.Fatalf("externalConnections after disassociate = %+v", dis.Repository.ExternalConnections)
	}
}

func TestSDKTagResource(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	arn := aws.ToString(createDomain(t, c, "tag-domain").Arn)

	if _, err := c.TagResource(ctx, &ca.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        []catypes.Tag{{Key: aws.String("team"), Value: aws.String("data")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	lt, err := c.ListTagsForResource(ctx, &ca.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	got := tagMap(lt.Tags)
	if got["team"] != "data" || got["env"] != "test" {
		t.Fatalf("tags = %v, want team=data and env=test", got)
	}

	if _, err := c.UntagResource(ctx, &ca.UntagResourceInput{
		ResourceArn: aws.String(arn), TagKeys: []string{"team"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	lt2, _ := c.ListTagsForResource(ctx, &ca.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if _, ok := tagMap(lt2.Tags)["team"]; ok {
		t.Fatal("team tag not removed")
	}
}

func tagMap(tags []catypes.Tag) map[string]string {
	out := map[string]string{}
	for _, t := range tags {
		out[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}

	return out
}
