package secretsmanager_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

func TestSDKListSecretsRejectsInvalidInput(t *testing.T) {
	client, _ := newClockedSecretsClient(t)
	ctx := context.Background()

	mustCreate(t, client, &awssm.CreateSecretInput{Name: aws.String("s"), SecretString: aws.String("x")})

	invalid := []struct {
		name string
		in   awssm.ListSecretsInput
	}{
		{"unknown filter key", awssm.ListSecretsInput{
			Filters: []smtypes.Filter{{Key: "primary_region", Values: []string{"us"}}},
		}},
		{"unknown SortBy", awssm.ListSecretsInput{SortBy: "size"}},
		{"unknown SortOrder", awssm.ListSecretsInput{SortOrder: "up"}},
		{"MaxResults 0", awssm.ListSecretsInput{MaxResults: aws.Int32(0)}},
		{"MaxResults 101", awssm.ListSecretsInput{MaxResults: aws.Int32(101)}},
	}

	for _, tc := range invalid {
		in := tc.in

		_, err := client.ListSecrets(ctx, &in)

		var apiErr interface{ ErrorCode() string }
		if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationException" {
			t.Fatalf("%s: err = %v, want ValidationException", tc.name, err)
		}
	}

	var bad *smtypes.InvalidNextTokenException
	if _, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{NextToken: aws.String("not-a-token!")}); !errors.As(err, &bad) {
		t.Fatalf("bad NextToken err = %v, want InvalidNextTokenException", err)
	}

	_, err := client.BatchGetSecretValue(ctx, &awssm.BatchGetSecretValueInput{
		Filters: []smtypes.Filter{{Key: "bogus", Values: []string{"s"}}},
	})

	var apiErr interface{ ErrorCode() string }
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationException" {
		t.Fatalf("BatchGetSecretValue unknown filter key err = %v, want ValidationException", err)
	}
}

func TestSDKListSecretsNegationFilter(t *testing.T) {
	client, _ := newClockedSecretsClient(t)
	ctx := context.Background()

	for _, n := range []string{"prod-db", "dev-db"} {
		mustCreate(t, client, &awssm.CreateSecretInput{Name: aws.String(n), SecretString: aws.String("x")})
	}

	out, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{
		Filters: []smtypes.Filter{{Key: smtypes.FilterNameStringTypeName, Values: []string{"!prod"}}},
	})
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}

	if got := secretNames(out.SecretList); len(got) != 1 || got[0] != "dev-db" {
		t.Fatalf("name=!prod returned %v, want [dev-db]", got)
	}

	// Values in one filter are OR'd, negated ones included (AWS documents no
	// other combination, and moto behaves the same): every name fails at least
	// one of the two prefixes, so both secrets match. Excluding both prefixes
	// takes two filters, which are AND'd.
	out, err = client.ListSecrets(ctx, &awssm.ListSecretsInput{
		Filters: []smtypes.Filter{{Key: smtypes.FilterNameStringTypeName, Values: []string{"!prod", "!dev"}}},
	})
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}

	if len(out.SecretList) != 2 {
		t.Fatalf("name=[!prod,!dev] returned %v, want both secrets", secretNames(out.SecretList))
	}

	out, err = client.ListSecrets(ctx, &awssm.ListSecretsInput{Filters: []smtypes.Filter{
		{Key: smtypes.FilterNameStringTypeName, Values: []string{"!prod"}},
		{Key: smtypes.FilterNameStringTypeName, Values: []string{"!dev"}},
	}})
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}

	if len(out.SecretList) != 0 {
		t.Fatalf("name=!prod AND name=!dev returned %v, want none", secretNames(out.SecretList))
	}

	// No secret here is owned by another service, so "!rds" keeps them all.
	out, err = client.ListSecrets(ctx, &awssm.ListSecretsInput{
		Filters: []smtypes.Filter{{Key: smtypes.FilterNameStringTypeOwningService, Values: []string{"!rds"}}},
	})
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}

	if len(out.SecretList) != 2 {
		t.Fatalf("owning-service=!rds returned %v, want both secrets", secretNames(out.SecretList))
	}
}

// TestSDKListSecretsCursorStableAcrossInserts checks the NextToken is a cursor:
// a secret created between page calls that sorts before the cursor neither
// shifts the next page nor makes an entry repeat.
func TestSDKListSecretsCursorStableAcrossInserts(t *testing.T) {
	client, _ := newClockedSecretsClient(t)
	ctx := context.Background()

	for _, n := range []string{"b", "d", "f", "h"} {
		mustCreate(t, client, &awssm.CreateSecretInput{Name: aws.String(n), SecretString: aws.String("x")})
	}

	page1, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{MaxResults: aws.Int32(2)})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}

	mustCreate(t, client, &awssm.CreateSecretInput{Name: aws.String("a"), SecretString: aws.String("x")})

	page2, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{MaxResults: aws.Int32(2), NextToken: page1.NextToken})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}

	got := append(secretNames(page1.SecretList), secretNames(page2.SecretList)...)
	want := []string{"b", "d", "f", "h"}

	if len(got) != len(want) {
		t.Fatalf("pages = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pages = %v, want %v", got, want)
		}
	}

	if page2.NextToken != nil {
		t.Fatalf("last page NextToken = %q, want none", aws.ToString(page2.NextToken))
	}
}
