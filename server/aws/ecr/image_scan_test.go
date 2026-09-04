package ecr_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
)

// TestSDKDescribeImageScanFindingsMissingImage guards that, for an existing
// repository but an imageId that resolves to no image, DescribeImageScanFindings
// returns ImageNotFoundException — not RepositoryNotFoundException (the finding
// was that the generic NotFound→RepositoryNotFoundException mapping mislabeled a
// missing image as a missing repository).
func TestSDKDescribeImageScanFindingsMissingImage(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("scan-repo"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	_, err := client.DescribeImageScanFindings(ctx, &awsecr.DescribeImageScanFindingsInput{
		RepositoryName: aws.String("scan-repo"),
		ImageId:        &ecrtypes.ImageIdentifier{ImageTag: aws.String("ghost")},
	})

	var imgNF *ecrtypes.ImageNotFoundException
	if !errors.As(err, &imgNF) {
		t.Fatalf("DescribeImageScanFindings(missing image) err = %v, want ImageNotFoundException", err)
	}
}

// TestSDKDescribeImageScanFindingsNoScan guards that, for an image that exists
// but has no scan results (scanOnPush disabled, StartImageScan never called),
// DescribeImageScanFindings returns ScanNotFoundException so callers can tell
// "no scan" apart from "no repository".
func TestSDKDescribeImageScanFindingsNoScan(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("scan-repo"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	if _, err := client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("scan-repo"),
		ImageManifest:  aws.String(sampleManifest),
		ImageTag:       aws.String("v1"),
	}); err != nil {
		t.Fatalf("PutImage: %v", err)
	}

	_, err := client.DescribeImageScanFindings(ctx, &awsecr.DescribeImageScanFindingsInput{
		RepositoryName: aws.String("scan-repo"),
		ImageId:        &ecrtypes.ImageIdentifier{ImageTag: aws.String("v1")},
	})

	var scanNF *ecrtypes.ScanNotFoundException
	if !errors.As(err, &scanNF) {
		t.Fatalf("DescribeImageScanFindings(no scan) err = %v, want ScanNotFoundException", err)
	}
}

// TestSDKDescribeImageScanFindingsMissingRepo guards that a genuinely missing
// repository still surfaces RepositoryNotFoundException (the image/scan
// distinction must not swallow the repository case).
func TestSDKDescribeImageScanFindingsMissingRepo(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	_, err := client.DescribeImageScanFindings(ctx, &awsecr.DescribeImageScanFindingsInput{
		RepositoryName: aws.String("ghost-repo"),
		ImageId:        &ecrtypes.ImageIdentifier{ImageTag: aws.String("v1")},
	})

	var repoNF *ecrtypes.RepositoryNotFoundException
	if !errors.As(err, &repoNF) {
		t.Fatalf("DescribeImageScanFindings(missing repo) err = %v, want RepositoryNotFoundException", err)
	}
}

// TestSDKStartImageScanMissingImage guards that StartImageScan for an imageId
// that does not resolve in an existing repository returns ImageNotFoundException
// rather than RepositoryNotFoundException.
func TestSDKStartImageScanMissingImage(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("scan-repo"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	_, err := client.StartImageScan(ctx, &awsecr.StartImageScanInput{
		RepositoryName: aws.String("scan-repo"),
		ImageId:        &ecrtypes.ImageIdentifier{ImageTag: aws.String("ghost")},
	})

	var imgNF *ecrtypes.ImageNotFoundException
	if !errors.As(err, &imgNF) {
		t.Fatalf("StartImageScan(missing image) err = %v, want ImageNotFoundException", err)
	}
}

// TestSDKImageScanRegistryID guards that StartImageScan and
// DescribeImageScanFindings echo registryId on success — real ECR includes it
// on both responses (it was previously omitted, which surfaces to SDK callers
// as a null/empty RegistryId field).
func TestSDKImageScanRegistryID(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("scan-registryid-repo"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	if _, err := client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("scan-registryid-repo"),
		ImageManifest:  aws.String(sampleManifest),
		ImageTag:       aws.String("v1"),
	}); err != nil {
		t.Fatalf("PutImage: %v", err)
	}

	start, err := client.StartImageScan(ctx, &awsecr.StartImageScanInput{
		RepositoryName: aws.String("scan-registryid-repo"),
		ImageId:        &ecrtypes.ImageIdentifier{ImageTag: aws.String("v1")},
	})
	if err != nil {
		t.Fatalf("StartImageScan: %v", err)
	}

	if aws.ToString(start.RegistryId) != "123456789012" {
		t.Fatalf("StartImageScan registryId = %q, want 123456789012", aws.ToString(start.RegistryId))
	}

	findings, err := client.DescribeImageScanFindings(ctx, &awsecr.DescribeImageScanFindingsInput{
		RepositoryName: aws.String("scan-registryid-repo"),
		ImageId:        &ecrtypes.ImageIdentifier{ImageTag: aws.String("v1")},
	})
	if err != nil {
		t.Fatalf("DescribeImageScanFindings: %v", err)
	}

	if aws.ToString(findings.RegistryId) != "123456789012" {
		t.Fatalf("DescribeImageScanFindings registryId = %q, want 123456789012", aws.ToString(findings.RegistryId))
	}
}

// TestSDKDescribeImagesScanStatus guards that DescribeImages includes
// imageScanStatus once an image has scan results (scanOnPush or an explicit
// StartImageScan), and omits it — matching real ECR, which models it as
// optional — for an image that has never been scanned.
func TestSDKDescribeImagesScanStatus(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("scan-status-repo"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	if _, err := client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("scan-status-repo"),
		ImageManifest:  aws.String(sampleManifest),
		ImageTag:       aws.String("scanned"),
	}); err != nil {
		t.Fatalf("PutImage scanned: %v", err)
	}

	if _, err := client.PutImage(ctx, &awsecr.PutImageInput{
		RepositoryName: aws.String("scan-status-repo"),
		ImageManifest:  aws.String(sampleManifest + " unscanned"),
		ImageTag:       aws.String("unscanned"),
	}); err != nil {
		t.Fatalf("PutImage unscanned: %v", err)
	}

	if _, err := client.StartImageScan(ctx, &awsecr.StartImageScanInput{
		RepositoryName: aws.String("scan-status-repo"),
		ImageId:        &ecrtypes.ImageIdentifier{ImageTag: aws.String("scanned")},
	}); err != nil {
		t.Fatalf("StartImageScan: %v", err)
	}

	desc, err := client.DescribeImages(ctx, &awsecr.DescribeImagesInput{
		RepositoryName: aws.String("scan-status-repo"),
	})
	if err != nil {
		t.Fatalf("DescribeImages: %v", err)
	}

	var scannedStatus, unscannedStatus *ecrtypes.ImageScanStatus

	for i := range desc.ImageDetails {
		d := &desc.ImageDetails[i]
		for _, tag := range d.ImageTags {
			switch tag {
			case "scanned":
				scannedStatus = d.ImageScanStatus
			case "unscanned":
				unscannedStatus = d.ImageScanStatus
			}
		}
	}

	if scannedStatus == nil || scannedStatus.Status != ecrtypes.ScanStatusComplete {
		t.Fatalf("DescribeImages scanned image imageScanStatus = %+v, want COMPLETE", scannedStatus)
	}

	if unscannedStatus != nil {
		t.Fatalf("DescribeImages unscanned image imageScanStatus = %+v, want omitted (nil)", unscannedStatus)
	}
}

// TestSDKDeleteRepositoryPolicyNoPolicy guards that DeleteRepositoryPolicy on an
// existing repository that has no policy set returns
// RepositoryPolicyNotFoundException — consistent with GetRepositoryPolicy, and
// not RepositoryNotFoundException.
func TestSDKDeleteRepositoryPolicyNoPolicy(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("policy-repo"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	_, err := client.DeleteRepositoryPolicy(ctx, &awsecr.DeleteRepositoryPolicyInput{
		RepositoryName: aws.String("policy-repo"),
	})

	var polNF *ecrtypes.RepositoryPolicyNotFoundException
	if !errors.As(err, &polNF) {
		t.Fatalf("DeleteRepositoryPolicy(no policy) err = %v, want RepositoryPolicyNotFoundException", err)
	}

	// A genuinely missing repository still reports RepositoryNotFoundException.
	_, err = client.DeleteRepositoryPolicy(ctx, &awsecr.DeleteRepositoryPolicyInput{
		RepositoryName: aws.String("ghost-repo"),
	})

	var repoNF *ecrtypes.RepositoryNotFoundException
	if !errors.As(err, &repoNF) {
		t.Fatalf("DeleteRepositoryPolicy(missing repo) err = %v, want RepositoryNotFoundException", err)
	}
}
