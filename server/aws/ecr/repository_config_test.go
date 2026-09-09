package ecr_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
)

// TestSDKECRPutImageTagMutability exercises the Terraform in-place update of
// aws_ecr_repository.image_tag_mutability: PutImageTagMutability was previously
// undispatched (UnknownOperationException). The new setting must be reflected by
// DescribeRepositories and, critically, take effect on subsequent pushes.
func TestSDKECRPutImageTagMutability(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("mut-repo"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	if _, err := client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("mut-repo"),
		ImageManifest:  aws.String(sampleManifest),
		ImageTag:       aws.String("v1"),
	}); err != nil {
		t.Fatalf("PutImage v1: %v", err)
	}

	// Flip to IMMUTABLE.
	put, err := client.PutImageTagMutability(ctx, &awsecr.PutImageTagMutabilityInput{
		RepositoryName:     aws.String("mut-repo"),
		ImageTagMutability: ecrtypes.ImageTagMutabilityImmutable,
	})
	if err != nil {
		t.Fatalf("PutImageTagMutability(IMMUTABLE): %v", err)
	}

	if put.ImageTagMutability != ecrtypes.ImageTagMutabilityImmutable {
		t.Fatalf("PutImageTagMutability echoed %q, want IMMUTABLE", put.ImageTagMutability)
	}

	if aws.ToString(put.RegistryId) != "123456789012" || aws.ToString(put.RepositoryName) != "mut-repo" {
		t.Fatalf("PutImageTagMutability response registryId=%q repositoryName=%q",
			aws.ToString(put.RegistryId), aws.ToString(put.RepositoryName))
	}

	// DescribeRepositories reflects the new value.
	desc, err := client.DescribeRepositories(ctx, &awsecr.DescribeRepositoriesInput{
		RepositoryNames: []string{"mut-repo"},
	})
	if err != nil {
		t.Fatalf("DescribeRepositories: %v", err)
	}

	if desc.Repositories[0].ImageTagMutability != ecrtypes.ImageTagMutabilityImmutable {
		t.Fatalf("DescribeRepositories mutability = %q, want IMMUTABLE",
			desc.Repositories[0].ImageTagMutability)
	}

	// The setting takes effect: moving the tag to different image content now
	// fails. (Re-pushing the byte-identical manifest is a distinct case — real
	// ECR's ImageAlreadyExistsException, covered by
	// TestSDKECRPutImageIdenticalRepushAlreadyExists below — since nothing
	// would actually change.)
	_, err = client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("mut-repo"),
		ImageManifest:  aws.String(sampleManifest + " "),
		ImageTag:       aws.String("v1"),
	})

	var already *ecrtypes.ImageTagAlreadyExistsException
	if !errors.As(err, &already) {
		t.Fatalf("re-push to IMMUTABLE repo: want ImageTagAlreadyExistsException, got %v", err)
	}

	// Flip back to MUTABLE; moving the tag to different image content succeeds
	// again (the earlier IMMUTABLE push was rejected, so v1 still points at the
	// original sampleManifest digest here — sampleManifest+" " is a new digest).
	if _, err := client.PutImageTagMutability(ctx, &awsecr.PutImageTagMutabilityInput{
		RepositoryName:     aws.String("mut-repo"),
		ImageTagMutability: ecrtypes.ImageTagMutabilityMutable,
	}); err != nil {
		t.Fatalf("PutImageTagMutability(MUTABLE): %v", err)
	}

	if _, err := client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("mut-repo"),
		ImageManifest:  aws.String(sampleManifest + " "),
		ImageTag:       aws.String("v1"),
	}); err != nil {
		t.Fatalf("re-push after MUTABLE: %v", err)
	}
}

// TestSDKECRPutImageIdenticalRepushAlreadyExists exercises real ECR's
// ImageAlreadyExistsException: re-pushing the byte-identical manifest under a
// tag it already carries is a no-op push and is rejected, regardless of the
// repository's tag mutability setting — it is distinct from
// ImageTagAlreadyExistsException, which fires only when the tag is being
// moved to a DIFFERENT digest on an IMMUTABLE repository.
func TestSDKECRPutImageIdenticalRepushAlreadyExists(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("repush-repo"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	if _, err := client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("repush-repo"),
		ImageManifest:  aws.String(sampleManifest),
		ImageTag:       aws.String("v1"),
	}); err != nil {
		t.Fatalf("PutImage v1: %v", err)
	}

	_, err := client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("repush-repo"),
		ImageManifest:  aws.String(sampleManifest),
		ImageTag:       aws.String("v1"),
	})

	var already *ecrtypes.ImageAlreadyExistsException
	if !errors.As(err, &already) {
		t.Fatalf("identical re-push: want ImageAlreadyExistsException, got %v", err)
	}
}

// TestSDKECRPutImageScanningConfiguration exercises the Terraform in-place update
// of aws_ecr_repository.image_scanning_configuration.scan_on_push, previously
// undispatched.
func TestSDKECRPutImageScanningConfiguration(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("scan-repo"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	put, err := client.PutImageScanningConfiguration(ctx, &awsecr.PutImageScanningConfigurationInput{
		RepositoryName:             aws.String("scan-repo"),
		ImageScanningConfiguration: &ecrtypes.ImageScanningConfiguration{ScanOnPush: true},
	})
	if err != nil {
		t.Fatalf("PutImageScanningConfiguration: %v", err)
	}

	if put.ImageScanningConfiguration == nil || !put.ImageScanningConfiguration.ScanOnPush {
		t.Fatalf("PutImageScanningConfiguration echoed %+v, want scanOnPush=true",
			put.ImageScanningConfiguration)
	}

	if aws.ToString(put.RepositoryName) != "scan-repo" {
		t.Fatalf("PutImageScanningConfiguration repositoryName = %q", aws.ToString(put.RepositoryName))
	}

	desc, err := client.DescribeRepositories(ctx, &awsecr.DescribeRepositoriesInput{
		RepositoryNames: []string{"scan-repo"},
	})
	if err != nil {
		t.Fatalf("DescribeRepositories: %v", err)
	}

	cfg := desc.Repositories[0].ImageScanningConfiguration
	if cfg == nil || !cfg.ScanOnPush {
		t.Fatalf("DescribeRepositories scanning config = %+v, want scanOnPush=true", cfg)
	}
}

// TestSDKECRCreateRepositoryEncryptionConfiguration exercises the
// encryptionConfiguration round-trip Terraform's aws_ecr_repository depends on:
// real ECR reports an encryptionConfiguration on every repository (default
// AES256), and re-reads it on every refresh. Omitting it made an explicit
// encryption_configuration block drift and force replacement on every apply.
func TestSDKECRCreateRepositoryEncryptionConfiguration(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	// Default: no encryptionConfiguration in the request → AES256, no kmsKey.
	def, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("enc-default"),
	})
	if err != nil {
		t.Fatalf("CreateRepository(default): %v", err)
	}

	assertEnc(t, "create default", def.Repository.EncryptionConfiguration, ecrtypes.EncryptionTypeAes256, "")

	// KMS without an explicit key → KMS with a synthesized (non-empty) key ARN.
	kms, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("enc-kms"),
		EncryptionConfiguration: &ecrtypes.EncryptionConfiguration{
			EncryptionType: ecrtypes.EncryptionTypeKms,
		},
	})
	if err != nil {
		t.Fatalf("CreateRepository(KMS): %v", err)
	}

	kmsKey := aws.ToString(kms.Repository.EncryptionConfiguration.KmsKey)
	if kms.Repository.EncryptionConfiguration.EncryptionType != ecrtypes.EncryptionTypeKms || kmsKey == "" {
		t.Fatalf("CreateRepository(KMS) enc = %+v, want KMS with non-empty kmsKey",
			kms.Repository.EncryptionConfiguration)
	}

	// The values must survive DescribeRepositories unchanged (the refresh path).
	desc, err := client.DescribeRepositories(ctx, &awsecr.DescribeRepositoriesInput{
		RepositoryNames: []string{"enc-default", "enc-kms"},
	})
	if err != nil {
		t.Fatalf("DescribeRepositories: %v", err)
	}

	for i := range desc.Repositories {
		switch aws.ToString(desc.Repositories[i].RepositoryName) {
		case "enc-default":
			assertEnc(t, "describe default", desc.Repositories[i].EncryptionConfiguration,
				ecrtypes.EncryptionTypeAes256, "")
		case "enc-kms":
			assertEnc(t, "describe kms", desc.Repositories[i].EncryptionConfiguration,
				ecrtypes.EncryptionTypeKms, kmsKey)
		}
	}
}

// assertEnc fails unless enc reports the wanted type and key.
func assertEnc(t *testing.T, where string, enc *ecrtypes.EncryptionConfiguration,
	wantType ecrtypes.EncryptionType, wantKey string,
) {
	t.Helper()

	if enc == nil {
		t.Fatalf("%s: encryptionConfiguration is nil, want %s", where, wantType)
	}

	if enc.EncryptionType != wantType {
		t.Fatalf("%s: encryptionType = %q, want %q", where, enc.EncryptionType, wantType)
	}

	if aws.ToString(enc.KmsKey) != wantKey {
		t.Fatalf("%s: kmsKey = %q, want %q", where, aws.ToString(enc.KmsKey), wantKey)
	}
}

// TestSDKECRPutImageTagMutabilityMissingRepo asserts the operation surfaces
// RepositoryNotFoundException for an unknown repository.
func TestSDKECRPutImageTagMutabilityMissingRepo(t *testing.T) {
	client := newECRClient(t)

	_, err := client.PutImageTagMutability(context.Background(), &awsecr.PutImageTagMutabilityInput{
		RepositoryName:     aws.String("ghost-repo"),
		ImageTagMutability: ecrtypes.ImageTagMutabilityImmutable,
	})

	var notFound *ecrtypes.RepositoryNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("mutability on missing repo: want RepositoryNotFoundException, got %v", err)
	}
}
