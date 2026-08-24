package secretsmanager_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

func secretNames(secrets []smtypes.SecretListEntry) []string {
	out := make([]string, 0, len(secrets))
	for i := range secrets {
		out = append(out, aws.ToString(secrets[i].Name))
	}

	return out
}

// TestSDKListSecretsNameFilterPrefix verifies the name filter is a case-sensitive
// PREFIX match, not a substring match. "prod/db/password" must NOT be returned
// for name="db" even though it contains "db".
func TestSDKListSecretsNameFilterPrefix(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	for _, name := range []string{"prod/db/password", "db-config"} {
		if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
			Name: aws.String(name), SecretString: aws.String("x"),
		}); err != nil {
			t.Fatalf("CreateSecret %s: %v", name, err)
		}
	}

	out, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{
		Filters: []smtypes.Filter{{
			Key:    smtypes.FilterNameStringTypeName,
			Values: []string{"db"},
		}},
	})
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}

	got := secretNames(out.SecretList)
	if len(got) != 1 || got[0] != "db-config" {
		t.Fatalf("name filter 'db' returned %v, want only [db-config] (prefix, not substring)", got)
	}
}

// TestSDKListSecretsDescriptionFilterCaseInsensitivePrefix verifies the
// description filter is a case-INSENSITIVE prefix match.
func TestSDKListSecretsDescriptionFilterCaseInsensitivePrefix(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("api-key"), Description: aws.String("Production API token"),
		SecretString: aws.String("x"),
	}); err != nil {
		t.Fatalf("CreateSecret api-key: %v", err)
	}

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("db-key"), Description: aws.String("Staging database"),
		SecretString: aws.String("x"),
	}); err != nil {
		t.Fatalf("CreateSecret db-key: %v", err)
	}

	// Lowercase "prod" must prefix-match "Production ..." case-insensitively.
	out, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{
		Filters: []smtypes.Filter{{
			Key:    smtypes.FilterNameStringTypeDescription,
			Values: []string{"prod"},
		}},
	})
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}

	got := secretNames(out.SecretList)
	if len(got) != 1 || got[0] != "api-key" {
		t.Fatalf("description filter 'prod' returned %v, want only [api-key]", got)
	}

	// "database" is not a prefix of either description, so nothing matches.
	none, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{
		Filters: []smtypes.Filter{{
			Key:    smtypes.FilterNameStringTypeDescription,
			Values: []string{"database"},
		}},
	})
	if err != nil {
		t.Fatalf("ListSecrets database: %v", err)
	}

	if n := len(none.SecretList); n != 0 {
		t.Fatalf("description filter 'database' returned %d secrets, want 0 (not a prefix)",
			n)
	}
}

// TestSDKListSecretsTagKeyFilterPrefix verifies the tag-key filter is a prefix
// match against tag keys.
func TestSDKListSecretsTagKeyFilterPrefix(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("tagged"), SecretString: aws.String("x"),
		Tags: []smtypes.Tag{{Key: aws.String("environment"), Value: aws.String("prod")}},
	}); err != nil {
		t.Fatalf("CreateSecret tagged: %v", err)
	}

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("untagged"), SecretString: aws.String("x"),
	}); err != nil {
		t.Fatalf("CreateSecret untagged: %v", err)
	}

	// "env" is a prefix of the key "environment".
	out, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{
		Filters: []smtypes.Filter{{
			Key:    smtypes.FilterNameStringTypeTagKey,
			Values: []string{"env"},
		}},
	})
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}

	got := secretNames(out.SecretList)
	if len(got) != 1 || got[0] != "tagged" {
		t.Fatalf("tag-key filter 'env' returned %v, want only [tagged]", got)
	}

	// "vironment" is a substring but NOT a prefix, so it must not match.
	none, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{
		Filters: []smtypes.Filter{{
			Key:    smtypes.FilterNameStringTypeTagKey,
			Values: []string{"vironment"},
		}},
	})
	if err != nil {
		t.Fatalf("ListSecrets vironment: %v", err)
	}

	if n := len(none.SecretList); n != 0 {
		t.Fatalf("tag-key filter 'vironment' returned %d secrets, want 0 (not a prefix)", n)
	}
}
