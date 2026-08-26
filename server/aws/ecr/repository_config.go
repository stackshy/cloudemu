package ecr

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// imageTagMutabilitySetter is the AWS-specific PutImageTagMutability surface.
// Tag mutability is an ECR concept (Azure ACR and GCP Artifact Registry model it
// differently), so it is not part of the portable ContainerRegistry driver; the
// handler type-asserts for it rather than widening the shared interface.
type imageTagMutabilitySetter interface {
	PutImageTagMutability(ctx context.Context, repository, mutability string) (*crdriver.Repository, error)
}

// imageScanningConfigSetter is the AWS-specific PutImageScanningConfiguration
// surface, reached by the same type-assertion pattern.
type imageScanningConfigSetter interface {
	PutImageScanningConfiguration(ctx context.Context, repository string, scanOnPush bool) (*crdriver.Repository, error)
}

// putImageTagMutability updates a repository's imageTagMutability and echoes the
// new value. The mutated setting takes effect immediately: a later re-push of an
// existing tag on a now-IMMUTABLE repository fails with ImageTagAlreadyExists.
func (h *Handler) putImageTagMutability(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName     string `json:"repositoryName"`
		ImageTagMutability string `json:"imageTagMutability"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	setter, ok := h.registry.(imageTagMutabilitySetter)
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest, "ServerException", "image tag mutability not supported")
		return
	}

	repo, err := setter.PutImageTagMutability(r.Context(), req.RepositoryName, req.ImageTagMutability)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"registryId":         repo.RegistryID,
		"repositoryName":     repo.Name,
		"imageTagMutability": repo.ImageTagMutability,
	})
}

// putImageScanningConfiguration updates a repository's scan-on-push setting and
// echoes the imageScanningConfiguration object.
func (h *Handler) putImageScanningConfiguration(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName             string                  `json:"repositoryName"`
		ImageScanningConfiguration imageScanningConfigJSON `json:"imageScanningConfiguration"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	setter, ok := h.registry.(imageScanningConfigSetter)
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest, "ServerException", "image scanning configuration not supported")
		return
	}

	repo, err := setter.PutImageScanningConfiguration(r.Context(), req.RepositoryName, req.ImageScanningConfiguration.ScanOnPush)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"registryId":                 repo.RegistryID,
		"repositoryName":             repo.Name,
		"imageScanningConfiguration": imageScanningConfigJSON{ScanOnPush: repo.ScanOnPush},
	})
}
