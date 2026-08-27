package kms_test

import (
	"context"
	"testing"

	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// TestSDKKeyMetadataAlgorithmLists checks that CreateKey and DescribeKey both
// advertise the algorithm list matching a key's KeySpec + KeyUsage, as real KMS
// does: SigningAlgorithms for SIGN_VERIFY, EncryptionAlgorithms for
// ENCRYPT_DECRYPT, MacAlgorithms for HMAC keys.
func TestSDKKeyMetadataAlgorithmLists(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	cases := []struct {
		name    string
		spec    kmstypes.KeySpec
		usage   kmstypes.KeyUsageType
		signing []kmstypes.SigningAlgorithmSpec
		encrypt []kmstypes.EncryptionAlgorithmSpec
		mac     []kmstypes.MacAlgorithmSpec
	}{
		{
			name:  "RSA_2048 SIGN_VERIFY",
			spec:  kmstypes.KeySpecRsa2048,
			usage: kmstypes.KeyUsageTypeSignVerify,
			signing: []kmstypes.SigningAlgorithmSpec{
				kmstypes.SigningAlgorithmSpecRsassaPssSha256,
				kmstypes.SigningAlgorithmSpecRsassaPssSha384,
				kmstypes.SigningAlgorithmSpecRsassaPssSha512,
				kmstypes.SigningAlgorithmSpecRsassaPkcs1V15Sha256,
				kmstypes.SigningAlgorithmSpecRsassaPkcs1V15Sha384,
				kmstypes.SigningAlgorithmSpecRsassaPkcs1V15Sha512,
			},
		},
		{
			name:  "RSA_2048 ENCRYPT_DECRYPT",
			spec:  kmstypes.KeySpecRsa2048,
			usage: kmstypes.KeyUsageTypeEncryptDecrypt,
			encrypt: []kmstypes.EncryptionAlgorithmSpec{
				kmstypes.EncryptionAlgorithmSpecRsaesOaepSha1,
				kmstypes.EncryptionAlgorithmSpecRsaesOaepSha256,
			},
		},
		{
			name:    "ECC_NIST_P256 SIGN_VERIFY",
			spec:    kmstypes.KeySpecEccNistP256,
			usage:   kmstypes.KeyUsageTypeSignVerify,
			signing: []kmstypes.SigningAlgorithmSpec{kmstypes.SigningAlgorithmSpecEcdsaSha256},
		},
		{
			name:    "ECC_NIST_P521 SIGN_VERIFY",
			spec:    kmstypes.KeySpecEccNistP521,
			usage:   kmstypes.KeyUsageTypeSignVerify,
			signing: []kmstypes.SigningAlgorithmSpec{kmstypes.SigningAlgorithmSpecEcdsaSha512},
		},
		{
			name:  "HMAC_256 GENERATE_VERIFY_MAC",
			spec:  kmstypes.KeySpecHmac256,
			usage: kmstypes.KeyUsageTypeGenerateVerifyMac,
			mac:   []kmstypes.MacAlgorithmSpec{kmstypes.MacAlgorithmSpecHmacSha256},
		},
		{
			name:    "SYMMETRIC_DEFAULT ENCRYPT_DECRYPT",
			spec:    kmstypes.KeySpecSymmetricDefault,
			usage:   kmstypes.KeyUsageTypeEncryptDecrypt,
			encrypt: []kmstypes.EncryptionAlgorithmSpec{kmstypes.EncryptionAlgorithmSpecSymmetricDefault},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			created, err := c.CreateKey(ctx, &awskms.CreateKeyInput{KeySpec: tc.spec, KeyUsage: tc.usage})
			if err != nil {
				t.Fatalf("CreateKey: %v", err)
			}

			assertAlgorithms(t, "CreateKey", created.KeyMetadata, tc.signing, tc.encrypt, tc.mac)

			desc, err := c.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: created.KeyMetadata.KeyId})
			if err != nil {
				t.Fatalf("DescribeKey: %v", err)
			}

			assertAlgorithms(t, "DescribeKey", desc.KeyMetadata, tc.signing, tc.encrypt, tc.mac)
		})
	}
}

func assertAlgorithms(
	t *testing.T, op string, md *kmstypes.KeyMetadata,
	signing []kmstypes.SigningAlgorithmSpec,
	encrypt []kmstypes.EncryptionAlgorithmSpec,
	mac []kmstypes.MacAlgorithmSpec,
) {
	t.Helper()

	if !equalSpecs(md.SigningAlgorithms, signing) {
		t.Fatalf("%s SigningAlgorithms = %v, want %v", op, md.SigningAlgorithms, signing)
	}

	if !equalSpecs(md.EncryptionAlgorithms, encrypt) {
		t.Fatalf("%s EncryptionAlgorithms = %v, want %v", op, md.EncryptionAlgorithms, encrypt)
	}

	if !equalSpecs(md.MacAlgorithms, mac) {
		t.Fatalf("%s MacAlgorithms = %v, want %v", op, md.MacAlgorithms, mac)
	}
}

// equalSpecs compares two ordered lists of enum-string specs of any type.
func equalSpecs[A, B ~string](got []A, want []B) bool {
	if len(got) != len(want) {
		return false
	}

	for i := range got {
		if string(got[i]) != string(want[i]) {
			return false
		}
	}

	return true
}
