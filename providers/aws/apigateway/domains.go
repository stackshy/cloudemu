package apigateway

import (
	"context"
	"regexp"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

var (
	_ driver.DomainNames    = (*Mock)(nil)
	_ driver.DomainResolver = (*Mock)(nil)
	_ driver.VpcLinks       = (*Mock)(nil)
)

const (
	endpointEdge       = "EDGE"
	endpointRegional   = "REGIONAL"
	tlsPolicy10        = "TLS_1_0"
	tlsPolicy12        = "TLS_1_2"
	msgDomainNotFound  = "Invalid domain name identifier specified"
	msgDomainExists    = "The domain name you provided already exists."
	msgDomainName      = "Invalid domain name: must be a valid, lowercase DNS name"
	msgDomainCert      = "A certificate is required: set certificateArn (edge) or regionalCertificateArn (regional)"
	msgSecurityPolicy  = "Invalid security policy: must be TLS_1_0 or TLS_1_2"
	msgEndpointType    = "Invalid endpoint type: must be EDGE or REGIONAL"
	msgMappingNotFound = "Invalid base path mapping identifier specified"
	msgMappingExists   = "Base path mapping already exists for this domain name"
	msgBasePath        = "Base path may only contain alphanumerics and the characters $-_.+!*'(), and must not contain /"
	basePathNone       = "(none)"
	domainAvailable    = "AVAILABLE"
	maxDomainLen       = 253
	domainSuffix       = ".cloudfront.net"
)

var (
	domainNamePattern = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)
	basePathPattern   = regexp.MustCompile(`^[A-Za-z0-9$\-_.+!*'(),]*$`)
)

// CreateDomainName registers a custom domain. EDGE domains need certificateArn
// and REGIONAL ones regionalCertificateArn.
func (m *Mock) CreateDomainName(_ context.Context, in *driver.CreateDomainNameInput) (*driver.DomainName, error) {
	endpoint, policy, err := validateDomainInput(in)
	if err != nil {
		return nil, err
	}

	dn := &driver.DomainName{
		DomainName: in.DomainName, CertificateName: in.CertificateName, CertificateARN: in.CertificateARN,
		RegionalCertificateName: in.RegionalCertificateName, RegionalCertificateARN: in.RegionalCertificateARN,
		EndpointConfigurationType: []string{endpoint}, DomainNameStatus: domainAvailable,
		SecurityPolicy: policy, Tags: copyStrMap(in.Tags),
	}
	m.assignDomainTargets(dn, endpoint)

	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	if _, exists := m.domains[dn.DomainName]; exists {
		return nil, cerrors.New(cerrors.AlreadyExists, msgDomainExists)
	}

	m.domains[dn.DomainName] = dn
	m.mappings[dn.DomainName] = map[string]*driver.BasePathMapping{}

	out := copyDomain(dn)

	return &out, nil
}

func validateDomainInput(in *driver.CreateDomainNameInput) (endpoint, policy string, err error) {
	if len(in.DomainName) > maxDomainLen || !domainNamePattern.MatchString(in.DomainName) {
		return "", "", cerrors.New(cerrors.InvalidArgument, msgDomainName)
	}

	endpoint = endpointEdge
	if len(in.EndpointConfigurationType) > 0 {
		endpoint = in.EndpointConfigurationType[0]
	}

	if endpoint != endpointEdge && endpoint != endpointRegional {
		return "", "", cerrors.New(cerrors.InvalidArgument, msgEndpointType)
	}

	if !domainHasCertificate(in, endpoint) {
		return "", "", cerrors.New(cerrors.InvalidArgument, msgDomainCert)
	}

	policy = orDefault(in.SecurityPolicy, tlsPolicy12)
	if policy != tlsPolicy10 && policy != tlsPolicy12 {
		return "", "", cerrors.New(cerrors.InvalidArgument, msgSecurityPolicy)
	}

	return endpoint, policy, nil
}

// domainHasCertificate reports whether the request carries the certificate its
// endpoint type needs: certificateArn or name (EDGE), regional ones (REGIONAL).
func domainHasCertificate(in *driver.CreateDomainNameInput, endpoint string) bool {
	if endpoint == endpointEdge {
		return in.CertificateARN != "" || in.CertificateName != ""
	}

	return in.RegionalCertificateARN != "" || in.RegionalCertificateName != ""
}

// assignDomainTargets fills the generated distribution (EDGE) or regional domain
// name and hosted zone of a new domain.
func (m *Mock) assignDomainTargets(dn *driver.DomainName, endpoint string) {
	id := strings.ReplaceAll(strings.ToLower(idgen.UUID())[:13], "-", "")

	if endpoint == endpointEdge {
		dn.DistributionDomainName = "d" + id + domainSuffix
		dn.DistributionHostedZoneID = "Z2FDTNDATAQYW2"
		dn.CertificateUploadDate = m.now()

		return
	}

	dn.RegionalDomainName = "d-" + id + ".execute-api." + m.opts.Region + ".amazonaws.com"
	dn.RegionalHostedZoneID = "Z1UJRXOUMOOFQ8"
}

// GetDomainName returns one custom domain.
func (m *Mock) GetDomainName(_ context.Context, name string) (*driver.DomainName, error) {
	m.regionMu.RLock()
	defer m.regionMu.RUnlock()

	dn, ok := m.domains[name]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgDomainNotFound)
	}

	out := copyDomain(dn)

	return &out, nil
}

// GetDomainNames lists custom domains ordered by name.
func (m *Mock) GetDomainNames(_ context.Context, page driver.PageInput) (*driver.DomainNamePage, error) {
	m.regionMu.RLock()

	all := make([]driver.DomainName, 0, len(m.domains))
	for _, dn := range m.domains {
		all = append(all, copyDomain(dn))
	}

	m.regionMu.RUnlock()

	sort.Slice(all, func(i, j int) bool { return all[i].DomainName < all[j].DomainName })

	items, next, err := pageOf(all, page)
	if err != nil {
		return nil, err
	}

	return &driver.DomainNamePage{Items: items, Position: next}, nil
}

// UpdateDomainName patches the certificate and security policy fields.
func (m *Mock) UpdateDomainName(_ context.Context, name string, ops []driver.PatchOperation) (*driver.DomainName, error) {
	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	dn, ok := m.domains[name]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgDomainNotFound)
	}

	upd := copyDomain(dn)

	for _, op := range ops {
		switch op.Path {
		case "/certificateName":
			upd.CertificateName = op.Value
		case "/certificateArn":
			upd.CertificateARN = op.Value
		case "/regionalCertificateName":
			upd.RegionalCertificateName = op.Value
		case "/regionalCertificateArn":
			upd.RegionalCertificateARN = op.Value
		case "/securityPolicy":
			if op.Value != tlsPolicy10 && op.Value != tlsPolicy12 {
				return nil, cerrors.New(cerrors.InvalidArgument, msgSecurityPolicy)
			}

			upd.SecurityPolicy = op.Value
		default:
			return nil, invalidPatchPath(op, "/certificateName", "/certificateArn", "/regionalCertificateName",
				"/regionalCertificateArn", "/securityPolicy")
		}
	}

	*dn = upd
	out := copyDomain(dn)

	return &out, nil
}

// DeleteDomainName removes a domain and its base path mappings.
func (m *Mock) DeleteDomainName(_ context.Context, name string) error {
	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	if _, ok := m.domains[name]; !ok {
		return cerrors.New(cerrors.NotFound, msgDomainNotFound)
	}

	delete(m.domains, name)
	delete(m.mappings, name)

	return nil
}

// normalizeBasePath maps the wire value "" to "(none)".
func normalizeBasePath(p string) string {
	if p == "" {
		return basePathNone
	}

	return p
}

// CreateBasePathMapping maps a base path of a domain to an API stage. An empty
// stage maps every stage of the API.
func (m *Mock) CreateBasePathMapping(
	_ context.Context, domain string, in driver.BasePathMapping,
) (*driver.BasePathMapping, error) {
	bp := normalizeBasePath(in.BasePath)
	if bp != basePathNone && (strings.Contains(bp, "/") || !basePathPattern.MatchString(bp)) {
		return nil, cerrors.New(cerrors.InvalidArgument, msgBasePath)
	}

	if err := m.checkMappingTarget(in.RestAPIID, in.Stage); err != nil {
		return nil, err
	}

	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	if _, ok := m.domains[domain]; !ok {
		return nil, cerrors.New(cerrors.NotFound, msgDomainNotFound)
	}

	if _, exists := m.mappings[domain][bp]; exists {
		return nil, cerrors.New(cerrors.AlreadyExists, msgMappingExists)
	}

	bpm := &driver.BasePathMapping{BasePath: bp, RestAPIID: in.RestAPIID, Stage: in.Stage}
	m.mappings[domain][bp] = bpm
	out := *bpm

	return &out, nil
}

// checkMappingTarget validates the API (and stage when set) of a mapping.
func (m *Mock) checkMappingTarget(restAPIID, stage string) error {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return err
	}

	if stage == "" {
		return nil
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	if _, ok := ad.stages[stage]; !ok {
		return cerrors.Newf(cerrors.NotFound, "Invalid stage identifier specified %s", stage)
	}

	return nil
}

// GetBasePathMapping returns one mapping.
func (m *Mock) GetBasePathMapping(_ context.Context, domain, basePath string) (*driver.BasePathMapping, error) {
	m.regionMu.RLock()
	defer m.regionMu.RUnlock()

	if _, ok := m.domains[domain]; !ok {
		return nil, cerrors.New(cerrors.NotFound, msgDomainNotFound)
	}

	bpm, ok := m.mappings[domain][normalizeBasePath(basePath)]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgMappingNotFound)
	}

	out := *bpm

	return &out, nil
}

// GetBasePathMappings lists a domain's mappings ordered by base path.
func (m *Mock) GetBasePathMappings(
	_ context.Context, domain string, page driver.PageInput,
) (*driver.BasePathMappingPage, error) {
	m.regionMu.RLock()

	if _, ok := m.domains[domain]; !ok {
		m.regionMu.RUnlock()

		return nil, cerrors.New(cerrors.NotFound, msgDomainNotFound)
	}

	all := make([]driver.BasePathMapping, 0, len(m.mappings[domain]))
	for _, bpm := range m.mappings[domain] {
		all = append(all, *bpm)
	}

	m.regionMu.RUnlock()

	sort.Slice(all, func(i, j int) bool { return all[i].BasePath < all[j].BasePath })

	items, next, err := pageOf(all, page)
	if err != nil {
		return nil, err
	}

	return &driver.BasePathMappingPage{Items: items, Position: next}, nil
}

// UpdateBasePathMapping patches /basePath, /restapiId and /stage.
func (m *Mock) UpdateBasePathMapping(
	_ context.Context, domain, basePath string, ops []driver.PatchOperation,
) (*driver.BasePathMapping, error) {
	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	if _, ok := m.domains[domain]; !ok {
		return nil, cerrors.New(cerrors.NotFound, msgDomainNotFound)
	}

	cur := normalizeBasePath(basePath)

	bpm, ok := m.mappings[domain][cur]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgMappingNotFound)
	}

	upd := *bpm

	for _, op := range ops {
		switch op.Path {
		case "/basePath":
			upd.BasePath = normalizeBasePath(op.Value)
		case "/restapiId":
			upd.RestAPIID = op.Value
		case "/stage":
			upd.Stage = op.Value
		default:
			return nil, invalidPatchPath(op, "/basePath", "/restapiId", "/stage")
		}
	}

	if err := m.checkMappingTarget(upd.RestAPIID, upd.Stage); err != nil {
		return nil, err
	}

	if upd.BasePath != cur {
		if _, exists := m.mappings[domain][upd.BasePath]; exists {
			return nil, cerrors.New(cerrors.AlreadyExists, msgMappingExists)
		}

		delete(m.mappings[domain], cur)
	}

	m.mappings[domain][upd.BasePath] = &upd
	out := upd

	return &out, nil
}

// DeleteBasePathMapping removes one mapping.
func (m *Mock) DeleteBasePathMapping(_ context.Context, domain, basePath string) error {
	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	if _, ok := m.domains[domain]; !ok {
		return cerrors.New(cerrors.NotFound, msgDomainNotFound)
	}

	bp := normalizeBasePath(basePath)
	if _, ok := m.mappings[domain][bp]; !ok {
		return cerrors.New(cerrors.NotFound, msgMappingNotFound)
	}

	delete(m.mappings[domain], bp)

	return nil
}

// ResolveDomain maps a request to a registered custom domain to its API and
// stage. The first path segment is the base path when a mapping for it exists;
// otherwise the "(none)" mapping serves the whole path. rest is the path left
// after the base path (and the stage, when the mapping names none).
func (m *Mock) ResolveDomain(host, path string) (restAPIID, stage, rest string, ok bool) {
	m.regionMu.RLock()
	defer m.regionMu.RUnlock()

	maps, found := m.mappings[strings.ToLower(host)]
	if !found {
		return "", "", "", false
	}

	trimmed := strings.TrimPrefix(path, "/")
	first, after, _ := strings.Cut(trimmed, "/")

	if bpm, hit := maps[first]; hit && first != "" {
		return mappedTarget(bpm, "/"+after)
	}

	if bpm, hit := maps[basePathNone]; hit {
		return mappedTarget(bpm, path)
	}

	return "", "", "", false
}

// mappedTarget resolves the API, stage and remaining path of a mapping hit; a
// mapping without a stage takes the stage from the first path segment.
func mappedTarget(bpm *driver.BasePathMapping, rest string) (apiID, stage, path string, ok bool) {
	if bpm.Stage != "" {
		return bpm.RestAPIID, bpm.Stage, rest, true
	}

	trimmed := strings.TrimPrefix(rest, "/")
	st, after, _ := strings.Cut(trimmed, "/")

	if st == "" {
		return "", "", "", false
	}

	return bpm.RestAPIID, st, "/" + after, true
}

func copyDomain(d *driver.DomainName) driver.DomainName {
	out := *d
	out.EndpointConfigurationType = copyStrSlice(d.EndpointConfigurationType)
	out.Tags = copyStrMap(d.Tags)

	return out
}
