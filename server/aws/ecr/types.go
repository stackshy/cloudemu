package ecr

import (
	"time"

	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

type tagJSON struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type imageScanningConfigJSON struct {
	ScanOnPush bool `json:"scanOnPush"`
}

// encryptionConfigJSON mirrors ECR's EncryptionConfiguration object. Real ECR
// reports an encryptionConfiguration on every repository (default AES256), and
// Terraform's aws_ecr_repository reads it back on every refresh — omitting it
// makes an explicit encryption_configuration block drift and force replacement.
type encryptionConfigJSON struct {
	EncryptionType string `json:"encryptionType"`
	KmsKey         string `json:"kmsKey,omitempty"`
}

type repositoryJSON struct {
	RepositoryArn              string                   `json:"repositoryArn,omitempty"`
	RepositoryName             string                   `json:"repositoryName"`
	RepositoryURI              string                   `json:"repositoryUri"`
	RegistryID                 string                   `json:"registryId,omitempty"`
	CreatedAt                  float64                  `json:"createdAt,omitempty"`
	ImageTagMutability         string                   `json:"imageTagMutability,omitempty"`
	ImageScanningConfiguration *imageScanningConfigJSON `json:"imageScanningConfiguration,omitempty"`
	EncryptionConfiguration    *encryptionConfigJSON    `json:"encryptionConfiguration,omitempty"`
}

type imageIDJSON struct {
	ImageDigest string `json:"imageDigest,omitempty"`
	ImageTag    string `json:"imageTag,omitempty"`
}

type imageJSON struct {
	RegistryID             string      `json:"registryId,omitempty"`
	RepositoryName         string      `json:"repositoryName"`
	ImageID                imageIDJSON `json:"imageId"`
	ImageManifest          string      `json:"imageManifest,omitempty"`
	ImageManifestMediaType string      `json:"imageManifestMediaType,omitempty"`
}

type imageDetailJSON struct {
	RegistryID       string               `json:"registryId,omitempty"`
	RepositoryName   string               `json:"repositoryName"`
	ImageDigest      string               `json:"imageDigest"`
	ImageTags        []string             `json:"imageTags,omitempty"`
	ImageSizeInBytes int64                `json:"imageSizeInBytes,omitempty"`
	ImagePushedAt    float64              `json:"imagePushedAt,omitempty"`
	ImageScanStatus  *imageScanStatusJSON `json:"imageScanStatus,omitempty"`
}

// imageScanStatusJSON mirrors ECR's ImageScanStatus object, echoed on
// DescribeImages once an image has scan results (from scanOnPush or an
// explicit StartImageScan) — omitted, as in real ECR, when no scan has run.
type imageScanStatusJSON struct {
	Status string `json:"status"`
}

type imageFailureJSON struct {
	ImageID       imageIDJSON `json:"imageId"`
	FailureCode   string      `json:"failureCode"`
	FailureReason string      `json:"failureReason"`
}

// --- request envelopes ---

type createRepositoryRequest struct {
	RepositoryName             string                  `json:"repositoryName"`
	Tags                       []tagJSON               `json:"tags"`
	ImageScanningConfiguration imageScanningConfigJSON `json:"imageScanningConfiguration"`
	ImageTagMutability         string                  `json:"imageTagMutability"`
	EncryptionConfiguration    *encryptionConfigJSON   `json:"encryptionConfiguration"`
}

type describeRepositoriesRequest struct {
	RepositoryNames []string `json:"repositoryNames"`
	MaxResults      int      `json:"maxResults"`
	NextToken       string   `json:"nextToken"`
}

type deleteRepositoryRequest struct {
	RepositoryName string `json:"repositoryName"`
	Force          bool   `json:"force"`
}

type putImageRequest struct {
	RepositoryName         string `json:"repositoryName"`
	ImageManifest          string `json:"imageManifest"`
	ImageManifestMediaType string `json:"imageManifestMediaType"`
	ImageTag               string `json:"imageTag"`
	ImageDigest            string `json:"imageDigest"`
}

// imageFilterJSON is the ECR filter object; tagStatus is TAGGED, UNTAGGED, or
// ANY (ListImages/DescribeImages).
type imageFilterJSON struct {
	TagStatus string `json:"tagStatus"`
}

type repositoryNameRequest struct {
	RepositoryName string          `json:"repositoryName"`
	Filter         imageFilterJSON `json:"filter"`
	MaxResults     int             `json:"maxResults"`
	NextToken      string          `json:"nextToken"`
}

type imageIDsRequest struct {
	RepositoryName string          `json:"repositoryName"`
	ImageIDs       []imageIDJSON   `json:"imageIds"`
	Filter         imageFilterJSON `json:"filter"`
	MaxResults     int             `json:"maxResults"`
	NextToken      string          `json:"nextToken"`
}

// --- response envelopes ---

type createRepositoryResponse struct {
	Repository repositoryJSON `json:"repository"`
}

type describeRepositoriesResponse struct {
	Repositories []repositoryJSON `json:"repositories"`
	NextToken    string           `json:"nextToken,omitempty"`
}

type deleteRepositoryResponse struct {
	Repository repositoryJSON `json:"repository"`
}

type putImageResponse struct {
	Image imageJSON `json:"image"`
}

type listImagesResponse struct {
	ImageIDs  []imageIDJSON `json:"imageIds"`
	NextToken string        `json:"nextToken,omitempty"`
}

type describeImagesResponse struct {
	ImageDetails []imageDetailJSON `json:"imageDetails"`
	NextToken    string            `json:"nextToken,omitempty"`
}

type batchDeleteImageResponse struct {
	ImageIDs []imageIDJSON      `json:"imageIds"`
	Failures []imageFailureJSON `json:"failures"`
}

type batchGetImageResponse struct {
	Images   []imageJSON        `json:"images"`
	Failures []imageFailureJSON `json:"failures"`
}

// epochSeconds converts an RFC3339 timestamp to Unix epoch seconds, the form
// the AWS JSON protocol uses for timestamp fields. Returns 0 on parse failure.
func epochSeconds(iso string) float64 {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return 0
	}

	return float64(t.Unix())
}

func toRepositoryJSON(r *crdriver.Repository) repositoryJSON {
	return repositoryJSON{
		RepositoryArn:              r.Arn,
		RepositoryName:             r.Name,
		RepositoryURI:              r.URI,
		RegistryID:                 r.RegistryID,
		CreatedAt:                  epochSeconds(r.CreatedAt),
		ImageTagMutability:         r.ImageTagMutability,
		ImageScanningConfiguration: &imageScanningConfigJSON{ScanOnPush: r.ScanOnPush},
		EncryptionConfiguration:    toEncryptionConfigJSON(r),
	}
}

// toEncryptionConfigJSON renders a repository's encryption configuration. Real
// ECR always reports one, defaulting to AES256 for repositories created before
// the field was modeled, so a repository with no stored type is reported as
// AES256 rather than omitted.
func toEncryptionConfigJSON(r *crdriver.Repository) *encryptionConfigJSON {
	encType := r.EncryptionType
	if encType == "" {
		encType = "AES256"
	}

	return &encryptionConfigJSON{EncryptionType: encType, KmsKey: r.KmsKey}
}

func toImageDetailJSON(d *crdriver.ImageDetail) imageDetailJSON {
	return imageDetailJSON{
		RegistryID:       d.RegistryID,
		RepositoryName:   d.Repository,
		ImageDigest:      d.Digest,
		ImageTags:        d.Tags,
		ImageSizeInBytes: d.SizeBytes,
		ImagePushedAt:    epochSeconds(d.PushedAt),
	}
}

// imageReference picks the digest if present, otherwise the tag — the form the
// driver's findImage resolves.
func imageReference(id imageIDJSON) string {
	if id.ImageDigest != "" {
		return id.ImageDigest
	}

	return id.ImageTag
}
