package sfn_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// TestInvalidDefinitionMessageHasNoCodePrefix guards against the canonical
// error-code taxonomy leaking into an InvalidDefinition message. Real Step
// Functions never prefixes its message with an internal code like
// "InvalidArgument:".
func TestInvalidDefinitionMessageHasNoCodePrefix(t *testing.T) {
	m := newMock(t)

	_, _, _, err := m.CreateStateMachine(context.Background(), driver.CreateStateMachineInput{
		Name: "bad", Definition: `{"not":"asl"}`, RoleArn: "arn:aws:iam::000000000000:role/r",
	})
	if err == nil {
		t.Fatal("expected InvalidDefinition for a definition without StartAt")
	}

	if got := exceptionOf(err); got != driver.ExInvalidDefinition {
		t.Fatalf("exception = %q, want %q", got, driver.ExInvalidDefinition)
	}

	if msg := errors.Message(err); strings.Contains(msg, "InvalidArgument") {
		t.Fatalf("message leaks the code taxonomy: %q", msg)
	}
}

// TestCreateIdempotencyFoldsEncryptionConfig verifies the CreateStateMachine
// idempotency check treats a differing encryptionConfiguration as a conflict
// (StateMachineAlreadyExists), matching real Step Functions which folds
// EncryptionConfiguration into its idempotency comparison.
func TestCreateIdempotencyFoldsEncryptionConfig(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	base := driver.CreateStateMachineInput{
		Name: "sm", Definition: definition, RoleArn: "arn:aws:iam::000000000000:role/r",
		EncryptionCfgJSON: `{"type":"AWS_OWNED_KEY"}`,
	}
	if _, _, _, err := m.CreateStateMachine(ctx, base); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Identical request (including encryption) is idempotent.
	if _, _, _, err := m.CreateStateMachine(ctx, base); err != nil {
		t.Fatalf("idempotent re-create should succeed, got %v", err)
	}

	// Same name, different encryption config -> StateMachineAlreadyExists.
	diff := base
	diff.EncryptionCfgJSON = `{"type":"CUSTOMER_MANAGED_KMS_KEY","kmsKeyId":"arn:aws:kms:us-east-1:000000000000:key/abc"}`

	_, _, _, err := m.CreateStateMachine(ctx, diff)
	if got := exceptionOf(err); got != driver.ExStateMachineAlreadyExists {
		t.Fatalf("differing encryption config: exception = %q, want %q", got, driver.ExStateMachineAlreadyExists)
	}
}
