package ecr

import (
	"time"

	crdriver "github.com/stackshy/cloudemu/containerregistry/driver"
)

type tagJSON struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type imageScanningConfigJSON struct {
	ScanOnPush bool `json:"scanOnPush"`
}

type repositoryJSON struct {
	RepositoryName     string  `json:"repositoryName"`
	RepositoryURI      string  `json:"repositoryUri"`
	RegistryID         string  `json:"registryId,omitempty"`
	CreatedAt          float64 `json:"createdAt,omitempty"`
	ImageTagMutability string  `json:"imageTagMutability,omitempty"`
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
	RegistryID       string   `json:"registryId,omitempty"`
	RepositoryName   string   `json:"repositoryName"`
	ImageDigest      string   `json:"imageDigest"`
	ImageTags        []string `json:"imageTags,omitempty"`
	ImageSizeInBytes int64    `json:"imageSizeInBytes,omitempty"`
	ImagePushedAt    float64  `json:"imagePushedAt,omitempty"`
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
}

type describeRepositoriesRequest struct {
	RepositoryNames []string `json:"repositoryNames"`
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

type repositoryNameRequest struct {
	RepositoryName string `json:"repositoryName"`
}

type imageIDsRequest struct {
	RepositoryName string        `json:"repositoryName"`
	ImageIDs       []imageIDJSON `json:"imageIds"`
}

// --- response envelopes ---

type createRepositoryResponse struct {
	Repository repositoryJSON `json:"repository"`
}

type describeRepositoriesResponse struct {
	Repositories []repositoryJSON `json:"repositories"`
}

type deleteRepositoryResponse struct {
	Repository repositoryJSON `json:"repository"`
}

type putImageResponse struct {
	Image imageJSON `json:"image"`
}

type listImagesResponse struct {
	ImageIDs []imageIDJSON `json:"imageIds"`
}

type describeImagesResponse struct {
	ImageDetails []imageDetailJSON `json:"imageDetails"`
}

type batchDeleteImageResponse struct {
	ImageIDs []imageIDJSON      `json:"imageIds"`
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
		RepositoryName: r.Name,
		RepositoryURI:  r.URI,
		CreatedAt:      epochSeconds(r.CreatedAt),
	}
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
