package secretsmanager_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/smithy-go"
)

// versionPageSize is the MaxResults used in the pagination tests: small enough
// that the fixtures span several pages.
const versionPageSize = 2

// TestSDKListSecretVersionIdsPaginates proves ListSecretVersionIds honors
// MaxResults/NextToken: the paginator walks every version exactly once and
// terminates (previously the handler returned every version with no token).
func TestSDKListSecretVersionIdsPaginates(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	putVersions(t, client, "api-key", []string{"v1", "v2", "v3", "v4", "v5"})

	paginator := awssm.NewListSecretVersionIdsPaginator(client, &awssm.ListSecretVersionIdsInput{
		SecretId:          aws.String("api-key"),
		IncludeDeprecated: aws.Bool(true),
		MaxResults:        aws.Int32(versionPageSize),
	})

	got := map[string]bool{}
	pages := 0

	for paginator.HasMorePages() {
		out, err := paginator.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		pages++

		if len(out.Versions) > versionPageSize {
			t.Fatalf("page returned %d versions, want <= %d", len(out.Versions), versionPageSize)
		}

		for _, v := range out.Versions {
			id := aws.ToString(v.VersionId)
			if got[id] {
				t.Fatalf("version %s returned twice", id)
			}

			got[id] = true
		}
	}

	// 5 versions returned when IncludeDeprecated is set.
	if len(got) != 5 {
		t.Fatalf("walked %d versions, want 5", len(got))
	}

	// 5 versions, page size 2 -> pages of 2, 2, 1.
	if wantPages := 3; pages != wantPages {
		t.Fatalf("walked %d pages, want %d", pages, wantPages)
	}
}

// TestSDKListSecretVersionIdsSinglePageNoToken proves a version set that fits in
// one page returns no NextToken.
func TestSDKListSecretVersionIdsSinglePageNoToken(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	putVersions(t, client, "api-key", []string{"v1", "v2"})

	out, err := client.ListSecretVersionIds(ctx, &awssm.ListSecretVersionIdsInput{
		SecretId:   aws.String("api-key"),
		MaxResults: aws.Int32(versionPageSize),
	})
	if err != nil {
		t.Fatalf("ListSecretVersionIds: %v", err)
	}

	if out.NextToken != nil {
		t.Fatalf("NextToken = %q, want nil for a single page", aws.ToString(out.NextToken))
	}
}

// TestSDKListSecretVersionIdsInvalidToken proves a malformed NextToken surfaces
// as an API error rather than being silently ignored.
func TestSDKListSecretVersionIdsInvalidToken(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	putVersions(t, client, "api-key", []string{"v1", "v2"})

	_, err := client.ListSecretVersionIds(ctx, &awssm.ListSecretVersionIdsInput{
		SecretId:  aws.String("api-key"),
		NextToken: aws.String("!!not-base64!!"),
	})
	if err == nil {
		t.Fatal("ListSecretVersionIds with a bad NextToken succeeded, want an error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not an API error: %v", err)
	}
}
