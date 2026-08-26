package secretsmanager_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

// TestSDKClientRequestTokenIdempotent guards F1: PutSecretValue with a repeated
// ClientRequestToken and identical content is idempotent (same VersionId, no new
// version), the token IS the VersionId, and reusing it with different content is
// ResourceExistsException.
func TestSDKClientRequestTokenIdempotent(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("idem"), SecretString: aws.String("v1"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	const token = "11111111-1111-1111-1111-111111111111"

	first, err := client.PutSecretValue(ctx, &awssm.PutSecretValueInput{
		SecretId: aws.String("idem"), SecretString: aws.String("v2"),
		ClientRequestToken: aws.String(token),
	})
	if err != nil {
		t.Fatalf("PutSecretValue(first): %v", err)
	}

	if aws.ToString(first.VersionId) != token {
		t.Fatalf("VersionId = %q, want the ClientRequestToken %q", aws.ToString(first.VersionId), token)
	}

	// Same token, same content: idempotent no-op, same VersionId.
	second, err := client.PutSecretValue(ctx, &awssm.PutSecretValueInput{
		SecretId: aws.String("idem"), SecretString: aws.String("v2"),
		ClientRequestToken: aws.String(token),
	})
	if err != nil {
		t.Fatalf("PutSecretValue(second): %v", err)
	}

	if aws.ToString(second.VersionId) != token {
		t.Fatalf("idempotent VersionId = %q, want %q", aws.ToString(second.VersionId), token)
	}

	// Total versions must be 2 (initial + one put), not 3.
	list, err := client.ListSecretVersionIds(ctx, &awssm.ListSecretVersionIdsInput{
		SecretId: aws.String("idem"), IncludeDeprecated: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("ListSecretVersionIds: %v", err)
	}

	if len(list.Versions) != 2 {
		t.Fatalf("version count = %d, want 2 (idempotent put did not add a version)", len(list.Versions))
	}

	// GetSecretValue by the token resolves.
	got, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{
		SecretId: aws.String("idem"), VersionId: aws.String(token),
	})
	if err != nil {
		t.Fatalf("GetSecretValue(by token): %v", err)
	}

	if aws.ToString(got.SecretString) != "v2" {
		t.Fatalf("value by token = %q, want v2", aws.ToString(got.SecretString))
	}

	// Same token, different content: ResourceExistsException.
	_, err = client.PutSecretValue(ctx, &awssm.PutSecretValueInput{
		SecretId: aws.String("idem"), SecretString: aws.String("different"),
		ClientRequestToken: aws.String(token),
	})

	var exists *smtypes.ResourceExistsException
	if !errors.As(err, &exists) {
		t.Fatalf("reused token + different content: got %v, want ResourceExistsException", err)
	}
}

// TestSDKVersionIdIsUUID guards F9: version ids are 36-char UUIDs, not counter
// ids.
func TestSDKVersionIdIsUUID(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	created, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("uuidver"), SecretString: aws.String("v1"),
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	id := aws.ToString(created.VersionId)
	if len(id) != 36 || strings.Count(id, "-") != 4 {
		t.Fatalf("VersionId = %q, want a 36-char UUID", id)
	}
}

// TestSDKPutSecretValueVersionStages guards F2: a Put with VersionStages
// [AWSPENDING] attaches exactly that label and does NOT become AWSCURRENT — the
// default GetSecretValue still returns the prior value; a Put without stages
// promotes to AWSCURRENT and demotes the prior current to AWSPREVIOUS.
func TestSDKPutSecretValueVersionStages(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	created, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("stg"), SecretString: aws.String("v1"),
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	pending, err := client.PutSecretValue(ctx, &awssm.PutSecretValueInput{
		SecretId: aws.String("stg"), SecretString: aws.String("pending"),
		VersionStages: []string{"AWSPENDING"},
	})
	if err != nil {
		t.Fatalf("PutSecretValue(AWSPENDING): %v", err)
	}

	if len(pending.VersionStages) != 1 || pending.VersionStages[0] != "AWSPENDING" {
		t.Fatalf("staged version stages = %v, want [AWSPENDING]", pending.VersionStages)
	}

	// Default read still returns the prior AWSCURRENT value.
	cur, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("stg")})
	if err != nil {
		t.Fatalf("GetSecretValue(default): %v", err)
	}

	if aws.ToString(cur.SecretString) != "v1" {
		t.Fatalf("default value = %q, want v1 (AWSCURRENT must not have moved)", aws.ToString(cur.SecretString))
	}

	if aws.ToString(cur.VersionId) != aws.ToString(created.VersionId) {
		t.Fatalf("AWSCURRENT moved off the original version")
	}

	// A Put without stages promotes to AWSCURRENT; prior current -> AWSPREVIOUS.
	promoted, err := client.PutSecretValue(ctx, &awssm.PutSecretValueInput{
		SecretId: aws.String("stg"), SecretString: aws.String("v2"),
	})
	if err != nil {
		t.Fatalf("PutSecretValue(promote): %v", err)
	}

	if len(promoted.VersionStages) != 1 || promoted.VersionStages[0] != "AWSCURRENT" {
		t.Fatalf("promoted stages = %v, want [AWSCURRENT]", promoted.VersionStages)
	}

	prev, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{
		SecretId: aws.String("stg"), VersionStage: aws.String("AWSPREVIOUS"),
	})
	if err != nil {
		t.Fatalf("GetSecretValue(AWSPREVIOUS): %v", err)
	}

	if aws.ToString(prev.SecretString) != "v1" {
		t.Fatalf("AWSPREVIOUS value = %q, want v1", aws.ToString(prev.SecretString))
	}
}

// TestSDKUpdateSecretVersionStagePromote guards F4: UpdateSecretVersionStage
// moving AWSCURRENT to a pending version promotes it (default get returns the
// new value) and auto-demotes the old current to AWSPREVIOUS — the rotation
// finishSecret step.
func TestSDKUpdateSecretVersionStagePromote(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	created, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("rotate"), SecretString: aws.String("current-val"),
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	pending, err := client.PutSecretValue(ctx, &awssm.PutSecretValueInput{
		SecretId: aws.String("rotate"), SecretString: aws.String("pending-val"),
		VersionStages: []string{"AWSPENDING"},
	})
	if err != nil {
		t.Fatalf("PutSecretValue(AWSPENDING): %v", err)
	}

	if _, err := client.UpdateSecretVersionStage(ctx, &awssm.UpdateSecretVersionStageInput{
		SecretId:            aws.String("rotate"),
		VersionStage:        aws.String("AWSCURRENT"),
		MoveToVersionId:     pending.VersionId,
		RemoveFromVersionId: created.VersionId,
	}); err != nil {
		t.Fatalf("UpdateSecretVersionStage: %v", err)
	}

	cur, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("rotate")})
	if err != nil {
		t.Fatalf("GetSecretValue(default): %v", err)
	}

	if aws.ToString(cur.SecretString) != "pending-val" {
		t.Fatalf("default value = %q, want pending-val (AWSCURRENT should have moved)", aws.ToString(cur.SecretString))
	}

	if aws.ToString(cur.VersionId) != aws.ToString(pending.VersionId) {
		t.Fatalf("AWSCURRENT did not move to the pending version")
	}

	prev, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{
		SecretId: aws.String("rotate"), VersionStage: aws.String("AWSPREVIOUS"),
	})
	if err != nil {
		t.Fatalf("GetSecretValue(AWSPREVIOUS): %v", err)
	}

	if aws.ToString(prev.SecretString) != "current-val" {
		t.Fatalf("AWSPREVIOUS value = %q, want current-val (old current demoted)", aws.ToString(prev.SecretString))
	}
}

// TestSDKUpdateSecretReturnsVersionId guards F6: UpdateSecret that changes the
// value returns the new (non-empty) VersionId.
func TestSDKUpdateSecretReturnsVersionId(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("upd"), SecretString: aws.String("v1"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	out, err := client.UpdateSecret(ctx, &awssm.UpdateSecretInput{
		SecretId: aws.String("upd"), SecretString: aws.String("v2"),
	})
	if err != nil {
		t.Fatalf("UpdateSecret: %v", err)
	}

	if aws.ToString(out.VersionId) == "" {
		t.Fatal("UpdateSecret returned empty VersionId, want the new version id")
	}
}

// TestSDKSecretARNSuffixResolvesByName guards F8: the ARN carries a 6-char
// "-XXXXXX" suffix, and GetSecretValue by the bare name still resolves.
func TestSDKSecretARNSuffixResolvesByName(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	created, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("arn-probe"), SecretString: aws.String("v"),
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	arn := aws.ToString(created.ARN)

	const marker = ":secret:"

	seg := arn[strings.LastIndex(arn, marker)+len(marker):]
	if seg == "arn-probe" {
		t.Fatalf("ARN resource segment = %q, want a -XXXXXX suffix", seg)
	}

	if !strings.HasPrefix(seg, "arn-probe-") || len(seg) != len("arn-probe-")+6 {
		t.Fatalf("ARN resource segment = %q, want arn-probe-<6 chars>", seg)
	}

	// Lookup by the bare friendly name still works.
	byName, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("arn-probe")})
	if err != nil {
		t.Fatalf("GetSecretValue(by name): %v", err)
	}

	if aws.ToString(byName.SecretString) != "v" {
		t.Fatalf("value by name = %q, want v", aws.ToString(byName.SecretString))
	}

	// Lookup by the suffixed ARN also works.
	byARN, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: created.ARN})
	if err != nil {
		t.Fatalf("GetSecretValue(by ARN): %v", err)
	}

	if aws.ToString(byARN.SecretString) != "v" {
		t.Fatalf("value by ARN = %q, want v", aws.ToString(byARN.SecretString))
	}
}

// TestSDKGetRandomPasswordRequireEachType guards F5: with RequireEachIncludedType
// the password always contains at least one of each included class.
func TestSDKGetRandomPasswordRequireEachType(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	const iterations = 50

	for i := 0; i < iterations; i++ {
		out, err := client.GetRandomPassword(ctx, &awssm.GetRandomPasswordInput{
			PasswordLength:          aws.Int64(4),
			RequireEachIncludedType: aws.Bool(true),
		})
		if err != nil {
			t.Fatalf("GetRandomPassword(iter %d): %v", i, err)
		}

		pw := aws.ToString(out.RandomPassword)
		if len([]rune(pw)) != 4 {
			t.Fatalf("password %q length = %d, want 4", pw, len([]rune(pw)))
		}

		var hasLower, hasUpper, hasDigit, hasPunct bool

		for _, c := range pw {
			switch {
			case unicode.IsLower(c):
				hasLower = true
			case unicode.IsUpper(c):
				hasUpper = true
			case unicode.IsDigit(c):
				hasDigit = true
			default:
				hasPunct = true
			}
		}

		if !hasLower || !hasUpper || !hasDigit || !hasPunct {
			t.Fatalf("password %q missing a required class (lower=%v upper=%v digit=%v punct=%v)",
				pw, hasLower, hasUpper, hasDigit, hasPunct)
		}
	}

	// PasswordLength shorter than the number of required classes is rejected.
	_, err := client.GetRandomPassword(ctx, &awssm.GetRandomPasswordInput{
		PasswordLength:          aws.Int64(2),
		RequireEachIncludedType: aws.Bool(true),
	})

	var invalid *smtypes.InvalidParameterException
	if !errors.As(err, &invalid) {
		t.Fatalf("short length + RequireEachIncludedType: got %v, want InvalidParameterException", err)
	}
}
