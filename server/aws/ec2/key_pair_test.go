package ec2_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	smithy "github.com/aws/smithy-go"
)

// TestCreateKeyPairReturnsKeyIDAndUsablePEM pins that keyPairId is a real
// key-... id (not an ARN) and that KeyMaterial parses as a PEM RSA private key
// — the old 20-byte stub broke every SSH-connect flow.
func TestCreateKeyPairReturnsKeyIDAndUsablePEM(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	kp, err := client.CreateKeyPair(ctx, &ec2.CreateKeyPairInput{
		KeyName: aws.String("web"),
	})
	if err != nil {
		t.Fatalf("CreateKeyPair: %v", err)
	}

	if id := aws.ToString(kp.KeyPairId); !strings.HasPrefix(id, "key-") {
		t.Errorf("KeyPairId = %q, want a key-... id", id)
	}

	block, _ := pem.Decode([]byte(aws.ToString(kp.KeyMaterial)))
	if block == nil {
		t.Fatalf("KeyMaterial is not PEM: %q", aws.ToString(kp.KeyMaterial))
	}
	if _, err := x509.ParsePKCS1PrivateKey(block.Bytes); err != nil {
		t.Fatalf("KeyMaterial is not a usable RSA private key: %v", err)
	}

	if fp := aws.ToString(kp.KeyFingerprint); !strings.Contains(fp, ":") {
		t.Errorf("KeyFingerprint = %q, want colon-separated hex", fp)
	}
}

// TestCreateDuplicateKeyPairErrors pins the EC2-specific InvalidKeyPair.Duplicate
// code rather than the generic ResourceAlreadyExists.
func TestCreateDuplicateKeyPairErrors(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	if _, err := client.CreateKeyPair(ctx, &ec2.CreateKeyPairInput{KeyName: aws.String("dup")}); err != nil {
		t.Fatalf("first CreateKeyPair: %v", err)
	}

	_, err := client.CreateKeyPair(ctx, &ec2.CreateKeyPairInput{KeyName: aws.String("dup")})
	if err == nil {
		t.Fatalf("duplicate CreateKeyPair returned no error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidKeyPair.Duplicate" {
		t.Fatalf("error code = %v, want InvalidKeyPair.Duplicate", err)
	}
}

// TestDescribeKeyPairsReportsCreateTime pins that DescribeKeyPairs carries a
// non-nil CreateTime.
func TestDescribeKeyPairsReportsCreateTime(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	if _, err := client.CreateKeyPair(ctx, &ec2.CreateKeyPairInput{KeyName: aws.String("timed")}); err != nil {
		t.Fatalf("CreateKeyPair: %v", err)
	}

	out, err := client.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{
		KeyNames: []string{"timed"},
	})
	if err != nil {
		t.Fatalf("DescribeKeyPairs: %v", err)
	}
	if len(out.KeyPairs) != 1 {
		t.Fatalf("DescribeKeyPairs = %d, want 1", len(out.KeyPairs))
	}
	if out.KeyPairs[0].CreateTime == nil {
		t.Errorf("CreateTime is nil")
	}
}

// TestDescribeKeyPairsUnknownNameErrors pins that an unknown key name returns
// InvalidKeyPair.NotFound rather than an empty success.
func TestDescribeKeyPairsUnknownNameErrors(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	_, err := client.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{
		KeyNames: []string{"ghost"},
	})
	if err == nil {
		t.Fatalf("DescribeKeyPairs(unknown) returned no error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidKeyPair.NotFound" {
		t.Fatalf("error code = %v, want InvalidKeyPair.NotFound", err)
	}
}
