package artifactregistry

import (
	"encoding/json"
	"strconv"
	"strings"

	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// repoFormat is the Repository.format field. The GAPIC apiv1 REST client
// serializes proto enums with UseEnumNumbers, so it sends format as a number
// (e.g. 1 for DOCKER); the raw google.golang.org/api client sends the name.
// UnmarshalJSON accepts either and normalizes to the string name so a create
// with a GAPIC client no longer 400s "cannot unmarshal number into string".
type repoFormat string

// formatNames maps the artifactregistry.v1 Repository_Format enum numbers to
// their names, matching the protobuf enum.
//
//nolint:gochecknoglobals // static enum lookup table, not mutable state.
var formatNames = map[int]string{
	0:  "FORMAT_UNSPECIFIED",
	1:  "DOCKER",
	2:  "MAVEN",
	3:  "NPM",
	5:  "APT",
	6:  "YUM",
	8:  "PYTHON",
	9:  "KFP",
	10: "GO",
	11: "GENERIC",
}

// UnmarshalJSON accepts the format as a JSON string or a numeric enum.
func (f *repoFormat) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == "" {
		return nil
	}

	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}

		*f = repoFormat(str)

		return nil
	}

	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}

	if name, ok := formatNames[n]; ok {
		*f = repoFormat(name)
	}

	return nil
}

// repositoryJSON is the artifactregistry.googleapis.com v1 Repository shape.
type repositoryJSON struct {
	Name                string                     `json:"name"`
	Format              repoFormat                 `json:"format,omitempty"`
	Mode                string                     `json:"mode,omitempty"`
	Description         string                     `json:"description,omitempty"`
	Labels              map[string]string          `json:"labels,omitempty"`
	KmsKeyName          string                     `json:"kmsKeyName,omitempty"`
	DockerConfig        *dockerConfigJSON          `json:"dockerConfig,omitempty"`
	CleanupPolicies     map[string]json.RawMessage `json:"cleanupPolicies,omitempty"`
	CleanupPolicyDryRun bool                       `json:"cleanupPolicyDryRun,omitempty"`
	SizeBytes           string                     `json:"sizeBytes,omitempty"`
	SatisfiesPzs        bool                       `json:"satisfiesPzs,omitempty"`
	CreateTime          string                     `json:"createTime,omitempty"`
	UpdateTime          string                     `json:"updateTime,omitempty"`
}

// dockerConfigJSON is the v1 DockerRepositoryConfig shape. immutableTags marks a
// Docker repository whose tags cannot be reassigned.
type dockerConfigJSON struct {
	ImmutableTags bool `json:"immutableTags,omitempty"`
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

// packageJSON is the v1 Package shape.
type packageJSON struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	CreateTime  string `json:"createTime,omitempty"`
	UpdateTime  string `json:"updateTime,omitempty"`
}

// versionJSON is the v1 Version shape.
type versionJSON struct {
	Name        string     `json:"name"`
	CreateTime  string     `json:"createTime,omitempty"`
	UpdateTime  string     `json:"updateTime,omitempty"`
	RelatedTags []tagsJSON `json:"relatedTags,omitempty"`
}

// tagsJSON is the v1 Tag shape.
type tagsJSON struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// fileJSON is the v1 File shape.
type fileJSON struct {
	Name       string `json:"name"`
	SizeBytes  string `json:"sizeBytes,omitempty"`
	Owner      string `json:"owner,omitempty"`
	CreateTime string `json:"createTime,omitempty"`
	UpdateTime string `json:"updateTime,omitempty"`
}

type listRepositoriesResponse struct {
	Repositories  []repositoryJSON `json:"repositories"`
	NextPageToken string           `json:"nextPageToken,omitempty"`
}

type listDockerImagesResponse struct {
	DockerImages  []dockerImageJSON `json:"dockerImages"`
	NextPageToken string            `json:"nextPageToken,omitempty"`
}

type listPackagesResponse struct {
	Packages      []packageJSON `json:"packages"`
	NextPageToken string        `json:"nextPageToken,omitempty"`
}

type listVersionsResponse struct {
	Versions      []versionJSON `json:"versions"`
	NextPageToken string        `json:"nextPageToken,omitempty"`
}

type listTagsResponse struct {
	Tags          []tagsJSON `json:"tags"`
	NextPageToken string     `json:"nextPageToken,omitempty"`
}

type listFilesResponse struct {
	Files         []fileJSON `json:"files"`
	NextPageToken string     `json:"nextPageToken,omitempty"`
}

// operationJSON is a google.longrunning.Operation. Artifact Registry's create
// and delete are async; the mock returns a completed operation immediately.
type operationJSON struct {
	Name     string `json:"name"`
	Done     bool   `json:"done"`
	Response any    `json:"response,omitempty"`
}

const (
	dockerFormat      = "DOCKER"
	standardMode      = "STANDARD_REPOSITORY"
	formatTag         = "cloudemu:gcpArFormat"
	descriptionTag    = "cloudemu:gcpArDescription"
	modeTag           = "cloudemu:gcpArMode"
	kmsKeyTag         = "cloudemu:gcpArKmsKeyName"
	immutableTagsTag  = crdriver.ImmutableTagsReservedTag
	cleanupPolicyTag  = "cloudemu:gcpArCleanupPolicies"
	cleanupDryRunTag  = "cloudemu:gcpArCleanupPolicyDryRun"
	reservedTagPrefix = "cloudemu:"
	trueTag           = "true"
	packagesSeg       = "packages"
	versionsSeg       = "versions"
	tagsSeg           = "tags"
	filesSeg          = "files"
)

func repositoryResourceName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/repositories/" + id
}

// repositoriesMarker precedes the repository name in the self-link the driver
// stores (projects/{p}/repositories/{name}).
const repositoriesMarker = "/repositories/"

// repoName recovers the bare repository name from the driver's self-link Name.
// It splits on the resource-type marker rather than the last slash so
// hierarchical names like "team/app" survive intact.
func repoName(name string) string {
	if idx := strings.Index(name, repositoriesMarker); idx >= 0 {
		return name[idx+len(repositoriesMarker):]
	}

	return name
}

func toRepositoryJSON(project, location string, r *crdriver.Repository, sizeBytes int64) repositoryJSON {
	format := dockerFormat
	if f := r.Tags[formatTag]; f != "" {
		format = f
	}

	mode := standardMode
	if m := r.Tags[modeTag]; m != "" {
		mode = m
	}

	updateTime := r.UpdatedAt
	if updateTime == "" {
		updateTime = r.CreatedAt
	}

	out := repositoryJSON{
		Name:        repositoryResourceName(project, location, repoName(r.Name)),
		Format:      repoFormat(format),
		Mode:        mode,
		Description: r.Tags[descriptionTag],
		Labels:      stripReservedTags(r.Tags),
		KmsKeyName:  r.Tags[kmsKeyTag],
		CreateTime:  r.CreatedAt,
		UpdateTime:  updateTime,
	}

	if sizeBytes > 0 {
		out.SizeBytes = strconv.FormatInt(sizeBytes, 10)
	}

	out.DockerConfig, out.CleanupPolicies, out.CleanupPolicyDryRun = repoExtrasFromTags(r.Tags)

	return out
}

// repoExtrasFromTags reconstructs the GCP-only Repository fields (dockerConfig,
// cleanupPolicies, cleanupPolicyDryRun) that the driver stores as reserved tags
// so they round-trip on get/list without Terraform drift.
func repoExtrasFromTags(
	tags map[string]string,
) (docker *dockerConfigJSON, policies map[string]json.RawMessage, dryRun bool) {
	if tags[immutableTagsTag] == trueTag {
		docker = &dockerConfigJSON{ImmutableTags: true}
	}

	if raw := tags[cleanupPolicyTag]; raw != "" {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &m); err == nil {
			policies = m
		}
	}

	return docker, policies, tags[cleanupDryRunTag] == trueTag
}

// stripReservedTags returns user labels without cloudemu-internal keys.
func stripReservedTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if strings.HasPrefix(k, reservedTagPrefix) {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
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
