package acr

import (
	"strings"

	crdriver "github.com/stackshy/cloudemu/containerregistry/driver"
)

// registryLoginServer is the synthetic login server reported for this mock
// registry. Real ACR uses {registry-name}.azurecr.io.
const registryLoginServer = "cloudemu.azurecr.io"

// changeableAttributes mirrors ACR's RepositoryWriteableProperties /
// TagWriteableProperties. The mock reports everything enabled.
type changeableAttributes struct {
	DeleteEnabled bool `json:"deleteEnabled"`
	WriteEnabled  bool `json:"writeEnabled"`
	ListEnabled   bool `json:"listEnabled"`
	ReadEnabled   bool `json:"readEnabled"`
}

func allEnabled() changeableAttributes {
	return changeableAttributes{DeleteEnabled: true, WriteEnabled: true, ListEnabled: true, ReadEnabled: true}
}

// catalogResponse is the GET /acr/v1/_catalog body.
type catalogResponse struct {
	Repositories []string `json:"repositories"`
}

// repositoryProperties is the GET /acr/v1/{name} body.
type repositoryProperties struct {
	Registry             string               `json:"registry"`
	ImageName            string               `json:"imageName"`
	CreatedTime          string               `json:"createdTime,omitempty"`
	LastUpdateTime       string               `json:"lastUpdateTime,omitempty"`
	ManifestCount        int                  `json:"manifestCount"`
	TagCount             int                  `json:"tagCount"`
	ChangeableAttributes changeableAttributes `json:"changeableAttributes"`
}

// tagAttributes is one entry in the _tags list.
type tagAttributes struct {
	Name                 string               `json:"name"`
	Digest               string               `json:"digest"`
	CreatedTime          string               `json:"createdTime,omitempty"`
	LastUpdateTime       string               `json:"lastUpdateTime,omitempty"`
	ChangeableAttributes changeableAttributes `json:"changeableAttributes"`
}

// tagListResponse is the GET /acr/v1/{name}/_tags body.
type tagListResponse struct {
	Registry  string          `json:"registry"`
	ImageName string          `json:"imageName"`
	Tags      []tagAttributes `json:"tags"`
}

// deleteRepositoryResponse is the DELETE /acr/v1/{name} body.
type deleteRepositoryResponse struct {
	ManifestsDeleted []string `json:"manifestsDeleted"`
	TagsDeleted      []string `json:"tagsDeleted"`
}

// shortID returns the final path segment, so an Azure resource-ID repository
// name resolves back to the bare repository name used on the wire.
func shortID(name string) string {
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		return name[idx+1:]
	}

	return name
}

func countTags(images []crdriver.ImageDetail) int {
	n := 0

	for i := range images {
		for _, tag := range images[i].Tags {
			if tag != "" {
				n++
			}
		}
	}

	return n
}

func toTagAttributes(images []crdriver.ImageDetail) []tagAttributes {
	out := make([]tagAttributes, 0, len(images))

	for i := range images {
		img := images[i]

		for _, tag := range img.Tags {
			if tag == "" {
				continue
			}

			out = append(out, tagAttributes{
				Name:                 tag,
				Digest:               img.Digest,
				CreatedTime:          img.PushedAt,
				LastUpdateTime:       img.PushedAt,
				ChangeableAttributes: allEnabled(),
			})
		}
	}

	return out
}
