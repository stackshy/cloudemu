package secretsmanager_test

import (
	"context"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// putVersions creates a secret and appends (n-1) additional versions so the
// secret has n total versions, returning them in creation order.
func putVersions(t *testing.T, client *awssm.Client, name string, values []string) {
	t.Helper()

	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name:         aws.String(name),
		SecretString: aws.String(values[0]),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	for _, v := range values[1:] {
		if _, err := client.PutSecretValue(ctx, &awssm.PutSecretValueInput{
			SecretId:     aws.String(name),
			SecretString: aws.String(v),
		}); err != nil {
			t.Fatalf("PutSecretValue(%s): %v", v, err)
		}
	}
}

// TestSDKListSecretVersionIdsStaging verifies that with 3+ versions exactly one
// version carries AWSCURRENT and exactly one carries AWSPREVIOUS, and that
// deprecated (label-less) versions are omitted by default. This is the round-3
// audit fix: previously every superseded version was labeled AWSPREVIOUS.
func TestSDKListSecretVersionIdsStaging(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	putVersions(t, client, "api-key", []string{"v1", "v2", "v3", "v4"})

	out, err := client.ListSecretVersionIds(ctx, &awssm.ListSecretVersionIdsInput{
		SecretId: aws.String("api-key"),
	})
	if err != nil {
		t.Fatalf("ListSecretVersionIds: %v", err)
	}

	// Default: only the two labeled versions (AWSCURRENT + AWSPREVIOUS).
	if len(out.Versions) != 2 {
		t.Fatalf("default version count = %d, want 2 (only labeled versions)", len(out.Versions))
	}

	var current, previous int

	for _, v := range out.Versions {
		if len(v.VersionStages) != 1 {
			t.Fatalf("version %s has stages %v, want exactly one", aws.ToString(v.VersionId), v.VersionStages)
		}

		switch v.VersionStages[0] {
		case "AWSCURRENT":
			current++
		case "AWSPREVIOUS":
			previous++
		default:
			t.Fatalf("unexpected stage %q", v.VersionStages[0])
		}
	}

	if current != 1 || previous != 1 {
		t.Fatalf("got %d AWSCURRENT and %d AWSPREVIOUS, want exactly 1 each", current, previous)
	}
}

// TestSDKListSecretVersionIdsIncludeDeprecated verifies that IncludeDeprecated
// returns the label-less versions too, and only they lack staging labels.
func TestSDKListSecretVersionIdsIncludeDeprecated(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	putVersions(t, client, "api-key", []string{"v1", "v2", "v3", "v4"})

	out, err := client.ListSecretVersionIds(ctx, &awssm.ListSecretVersionIdsInput{
		SecretId:          aws.String("api-key"),
		IncludeDeprecated: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("ListSecretVersionIds: %v", err)
	}

	if len(out.Versions) != 4 {
		t.Fatalf("IncludeDeprecated version count = %d, want 4", len(out.Versions))
	}

	stageCounts := map[string]int{}
	deprecated := 0

	for _, v := range out.Versions {
		if len(v.VersionStages) == 0 {
			deprecated++
			continue
		}

		for _, s := range v.VersionStages {
			stageCounts[s]++
		}
	}

	if stageCounts["AWSCURRENT"] != 1 || stageCounts["AWSPREVIOUS"] != 1 {
		t.Fatalf("stage counts = %v, want exactly 1 AWSCURRENT and 1 AWSPREVIOUS", stageCounts)
	}

	if deprecated != 2 {
		t.Fatalf("deprecated (label-less) versions = %d, want 2", deprecated)
	}
}

// TestSDKGetSecretValueDeprecatedStages verifies that reading a deprecated
// version by its ID returns no staging labels (it is neither AWSCURRENT nor
// AWSPREVIOUS).
func TestSDKGetSecretValueDeprecatedStages(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	putVersions(t, client, "api-key", []string{"v1", "v2", "v3", "v4"})

	all, err := client.ListSecretVersionIds(ctx, &awssm.ListSecretVersionIdsInput{
		SecretId:          aws.String("api-key"),
		IncludeDeprecated: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("ListSecretVersionIds: %v", err)
	}

	// Find a deprecated version ID (no staging labels).
	var deprecatedID string

	ids := make([]string, 0, len(all.Versions))
	for _, v := range all.Versions {
		if len(v.VersionStages) == 0 {
			ids = append(ids, aws.ToString(v.VersionId))
		}
	}

	sort.Strings(ids)

	if len(ids) == 0 {
		t.Fatal("no deprecated version found")
	}

	deprecatedID = ids[0]

	got, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{
		SecretId:  aws.String("api-key"),
		VersionId: aws.String(deprecatedID),
	})
	if err != nil {
		t.Fatalf("GetSecretValue(deprecated): %v", err)
	}

	if len(got.VersionStages) != 0 {
		t.Fatalf("deprecated version stages = %v, want empty", got.VersionStages)
	}
}
