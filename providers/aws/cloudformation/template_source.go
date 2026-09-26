package cloudformation

import (
	"context"
	"net/url"
	"regexp"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// TemplateFetcher reads a template object from S3. An empty versionID reads
// the current version.
type TemplateFetcher func(ctx context.Context, bucket, key, versionID string) ([]byte, error)

// Error texts for a TemplateURL CloudFormation cannot use.
const (
	msgBothTemplates  = "You cannot specify both Template URL and Template Body"
	msgNotS3URL       = "TemplateURL must be an Amazon S3 URL."
	msgTemplateAccess = "TemplateURL must reference a valid S3 object to which you have access."
)

// SetTemplateFetcher installs the S3 reader TemplateURL uses. The provider
// factory wires it to the emulated S3 service.
func (m *Mock) SetTemplateFetcher(f TemplateFetcher) {
	m.fetchTemplate = f
}

// templateBody returns the template a request names, from its body or by
// reading its TemplateURL from S3.
func (m *Mock) templateBody(ctx context.Context, body, templateURL string) (string, error) {
	switch {
	case body != "" && templateURL != "":
		return "", cerrors.New(cerrors.InvalidArgument, msgBothTemplates)
	case body != "":
		return body, nil
	case templateURL == "":
		return "", cerrors.New(cerrors.InvalidArgument, cfn.MsgNoTemplate)
	}

	obj, ok := parseS3URL(templateURL)
	if !ok {
		return "", cerrors.New(cerrors.InvalidArgument, msgNotS3URL)
	}

	if m.fetchTemplate == nil {
		return "", cerrors.New(cerrors.InvalidArgument, msgTemplateAccess)
	}

	data, err := m.fetchTemplate(ctx, obj.bucket, obj.key, obj.versionID)
	if err != nil {
		return "", cerrors.New(cerrors.InvalidArgument, msgTemplateAccess)
	}

	return string(data), nil
}

// ValidateTemplate parses a template and reports its parameters, description,
// required capabilities and transforms. TemplateBody wins over TemplateURL.
func (m *Mock) ValidateTemplate(ctx context.Context, in *cfn.ValidateTemplateInput) (*cfn.TemplateSummary, error) {
	templateURL := in.TemplateURL
	if in.TemplateBody != "" {
		templateURL = ""
	}

	body, err := m.templateBody(ctx, in.TemplateBody, templateURL)
	if err != nil {
		return nil, err
	}

	t, err := cfn.ParseTemplate(body)
	if err != nil {
		return nil, err
	}

	return cfn.Summarize(t), nil
}

// s3Object names the object a TemplateURL points at.
type s3Object struct {
	bucket, key, versionID string
}

// awsS3HostRE matches an Amazon S3 endpoint host. The optional first group is
// the bucket of a virtual-hosted URL. The rest covers s3, s3.<region>,
// s3-<region> and s3.dualstack.<region>.
var awsS3HostRE = regexp.MustCompile(`^(?:(.+)\.)?s3(?:[.-][a-z0-9-]+)*\.amazonaws\.com(?:\.cn)?$`)

// parseS3URL splits an S3 object URL into bucket, key and optional versionId.
// Amazon S3 hosts must use https, in the virtual-hosted form
// (bucket.s3.region.amazonaws.com/key) or the path form
// (s3.region.amazonaws.com/bucket/key). The emulator's own local endpoint is
// also accepted, since it serves the same S3. Any other host is rejected, as
// CloudFormation does.
func parseS3URL(raw string) (s3Object, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return s3Object{}, false
	}

	host := strings.ToLower(u.Hostname())
	path := strings.TrimPrefix(u.Path, "/")
	version := u.Query().Get("versionId")

	var bucket string

	switch {
	case u.Scheme == "https" && awsS3HostRE.MatchString(host):
		bucket = awsS3HostRE.FindStringSubmatch(host)[1]
	case (u.Scheme == "http" || u.Scheme == "https") && isLocalHost(host):
		bucket, _ = localVirtualBucket(host)
	default:
		return s3Object{}, false
	}

	if bucket == "" {
		var found bool
		if bucket, path, found = strings.Cut(path, "/"); !found || bucket == "" {
			return s3Object{}, false
		}
	}

	if path == "" {
		return s3Object{}, false
	}

	return s3Object{bucket: bucket, key: path, versionID: version}, true
}

// localSuffix is the wildcard DNS name LocalStack-style tooling uses for
// virtual-hosted S3 on the local machine.
const localSuffix = ".s3.localhost.localstack.cloud"

// isLocalHost reports the emulator's own endpoint on this machine or in Docker.
func isLocalHost(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1", "host.docker.internal", "localhost.localstack.cloud",
		"s3.localhost.localstack.cloud":
		return true
	}

	return strings.HasSuffix(host, localSuffix)
}

// localVirtualBucket returns the bucket label of a local virtual-hosted URL.
func localVirtualBucket(host string) (string, bool) {
	if b, found := strings.CutSuffix(host, localSuffix); found && b != "" {
		return b, true
	}

	return "", false
}
