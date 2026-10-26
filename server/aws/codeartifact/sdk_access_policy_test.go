package codeartifact_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	ca "github.com/aws/aws-sdk-go-v2/service/codeartifact"
	catypes "github.com/aws/aws-sdk-go-v2/service/codeartifact/types"
)

func TestSDKInvalidNames(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	for _, bad := range []string{"my domain", "UPPER", "a", "-dash"} {
		_, err := e.c.CreateDomain(ctx, &ca.CreateDomainInput{Domain: aws.String(bad)})

		apiErr := requireAPICode(t, err, "ValidationException", 400)
		if !strings.Contains(apiErr.ErrorMessage(), "domain name") {
			t.Fatalf("message = %q", apiErr.ErrorMessage())
		}

		var ve *catypes.ValidationException
		if !errors.As(err, &ve) || ve.Reason != catypes.ValidationExceptionReasonFieldValidationFailed {
			t.Fatalf("reason = %+v", ve)
		}
	}

	e.seed(t)

	_, err := e.c.CreateRepository(ctx, &ca.CreateRepositoryInput{Domain: aws.String("dom"), Repository: aws.String("a b")})
	requireAPICode(t, err, "ValidationException", 400)

	got, err := e.c.ListDomains(ctx, &ca.ListDomainsInput{})
	if err != nil || len(got.Domains) != 1 {
		t.Fatalf("domains = %+v err=%v", got, err)
	}
}

func TestSDKDomainOwnerMismatch(t *testing.T) {
	e := newEnv(t)
	e.seed(t, "repo")

	ctx := context.Background()
	foreign := aws.String("999999999999")

	_, err := e.c.DescribeDomain(ctx, &ca.DescribeDomainInput{Domain: aws.String("dom"), DomainOwner: foreign})
	requireAPICode(t, err, "ResourceNotFoundException", 404)

	_, err = e.c.DescribeRepository(ctx, &ca.DescribeRepositoryInput{Domain: aws.String("dom"), DomainOwner: foreign, Repository: aws.String("repo")})
	requireAPICode(t, err, "ResourceNotFoundException", 404)

	_, err = e.c.ListRepositoriesInDomain(ctx, &ca.ListRepositoriesInDomainInput{Domain: aws.String("dom"), DomainOwner: foreign})
	requireAPICode(t, err, "ResourceNotFoundException", 404)

	_, err = e.c.CreateRepository(ctx, &ca.CreateRepositoryInput{Domain: aws.String("dom"), DomainOwner: foreign, Repository: aws.String("other")})
	requireAPICode(t, err, "ResourceNotFoundException", 404)

	owner := e.cloud.CodeArtifact
	d, derr := owner.DescribeDomain(ctx, "dom", "")

	if derr != nil {
		t.Fatal(derr)
	}

	if _, err = e.c.DescribeDomain(ctx, &ca.DescribeDomainInput{Domain: aws.String("dom"), DomainOwner: aws.String(d.Owner)}); err != nil {
		t.Fatalf("matching owner rejected: %v", err)
	}

	out, err := e.c.ListRepositoriesInDomain(ctx, &ca.ListRepositoriesInDomainInput{
		Domain: aws.String("dom"), AdministratorAccount: aws.String("999999999999"),
	})
	if err != nil || len(out.Repositories) != 0 {
		t.Fatalf("administrator filter: %+v err=%v", out, err)
	}
}

func TestSDKExternalConnectionErrors(t *testing.T) {
	e := newEnv(t)
	e.seed(t, "repo")

	ctx := context.Background()
	in := func(name string) *ca.AssociateExternalConnectionInput {
		return &ca.AssociateExternalConnectionInput{
			Domain: aws.String("dom"), Repository: aws.String("repo"), ExternalConnection: aws.String(name),
		}
	}

	out, err := e.c.AssociateExternalConnection(ctx, in("public:npmjs"))
	if err != nil {
		t.Fatalf("associate: %v", err)
	}

	if len(out.Repository.ExternalConnections) != 1 || out.Repository.ExternalConnections[0].Status != catypes.ExternalConnectionStatusAvailable ||
		out.Repository.ExternalConnections[0].PackageFormat != catypes.PackageFormatNpm {
		t.Fatalf("connections = %+v", out.Repository.ExternalConnections)
	}

	_, err = e.c.AssociateExternalConnection(ctx, in("public:pypi"))
	requireAPICode(t, err, "ConflictException", 409)

	var ce *catypes.ConflictException
	if !errors.As(err, &ce) || aws.ToString(ce.ResourceId) != "repo" || ce.ResourceType != catypes.ResourceTypeRepository {
		t.Fatalf("conflict identity = %+v", ce)
	}

	_, err = e.c.AssociateExternalConnection(ctx, in("public:nope"))
	requireAPICode(t, err, "ResourceNotFoundException", 404)

	_, err = e.c.DisassociateExternalConnection(ctx, &ca.DisassociateExternalConnectionInput{
		Domain: aws.String("dom"), Repository: aws.String("repo"), ExternalConnection: aws.String("public:pypi"),
	})
	requireAPICode(t, err, "ResourceNotFoundException", 404)
}

func TestSDKQuotaExceeded(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		if _, err := e.c.CreateDomain(ctx, &ca.CreateDomainInput{Domain: aws.String("dom-" + string(rune('a'+i)) + "x")}); err != nil {
			t.Fatal(err)
		}
	}

	_, err := e.c.CreateDomain(ctx, &ca.CreateDomainInput{Domain: aws.String("dom-over")})
	requireAPICode(t, err, "ServiceQuotaExceededException", 402)

	var qe *catypes.ServiceQuotaExceededException
	if !errors.As(err, &qe) || qe.ResourceType != catypes.ResourceTypeDomain {
		t.Fatalf("quota error = %+v", qe)
	}
}

func TestSDKGetAuthorizationToken(t *testing.T) {
	e := newEnv(t)
	e.seed(t)

	ctx := context.Background()
	before := time.Now()

	out, err := e.c.GetAuthorizationToken(ctx, &ca.GetAuthorizationTokenInput{Domain: aws.String("dom"), DurationSeconds: aws.Int64(900)})
	if err != nil {
		t.Fatalf("GetAuthorizationToken: %v", err)
	}

	if aws.ToString(out.AuthorizationToken) == "" || out.Expiration == nil || out.Expiration.Before(before.Add(14*time.Minute)) {
		t.Fatalf("token = %+v", out)
	}

	_, err = e.c.GetAuthorizationToken(ctx, &ca.GetAuthorizationTokenInput{Domain: aws.String("ghost")})
	requireAPICode(t, err, "ResourceNotFoundException", 404)

	_, err = e.c.GetAuthorizationToken(ctx, &ca.GetAuthorizationTokenInput{Domain: aws.String("dom"), DurationSeconds: aws.Int64(10)})
	requireAPICode(t, err, "ValidationException", 400)
}

func TestSDKGetRepositoryEndpoint(t *testing.T) {
	e := newEnv(t)
	e.seed(t, "repo")

	ctx := context.Background()

	out, err := e.c.GetRepositoryEndpoint(ctx, &ca.GetRepositoryEndpointInput{
		Domain: aws.String("dom"), Repository: aws.String("repo"), Format: catypes.PackageFormatNpm,
	})
	if err != nil {
		t.Fatalf("GetRepositoryEndpoint: %v", err)
	}

	if got := aws.ToString(out.RepositoryEndpoint); !strings.HasPrefix(got, "https://dom-") || !strings.HasSuffix(got, ".amazonaws.com/npm/repo/") {
		t.Fatalf("endpoint = %q", got)
	}

	_, err = e.c.GetRepositoryEndpoint(ctx, &ca.GetRepositoryEndpointInput{
		Domain: aws.String("dom"), Repository: aws.String("ghost"), Format: catypes.PackageFormatNpm,
	})
	requireAPICode(t, err, "ResourceNotFoundException", 404)
}

func TestSDKPolicies(t *testing.T) {
	e := newEnv(t)
	e.seed(t, "repo")

	ctx := context.Background()
	doc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"codeartifact:ReadFromRepository","Resource":"*"}]}`

	_, err := e.c.GetDomainPermissionsPolicy(ctx, &ca.GetDomainPermissionsPolicyInput{Domain: aws.String("dom")})
	requireAPICode(t, err, "ResourceNotFoundException", 404)

	put, err := e.c.PutDomainPermissionsPolicy(ctx, &ca.PutDomainPermissionsPolicyInput{Domain: aws.String("dom"), PolicyDocument: aws.String(doc)})
	if err != nil {
		t.Fatalf("PutDomainPermissionsPolicy: %v", err)
	}

	if aws.ToString(put.Policy.Revision) == "" || aws.ToString(put.Policy.Document) != doc {
		t.Fatalf("put = %+v", put.Policy)
	}

	got, err := e.c.GetDomainPermissionsPolicy(ctx, &ca.GetDomainPermissionsPolicyInput{Domain: aws.String("dom")})
	if err != nil || aws.ToString(got.Policy.Revision) != aws.ToString(put.Policy.Revision) {
		t.Fatalf("get = %+v err=%v", got, err)
	}

	_, err = e.c.PutDomainPermissionsPolicy(ctx, &ca.PutDomainPermissionsPolicyInput{
		Domain: aws.String("dom"), PolicyDocument: aws.String(doc), PolicyRevision: aws.String("stale"),
	})
	requireAPICode(t, err, "ConflictException", 409)

	_, err = e.c.DeleteDomainPermissionsPolicy(ctx, &ca.DeleteDomainPermissionsPolicyInput{Domain: aws.String("dom"), PolicyRevision: aws.String("stale")})
	requireAPICode(t, err, "ConflictException", 409)

	_, err = e.c.PutDomainPermissionsPolicy(ctx, &ca.PutDomainPermissionsPolicyInput{Domain: aws.String("dom"), PolicyDocument: aws.String("nope")})
	requireAPICode(t, err, "ValidationException", 400)

	del, err := e.c.DeleteDomainPermissionsPolicy(ctx, &ca.DeleteDomainPermissionsPolicyInput{
		Domain: aws.String("dom"), PolicyRevision: put.Policy.Revision,
	})
	if err != nil || aws.ToString(del.Policy.Revision) != aws.ToString(put.Policy.Revision) {
		t.Fatalf("delete = %+v err=%v", del, err)
	}

	rput, err := e.c.PutRepositoryPermissionsPolicy(ctx, &ca.PutRepositoryPermissionsPolicyInput{
		Domain: aws.String("dom"), Repository: aws.String("repo"), PolicyDocument: aws.String(doc),
	})
	if err != nil {
		t.Fatalf("PutRepositoryPermissionsPolicy: %v", err)
	}

	rget, err := e.c.GetRepositoryPermissionsPolicy(ctx, &ca.GetRepositoryPermissionsPolicyInput{Domain: aws.String("dom"), Repository: aws.String("repo")})
	if err != nil || aws.ToString(rget.Policy.ResourceArn) != aws.ToString(rput.Policy.ResourceArn) {
		t.Fatalf("repo get = %+v err=%v", rget, err)
	}

	if _, err = e.c.DeleteRepositoryPermissionsPolicy(ctx, &ca.DeleteRepositoryPermissionsPolicyInput{
		Domain: aws.String("dom"), Repository: aws.String("repo"), PolicyRevision: rput.Policy.Revision,
	}); err != nil {
		t.Fatalf("DeleteRepositoryPermissionsPolicy: %v", err)
	}

	_, err = e.c.GetRepositoryPermissionsPolicy(ctx, &ca.GetRepositoryPermissionsPolicyInput{Domain: aws.String("dom"), Repository: aws.String("repo")})
	requireAPICode(t, err, "ResourceNotFoundException", 404)
}
