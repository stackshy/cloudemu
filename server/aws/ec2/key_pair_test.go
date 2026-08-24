package ec2_test

import (
	"context"
	"crypto/md5" //nolint:gosec // AWS defines imported key fingerprints as MD5 of the public key DER
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	smithy "github.com/aws/smithy-go"
	"golang.org/x/crypto/ssh"
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

// TestImportKeyPairFingerprintAndDescribe pins that ImportKeyPair (previously
// undispatched) returns the MD5-of-DER public-key fingerprint AWS uses for
// imported keys, assigns a key-... id, and is visible to DescribeKeyPairs.
func TestImportKeyPairFingerprintAndDescribe(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	sshPub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("ssh.NewPublicKey: %v", err)
	}
	authKey := ssh.MarshalAuthorizedKey(sshPub) // "ssh-rsa AAAA...\n"

	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	sum := md5.Sum(der) //nolint:gosec // matches AWS imported-key fingerprint definition
	want := colonHexTest(sum[:])

	out, err := client.ImportKeyPair(ctx, &ec2.ImportKeyPairInput{
		KeyName:           aws.String("imported"),
		PublicKeyMaterial: authKey,
	})
	if err != nil {
		t.Fatalf("ImportKeyPair: %v", err)
	}

	if id := aws.ToString(out.KeyPairId); !strings.HasPrefix(id, "key-") {
		t.Errorf("KeyPairId = %q, want a key-... id", id)
	}
	if got := aws.ToString(out.KeyFingerprint); got != want {
		t.Errorf("KeyFingerprint = %q, want MD5-of-DER %q", got, want)
	}
	if aws.ToString(out.KeyName) != "imported" {
		t.Errorf("KeyName = %q, want imported", aws.ToString(out.KeyName))
	}

	desc, err := client.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{
		KeyNames: []string{"imported"},
	})
	if err != nil {
		t.Fatalf("DescribeKeyPairs: %v", err)
	}
	if len(desc.KeyPairs) != 1 {
		t.Fatalf("DescribeKeyPairs = %d, want 1", len(desc.KeyPairs))
	}
	if got := aws.ToString(desc.KeyPairs[0].KeyFingerprint); got != want {
		t.Errorf("described fingerprint = %q, want %q", got, want)
	}
}

// TestImportKeyPairDuplicateRejected pins the AWS error code for importing a key
// whose name already exists.
func TestImportKeyPairDuplicateRejected(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("ssh.NewPublicKey: %v", err)
	}
	authKey := ssh.MarshalAuthorizedKey(sshPub)

	if _, err := client.ImportKeyPair(ctx, &ec2.ImportKeyPairInput{
		KeyName:           aws.String("dup-import"),
		PublicKeyMaterial: authKey,
	}); err != nil {
		t.Fatalf("ImportKeyPair(first): %v", err)
	}

	_, err = client.ImportKeyPair(ctx, &ec2.ImportKeyPairInput{
		KeyName:           aws.String("dup-import"),
		PublicKeyMaterial: authKey,
	})
	if err == nil {
		t.Fatal("ImportKeyPair(duplicate) succeeded, want error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidKeyPair.Duplicate" {
		t.Fatalf("ImportKeyPair error = %v, want InvalidKeyPair.Duplicate", err)
	}
}

// TestDeleteKeyPairIdempotent pins that deleting a non-existent key pair
// succeeds (real EC2 DeleteKeyPair returns true for a missing key), so
// Terraform destroy re-runs and cleanup scripts don't fail.
func TestDeleteKeyPairIdempotent(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	if _, err := client.DeleteKeyPair(ctx, &ec2.DeleteKeyPairInput{
		KeyName: aws.String("does-not-exist"),
	}); err != nil {
		t.Fatalf("DeleteKeyPair(missing) = %v, want success", err)
	}
}

// colonHexTest formats bytes as colon-separated lowercase hex, matching the
// server's fingerprint encoding.
func colonHexTest(b []byte) string {
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02x", x)
	}

	return strings.Join(parts, ":")
}
