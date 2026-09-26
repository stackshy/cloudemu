package cloudformation

import (
	"context"
	"net/url"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// TemplateFetcher reads a template object from S3.
type TemplateFetcher func(ctx context.Context, bucket, key string) ([]byte, error)

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

	bucket, key, ok := parseS3URL(templateURL)
	if !ok {
		return "", cerrors.New(cerrors.InvalidArgument, msgNotS3URL)
	}

	if m.fetchTemplate == nil {
		return "", cerrors.New(cerrors.InvalidArgument, msgTemplateAccess)
	}

	data, err := m.fetchTemplate(ctx, bucket, key)
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

// parseS3URL splits an S3 object URL into bucket and key. It accepts the
// virtual-hosted form (bucket.s3.region.amazonaws.com/key) and the path form
// (s3.region.amazonaws.com/bucket/key). Any other host, such as the emulator's
// own localhost endpoint, is read as the path form.
func parseS3URL(raw string) (bucket, key string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", "", false
	}

	host := strings.ToLower(u.Hostname())
	path := strings.TrimPrefix(u.Path, "/")

	if b, found := virtualHostBucket(host); found {
		if path == "" {
			return "", "", false
		}

		return b, path, true
	}

	bucket, key, found := strings.Cut(path, "/")
	if !found || bucket == "" || key == "" {
		return "", "", false
	}

	return bucket, key, true
}

// virtualHostBucket returns the bucket label of a virtual-hosted S3 host.
func virtualHostBucket(host string) (string, bool) {
	for _, marker := range []string{".s3.", ".s3-"} {
		if i := strings.Index(host, marker); i > 0 {
			return host[:i], true
		}
	}

	return "", false
}
