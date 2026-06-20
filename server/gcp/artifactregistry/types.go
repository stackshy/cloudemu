package artifactregistry

import (
	"strconv"
	"strings"

	crdriver "github.com/stackshy/cloudemu/containerregistry/driver"
)

// repositoryJSON is the artifactregistry.googleapis.com v1 Repository shape.
type repositoryJSON struct {
	Name        string            `json:"name"`
	Format      string            `json:"format,omitempty"`
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	CreateTime  string            `json:"createTime,omitempty"`
	UpdateTime  string            `json:"updateTime,omitempty"`
}

// dockerImageJSON is the v1 DockerImage shape. imageSizeBytes is an int64
// rendered as a string, the Google APIs convention.
type dockerImageJSON struct {
	Name           string   `json:"name"`
	URI            string   `json:"uri,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	ImageSizeBytes string   `json:"imageSizeBytes,omitempty"`
	MediaType      string   `json:"mediaType,omitempty"`
	UploadTime     string   `json:"uploadTime,omitempty"`
	UpdateTime     string   `json:"updateTime,omitempty"`
}

type listRepositoriesResponse struct {
	Repositories  []repositoryJSON `json:"repositories"`
	NextPageToken string           `json:"nextPageToken,omitempty"`
}

type listDockerImagesResponse struct {
	DockerImages  []dockerImageJSON `json:"dockerImages"`
	NextPageToken string            `json:"nextPageToken,omitempty"`
}

// operationJSON is a google.longrunning.Operation. Artifact Registry's create
// and delete are async; the mock returns a completed operation immediately.
type operationJSON struct {
	Name     string `json:"name"`
	Done     bool   `json:"done"`
	Response any    `json:"response,omitempty"`
}

const dockerFormat = "DOCKER"

func repositoryResourceName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/repositories/" + id
}

// shortID returns the final path segment, so a driver repository name of either
// "myrepo" or "projects/x/repositories/myrepo" yields "myrepo".
func shortID(name string) string {
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		return name[idx+1:]
	}

	return name
}

func toRepositoryJSON(project, location string, r *crdriver.Repository) repositoryJSON {
	return repositoryJSON{
		Name:       repositoryResourceName(project, location, shortID(r.Name)),
		Format:     dockerFormat,
		Labels:     r.Tags,
		CreateTime: r.CreatedAt,
		UpdateTime: r.CreatedAt,
	}
}

func toDockerImageJSON(project, location, repo string, d *crdriver.ImageDetail) dockerImageJSON {
	base := repositoryResourceName(project, location, repo) + "/dockerImages/" + d.Digest

	uri := d.Repository
	if d.Digest != "" {
		uri += "@" + d.Digest
	}

	return dockerImageJSON{
		Name:           base,
		URI:            uri,
		Tags:           d.Tags,
		ImageSizeBytes: strconv.FormatInt(d.SizeBytes, 10),
		MediaType:      d.MediaType,
		UploadTime:     d.PushedAt,
		UpdateTime:     d.PushedAt,
	}
}
