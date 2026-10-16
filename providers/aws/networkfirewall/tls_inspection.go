package networkfirewall

import (
	"context"
	"encoding/json"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

// tlsPayload is the part of a TLSInspectionConfiguration the emulator reads: the
// server certificate ARNs it reports back as Certificates. The payload itself
// is stored untouched.
type tlsPayload struct {
	ServerCertificateConfigurations []struct {
		ServerCertificates []struct {
			ResourceArn string `json:"ResourceArn"`
		} `json:"ServerCertificates"`
	} `json:"ServerCertificateConfigurations"`
}

func (m *Mock) CreateTLSInspectionConfiguration(
	_ context.Context, cfg nfdriver.CreateTLSInspectionConfig,
) (*nfdriver.TLSInspectionConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validateName("TLSInspectionConfigurationName", cfg.Name); err != nil {
		return nil, err
	}

	if err := validateDescription(cfg.Description); err != nil {
		return nil, err
	}

	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

	if _, err := tlsCertificates(cfg.Configuration); err != nil {
		return nil, err
	}

	t := &nfdriver.TLSInspectionConfig{
		Name:          cfg.Name,
		ARN:           m.arn("tls-configuration", cfg.Name),
		ID:            idgen.GenerateID(""),
		Description:   cfg.Description,
		Configuration: cloneRawJSON(cfg.Configuration),
		Status:        statusActive,
		LastModified:  m.now(),
		UpdateToken:   newToken(),
		Tags:          copyTags(cfg.Tags),
	}
	if !m.tlsConfigs.SetIfAbsent(cfg.Name, t) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "TLS inspection configuration %q already exists", cfg.Name)
	}

	return m.outTLS(t), nil
}

func (m *Mock) DescribeTLSInspectionConfiguration(_ context.Context, name, arn string) (*nfdriver.TLSInspectionConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.findTLS(name, arn)
	if err != nil {
		return nil, err
	}

	return m.outTLS(t), nil
}

func (m *Mock) UpdateTLSInspectionConfiguration(
	_ context.Context, name, arn string, cfg nfdriver.UpdateTLSInspectionConfig,
) (*nfdriver.TLSInspectionConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := requireToken(cfg.UpdateToken); err != nil {
		return nil, err
	}

	t, err := m.findTLS(name, arn)
	if err != nil {
		return nil, err
	}

	if err := checkToken(t.UpdateToken, cfg.UpdateToken); err != nil {
		return nil, err
	}

	if err := validateDescription(cfg.Description); err != nil {
		return nil, err
	}

	if _, err := tlsCertificates(cfg.Configuration); err != nil {
		return nil, err
	}

	t.Description = cfg.Description
	t.Configuration = cloneRawJSON(cfg.Configuration)
	t.LastModified = m.now()
	t.UpdateToken = newToken()

	return m.outTLS(t), nil
}

func (m *Mock) DeleteTLSInspectionConfiguration(_ context.Context, name, arn string) (*nfdriver.TLSInspectionConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.findTLS(name, arn)
	if err != nil {
		return nil, err
	}

	if n := m.tlsRefCount(t.ARN); n > 0 {
		return nil, nfdriver.InvalidOperation("TLS inspection configuration %q is used by %d firewall policy(ies)", t.Name, n)
	}

	out := m.outTLS(t)
	out.Status = statusDeleting

	m.tlsConfigs.Delete(t.Name)

	return out, nil
}

func (m *Mock) ListTLSInspectionConfigurations(
	_ context.Context, opts nfdriver.ListOptions,
) ([]nfdriver.TLSInspectionConfig, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	items, next, err := paginate(m.tlsConfigs.SortedValues(), opts)
	if err != nil {
		return nil, "", err
	}

	out := make([]nfdriver.TLSInspectionConfig, 0, len(items))
	for _, t := range items {
		out = append(out, *m.outTLS(t))
	}

	return out, next, nil
}

func (m *Mock) findTLS(name, arn string) (*nfdriver.TLSInspectionConfig, error) {
	if err := requireIdentifier("TLSInspectionConfiguration", name, arn); err != nil {
		return nil, err
	}

	if name != "" {
		if t, ok := m.tlsConfigs.Get(name); ok {
			return t, nil
		}
	} else if t, ok := m.tlsByARN(arn); ok {
		return t, nil
	}

	return nil, cerrors.Newf(cerrors.NotFound, "TLS inspection configuration %q not found", nonEmpty(name, arn))
}

func (m *Mock) tlsByARN(arn string) (*nfdriver.TLSInspectionConfig, bool) {
	for _, t := range m.tlsConfigs.SortedValues() {
		if t.ARN == arn {
			return t, true
		}
	}

	return nil, false
}

// tlsRefCount counts the firewall policies that use the TLS inspection configuration.
func (m *Mock) tlsRefCount(arn string) int {
	n := 0

	for _, p := range m.policies.SortedValues() {
		if p.TLSInspectionConfigurationARN == arn {
			n++
		}
	}

	return n
}

func (m *Mock) outTLS(t *nfdriver.TLSInspectionConfig) *nfdriver.TLSInspectionConfig {
	out := *t
	out.Configuration = cloneRawJSON(t.Configuration)
	out.Tags = copyTags(t.Tags)
	out.NumberOfAssociations = m.tlsRefCount(t.ARN)
	out.CertificateARNs, _ = tlsCertificates(t.Configuration)

	return &out
}

// tlsCertificates validates a TLSInspectionConfiguration payload (at least one
// server certificate configuration) and returns the server certificate ARNs.
func tlsCertificates(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, invalid("TLSInspectionConfiguration is required")
	}

	var p tlsPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, invalid("TLSInspectionConfiguration is not valid: %v", err)
	}

	if len(p.ServerCertificateConfigurations) == 0 {
		return nil, invalid("TLSInspectionConfiguration needs at least one ServerCertificateConfiguration")
	}

	var arns []string

	for _, c := range p.ServerCertificateConfigurations {
		for _, s := range c.ServerCertificates {
			if s.ResourceArn != "" {
				arns = append(arns, s.ResourceArn)
			}
		}
	}

	return arns, nil
}
