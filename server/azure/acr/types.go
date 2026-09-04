package acr

import (
	"strings"

	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// registryLoginServer is the synthetic login server reported for this mock
// registry. Real ACR uses {registry-name}.azurecr.io.
const registryLoginServer = "cloudemu.azurecr.io"

// changeableAttributes mirrors ACR's RepositoryWriteableProperties /
// TagWriteableProperties / ManifestWriteableProperties.
type changeableAttributes struct {
	DeleteEnabled bool `json:"deleteEnabled"`
	WriteEnabled  bool `json:"writeEnabled"`
	ListEnabled   bool `json:"listEnabled"`
	ReadEnabled   bool `json:"readEnabled"`
}

func allEnabled() changeableAttributes {
	return changeableAttributes{DeleteEnabled: true, WriteEnabled: true, ListEnabled: true, ReadEnabled: true}
}

// toChangeableAttributes converts the driver's always-resolved
// AzureChangeableAttributes into the wire shape. A nil field defaults to
// enabled, matching a registry that predates changeableAttributes tracking.
func toChangeableAttributes(a crdriver.AzureChangeableAttributes) changeableAttributes {
	return changeableAttributes{
		DeleteEnabled: boolOrEnabled(a.DeleteEnabled),
		WriteEnabled:  boolOrEnabled(a.WriteEnabled),
		ListEnabled:   boolOrEnabled(a.ListEnabled),
		ReadEnabled:   boolOrEnabled(a.ReadEnabled),
	}
}

func boolOrEnabled(p *bool) bool {
	if p == nil {
		return true
	}

	return *p
}

// changeableAttributesPatch is the PATCH request body for
// UpdateRepositoryProperties / UpdateTagProperties / UpdateManifestProperties:
// the raw {Repository,Tag,Manifest}WriteableProperties object, unwrapped. Every
// field is optional; an absent field leaves the corresponding attribute
// unchanged.
type changeableAttributesPatch struct {
	DeleteEnabled *bool `json:"deleteEnabled"`
	WriteEnabled  *bool `json:"writeEnabled"`
	ListEnabled   *bool `json:"listEnabled"`
	ReadEnabled   *bool `json:"readEnabled"`
}

func (p changeableAttributesPatch) toDriver() crdriver.AzureChangeableAttributes {
	return crdriver.AzureChangeableAttributes{
		DeleteEnabled: p.DeleteEnabled,
		WriteEnabled:  p.WriteEnabled,
		ListEnabled:   p.ListEnabled,
		ReadEnabled:   p.ReadEnabled,
	}
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

// tagProperties is the GET /acr/v1/{name}/_tags/{tag} body.
type tagProperties struct {
	Registry  string        `json:"registry"`
	ImageName string        `json:"imageName"`
	Tag       tagAttributes `json:"tag"`
}

// manifestAttributes is one entry in the _manifests list.
type manifestAttributes struct {
	Digest               string               `json:"digest"`
	CreatedTime          string               `json:"createdTime,omitempty"`
	LastUpdateTime       string               `json:"lastUpdateTime,omitempty"`
	MediaType            string               `json:"mediaType,omitempty"`
	ImageSize            int64                `json:"imageSize"`
	Tags                 []string             `json:"tags"`
	ChangeableAttributes changeableAttributes `json:"changeableAttributes"`
}

// manifestList is the GET /acr/v1/{name}/_manifests body.
type manifestList struct {
	Registry  string               `json:"registry"`
	ImageName string               `json:"imageName"`
	Manifests []manifestAttributes `json:"manifests"`
}

// manifestProperties is the GET /acr/v1/{name}/_manifests/{digest} body.
type manifestProperties struct {
	Registry  string             `json:"registry"`
	ImageName string             `json:"imageName"`
	Manifest  manifestAttributes `json:"manifest"`
}

func toManifestAttribute(img *crdriver.ImageDetail, attrs changeableAttributes) manifestAttributes {
	tags := img.Tags
	if tags == nil {
		tags = []string{}
	}

	return manifestAttributes{
		Digest:               img.Digest,
		CreatedTime:          img.PushedAt,
		LastUpdateTime:       img.PushedAt,
		MediaType:            img.MediaType,
		ImageSize:            img.SizeBytes,
		Tags:                 tags,
		ChangeableAttributes: attrs,
	}
}

// toManifestAttributes renders images (already filtered for listEnabled by the
// driver's ListImages) into wire form, resolving each manifest's real
// changeableAttributes via attrsFor.
func toManifestAttributes(
	images []crdriver.ImageDetail, attrsFor func(digest string) changeableAttributes,
) []manifestAttributes {
	out := make([]manifestAttributes, 0, len(images))
	for i := range images {
		out = append(out, toManifestAttribute(&images[i], attrsFor(images[i].Digest)))
	}

	return out
}

// registriesMarker precedes the repository name in the Azure resource ID the
// driver stores (…/Microsoft.ContainerRegistry/registries/{name}).
const registriesMarker = "/registries/"

// repoName recovers the bare repository name from the driver's resource-ID
// Name. It splits on the resource-type marker rather than the last slash so
// hierarchical names like "team/app" survive intact.
func repoName(name string) string {
	if idx := strings.Index(name, registriesMarker); idx >= 0 {
		return name[idx+len(registriesMarker):]
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

// toTagAttributes renders every tag on images (already filtered for
// manifest-level listEnabled by the driver's ListImages) into wire form,
// resolving each tag's real changeableAttributes via attrsFor and dropping any
// tag whose own listEnabled is false.
func toTagAttributes(
	images []crdriver.ImageDetail, attrsFor func(tag string) changeableAttributes,
) []tagAttributes {
	out := make([]tagAttributes, 0, len(images))

	for i := range images {
		img := images[i]

		for _, tag := range img.Tags {
			if tag == "" {
				continue
			}

			attrs := attrsFor(tag)
			if !attrs.ListEnabled {
				continue
			}

			out = append(out, tagAttributes{
				Name:                 tag,
				Digest:               img.Digest,
				CreatedTime:          img.PushedAt,
				LastUpdateTime:       img.PushedAt,
				ChangeableAttributes: attrs,
			})
		}
	}

	return out
}
