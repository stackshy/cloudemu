package kms_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

// --- reserved aws: tag prefix ---

func TestReservedAWSTagPrefixRejected(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	_, err := m.CreateKey(ctx, driver.CreateKeyInput{Tags: map[string]string{"aws:cloudformation:stack-name": "x"}})
	wantException(t, err, driver.ExTag)

	k, err := m.CreateKey(ctx, driver.CreateKeyInput{Tags: map[string]string{"team": "a", "awsome": "ok"}})
	if err != nil {
		t.Fatalf("CreateKey with normal tags: %v", err)
	}

	for _, key := range []string{"aws:foo", "AWS:Foo", "Aws:bar"} {
		err := m.TagResource(ctx, k.KeyID, map[string]string{"ok": "1", key: "v"})
		wantException(t, err, driver.ExTag)
	}

	tags, err := m.ListResourceTags(ctx, k.KeyID)
	if err != nil {
		t.Fatal(err)
	}

	if len(tags) != 2 || tags["ok"] != "" {
		t.Fatalf("rejected TagResource must not partially apply; tags = %v", tags)
	}

	if err := m.TagResource(ctx, k.KeyID, map[string]string{"owner": "me"}); err != nil {
		t.Fatalf("TagResource normal: %v", err)
	}
}

// --- CreateGrant principal validation ---

func TestCreateGrantPrincipalValidation(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	k, err := m.CreateKey(ctx, driver.CreateKeyInput{})
	if err != nil {
		t.Fatal(err)
	}

	ops := []string{driver.GrantOpEncrypt}

	valid := []string{
		"arn:aws:iam::111122223333:root",
		"arn:aws:iam::111122223333:user/alice",
		"arn:aws:iam::111122223333:role/ExampleRole",
		"arn:aws:sts::111122223333:assumed-role/ExampleRole/session",
		"arn:aws:sts::111122223333:federated-user/bob",
		"arn:aws-us-gov:iam::111122223333:role/r",
		"dynamodb.us-east-1.amazonaws.com",
		"111122223333",
	}
	for _, p := range valid {
		if _, _, err := m.CreateGrant(ctx, driver.CreateGrantInput{KeyID: k.KeyID, GranteePrincipal: p, Operations: ops}); err != nil {
			t.Errorf("GranteePrincipal %q rejected: %v", p, err)
		}
	}

	for _, p := range []string{
		"not-an-arn", "alice", "arn:aws:s3:::bucket", "arn:aws:iam", "arn:aws:iam:::role/x",
		// iam/sts ARNs that aren't grantable principals.
		"arn:aws:iam::111122223333:group/G",
		"arn:aws:iam::111122223333:policy/P",
		"arn:aws:iam::111122223333:instance-profile/I",
		"arn:aws:iam::111122223333:user/",
		"arn:aws:iam::111122223333:role",
		"arn:aws:sts::111122223333:root",
		"arn:aws:sts::111122223333:assumed-role/",
		// IAM and STS ARNs are global: a Region is not allowed.
		"arn:aws:iam:us-east-1:111122223333:role/R",
		"arn:aws:sts:us-east-1:111122223333:assumed-role/R/s",
		// The account must be a 12-digit ID.
		"arn:aws:iam::acct:root",
	} {
		_, _, err := m.CreateGrant(ctx, driver.CreateGrantInput{KeyID: k.KeyID, GranteePrincipal: p, Operations: ops})
		wantException(t, err, driver.ExInvalidArn)
	}

	// Outside the modeled pattern → ValidationException (no exception tag).
	_, _, err = m.CreateGrant(ctx, driver.CreateGrantInput{KeyID: k.KeyID, GranteePrincipal: "has space", Operations: ops})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("pattern violation: err = %v", err)
	}

	var apiErr *driver.APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("pattern violation should be a plain validation error, got %s", apiErr.Exception)
	}

	const wantMsg = "1 validation error detected: Value 'has space' at 'granteePrincipal' failed to satisfy constraint: " +
		"Member must satisfy regular expression pattern: ^[\\w+=,.@:/-]+$"
	if msg := cerrors.Message(err); msg != wantMsg {
		t.Fatalf("pattern violation message = %q, want %q", msg, wantMsg)
	}

	long := "arn:aws:iam::111122223333:role/" + strings.Repeat("r", 256)

	_, _, err = m.CreateGrant(ctx, driver.CreateGrantInput{KeyID: k.KeyID, GranteePrincipal: long, Operations: ops})
	if msg := cerrors.Message(err); !strings.HasSuffix(msg, "at 'granteePrincipal' failed to satisfy constraint: "+
		"Member must have length less than or equal to 256") {
		t.Fatalf("length violation message = %q", msg)
	}

	// RetiringPrincipal is validated the same way.
	_, _, err = m.CreateGrant(ctx, driver.CreateGrantInput{
		KeyID: k.KeyID, GranteePrincipal: valid[0], RetiringPrincipal: "bogus", Operations: ops,
	})
	wantException(t, err, driver.ExInvalidArn)
}
