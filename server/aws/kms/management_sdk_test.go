package kms_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

func TestSDKGrants(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	keyID := aws.ToString(key.KeyMetadata.KeyId)

	grant, err := c.CreateGrant(ctx, &awskms.CreateGrantInput{
		KeyId:             aws.String(keyID),
		GranteePrincipal:  aws.String("arn:aws:iam::000000000000:role/app"),
		RetiringPrincipal: aws.String("arn:aws:iam::000000000000:role/admin"),
		Name:              aws.String("app-grant"),
		Operations:        []kmstypes.GrantOperation{kmstypes.GrantOperationEncrypt, kmstypes.GrantOperationDecrypt},
	})
	if err != nil {
		t.Fatalf("CreateGrant: %v", err)
	}

	if aws.ToString(grant.GrantId) == "" || aws.ToString(grant.GrantToken) == "" {
		t.Fatalf("CreateGrant returned empty id/token: %+v", grant)
	}

	grants, err := c.ListGrants(ctx, &awskms.ListGrantsInput{KeyId: aws.String(keyID)})
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}

	if len(grants.Grants) != 1 || aws.ToString(grants.Grants[0].GrantId) != aws.ToString(grant.GrantId) {
		t.Fatalf("ListGrants mismatch: %+v", grants.Grants)
	}

	// Retirable by the retiring principal.
	retirable, err := c.ListRetirableGrants(ctx, &awskms.ListRetirableGrantsInput{
		RetiringPrincipal: aws.String("arn:aws:iam::000000000000:role/admin"),
	})
	if err != nil {
		t.Fatalf("ListRetirableGrants: %v", err)
	}

	if len(retirable.Grants) != 1 {
		t.Fatalf("ListRetirableGrants = %d, want 1", len(retirable.Grants))
	}

	if _, err := c.RevokeGrant(ctx, &awskms.RevokeGrantInput{
		KeyId: aws.String(keyID), GrantId: grant.GrantId,
	}); err != nil {
		t.Fatalf("RevokeGrant: %v", err)
	}

	grants, _ = c.ListGrants(ctx, &awskms.ListGrantsInput{KeyId: aws.String(keyID)})
	if len(grants.Grants) != 0 {
		t.Fatalf("grant should be revoked, got %d", len(grants.Grants))
	}
}

func TestSDKKeyRotation(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	keyID := aws.ToString(key.KeyMetadata.KeyId)

	if _, err := c.EnableKeyRotation(ctx, &awskms.EnableKeyRotationInput{
		KeyId: aws.String(keyID), RotationPeriodInDays: aws.Int32(180),
	}); err != nil {
		t.Fatalf("EnableKeyRotation: %v", err)
	}

	st, err := c.GetKeyRotationStatus(ctx, &awskms.GetKeyRotationStatusInput{KeyId: aws.String(keyID)})
	if err != nil {
		t.Fatalf("GetKeyRotationStatus: %v", err)
	}

	if !st.KeyRotationEnabled {
		t.Fatal("rotation should be enabled")
	}

	if _, err := c.RotateKeyOnDemand(ctx, &awskms.RotateKeyOnDemandInput{KeyId: aws.String(keyID)}); err != nil {
		t.Fatalf("RotateKeyOnDemand: %v", err)
	}

	rots, err := c.ListKeyRotations(ctx, &awskms.ListKeyRotationsInput{KeyId: aws.String(keyID)})
	if err != nil {
		t.Fatalf("ListKeyRotations: %v", err)
	}

	if len(rots.Rotations) != 1 || rots.Rotations[0].RotationType != kmstypes.RotationTypeOnDemand {
		t.Fatalf("expected 1 on-demand rotation, got %+v", rots.Rotations)
	}

	if _, err := c.DisableKeyRotation(ctx, &awskms.DisableKeyRotationInput{KeyId: aws.String(keyID)}); err != nil {
		t.Fatalf("DisableKeyRotation: %v", err)
	}
}

func TestSDKKeyPolicy(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	keyID := aws.ToString(key.KeyMetadata.KeyId)

	// A default policy exists.
	names, err := c.ListKeyPolicies(ctx, &awskms.ListKeyPoliciesInput{KeyId: aws.String(keyID)})
	if err != nil {
		t.Fatalf("ListKeyPolicies: %v", err)
	}

	if len(names.PolicyNames) != 1 || names.PolicyNames[0] != "default" {
		t.Fatalf("want [default], got %v", names.PolicyNames)
	}

	custom := `{"Version":"2012-10-17","Statement":[]}`
	if _, err := c.PutKeyPolicy(ctx, &awskms.PutKeyPolicyInput{
		KeyId: aws.String(keyID), PolicyName: aws.String("default"), Policy: aws.String(custom),
	}); err != nil {
		t.Fatalf("PutKeyPolicy: %v", err)
	}

	got, err := c.GetKeyPolicy(ctx, &awskms.GetKeyPolicyInput{
		KeyId: aws.String(keyID), PolicyName: aws.String("default"),
	})
	if err != nil {
		t.Fatalf("GetKeyPolicy: %v", err)
	}

	if aws.ToString(got.Policy) != custom {
		t.Fatalf("policy roundtrip mismatch: %s", aws.ToString(got.Policy))
	}
}
