package kms_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/smithy-go"
)

// TestSDKListKeysPaginationDeterministic walks ListKeys one key per page and
// asserts every key is seen exactly once. Before the fix the handler paged over
// map-iteration order, so an offset Marker could repeat or skip keys across pages.
func TestSDKListKeysPaginationDeterministic(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	const n = 5
	want := map[string]bool{}
	for i := 0; i < n; i++ {
		out, err := c.CreateKey(ctx, &awskms.CreateKeyInput{})
		if err != nil {
			t.Fatalf("CreateKey: %v", err)
		}
		want[aws.ToString(out.KeyMetadata.KeyId)] = true
	}

	seen := map[string]int{}
	var marker *string
	for pages := 0; pages <= n+1; pages++ {
		out, err := c.ListKeys(ctx, &awskms.ListKeysInput{Limit: aws.Int32(1), Marker: marker})
		if err != nil {
			t.Fatalf("ListKeys: %v", err)
		}
		for _, k := range out.Keys {
			seen[aws.ToString(k.KeyId)]++
		}
		if !out.Truncated {
			break
		}
		marker = out.NextMarker
	}

	if len(seen) != n {
		t.Fatalf("paginated walk saw %d distinct keys, want %d (%v)", len(seen), n, seen)
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("key %s returned %d times across pages, want exactly 1", id, count)
		}
		if !want[id] {
			t.Fatalf("paginated walk returned unknown key %s", id)
		}
	}
}

// TestSDKListBadMarker sends a Marker the server never issued to every KMS list
// operation. Operations that model InvalidMarkerException return it; only
// ListKeyPolicies, whose error list has no InvalidMarkerException, answers
// ValidationException. A Marker over the modeled 1024-character limit, or with
// a character outside U+0020-U+00FF, fails request validation with
// ValidationException everywhere.
func TestSDKListBadMarker(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	keyID := key.KeyMetadata.KeyId

	if _, err := c.CreateGrant(ctx, &awskms.CreateGrantInput{
		KeyId:             keyID,
		GranteePrincipal:  aws.String("arn:aws:iam::111122223333:role/R"),
		RetiringPrincipal: aws.String("arn:aws:iam::111122223333:role/R"),
		Operations:        []kmstypes.GrantOperation{kmstypes.GrantOperationEncrypt},
	}); err != nil {
		t.Fatalf("CreateGrant: %v", err)
	}

	ops := []struct {
		name string
		call func(marker *string) error
		want string
	}{
		{"ListKeys", func(m *string) error {
			_, err := c.ListKeys(ctx, &awskms.ListKeysInput{Marker: m})
			return err
		}, "InvalidMarkerException"},
		{"ListAliases", func(m *string) error {
			_, err := c.ListAliases(ctx, &awskms.ListAliasesInput{Marker: m})
			return err
		}, "InvalidMarkerException"},
		{"ListGrants", func(m *string) error {
			_, err := c.ListGrants(ctx, &awskms.ListGrantsInput{KeyId: keyID, Marker: m})
			return err
		}, "InvalidMarkerException"},
		{"ListRetirableGrants", func(m *string) error {
			_, err := c.ListRetirableGrants(ctx, &awskms.ListRetirableGrantsInput{
				RetiringPrincipal: aws.String("arn:aws:iam::111122223333:role/R"), Marker: m,
			})
			return err
		}, "InvalidMarkerException"},
		{"ListKeyRotations", func(m *string) error {
			_, err := c.ListKeyRotations(ctx, &awskms.ListKeyRotationsInput{KeyId: keyID, Marker: m})
			return err
		}, "InvalidMarkerException"},
		{"ListResourceTags", func(m *string) error {
			_, err := c.ListResourceTags(ctx, &awskms.ListResourceTagsInput{KeyId: keyID, Marker: m})
			return err
		}, "InvalidMarkerException"},
		{"DescribeCustomKeyStores", func(m *string) error {
			_, err := c.DescribeCustomKeyStores(ctx, &awskms.DescribeCustomKeyStoresInput{Marker: m})
			return err
		}, "InvalidMarkerException"},
		{"ListKeyPolicies", func(m *string) error {
			_, err := c.ListKeyPolicies(ctx, &awskms.ListKeyPoliciesInput{KeyId: keyID, Marker: m})
			return err
		}, "ValidationException"},
	}

	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			assertErrorCode(t, op.call(aws.String("not-a-marker")), op.want)
			assertErrorCode(t, op.call(aws.String(strings.Repeat("a", 1025))), "ValidationException")
			assertErrorCode(t, op.call(aws.String("bad\u0100marker")), "ValidationException")
		})
	}
}

func assertErrorCode(t *testing.T, err error, want string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want %s, got %v", want, err)
	}

	if apiErr.ErrorCode() != want {
		t.Fatalf("want %s, got %s: %s", want, apiErr.ErrorCode(), apiErr.ErrorMessage())
	}
}
