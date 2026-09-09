package emr

import (
	"context"
	"net/http"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// securityConfig is a named, JSON-bodied EMR security configuration. It is a
// standalone control-plane object (not owned by any cluster): CreateSecurityConfiguration
// stores one, RunJobFlow references it by name via Cluster.SecurityConfiguration.
type securityConfig struct {
	name    string
	config  string
	created time.Time
}

// --- wire shapes ---

// createSecurityConfigurationInput mirrors the SDK CreateSecurityConfigurationInput.
type createSecurityConfigurationInput struct {
	Name                  *string `json:"Name"`
	SecurityConfiguration *string `json:"SecurityConfiguration"`
}

// createSecurityConfigurationOutput mirrors the SDK CreateSecurityConfigurationOutput.
type createSecurityConfigurationOutput struct {
	Name             string   `json:"Name"`
	CreationDateTime *float64 `json:"CreationDateTime"`
}

// describeSecurityConfigurationInput mirrors the SDK DescribeSecurityConfigurationInput.
type describeSecurityConfigurationInput struct {
	Name *string `json:"Name"`
}

// describeSecurityConfigurationOutput mirrors the SDK DescribeSecurityConfigurationOutput.
type describeSecurityConfigurationOutput struct {
	Name                  string   `json:"Name"`
	SecurityConfiguration string   `json:"SecurityConfiguration"`
	CreationDateTime      *float64 `json:"CreationDateTime"`
}

// deleteSecurityConfigurationInput mirrors the SDK DeleteSecurityConfigurationInput.
type deleteSecurityConfigurationInput struct {
	Name *string `json:"Name"`
}

// listSecurityConfigurationsOutput mirrors the SDK ListSecurityConfigurationsOutput.
type listSecurityConfigurationsOutput struct {
	SecurityConfigurations []securityConfigurationSummaryWire `json:"SecurityConfigurations"`
}

// securityConfigurationSummaryWire mirrors the SDK SecurityConfigurationSummary.
type securityConfigurationSummaryWire struct {
	Name             string   `json:"Name"`
	CreationDateTime *float64 `json:"CreationDateTime"`
}

// --- store methods ---

// createSecurityConfig stores a new security configuration. A duplicate name or
// missing name/body is rejected with the InvalidRequestException EMR returns.
func (s *store) createSecurityConfig(name, config string) (time.Time, error) {
	if name == "" {
		return time.Time{}, cerrors.New(cerrors.InvalidArgument, "The security configuration name is required.")
	}

	if config == "" {
		return time.Time{}, cerrors.New(cerrors.InvalidArgument, "The security configuration is required.")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.secConfig[name]; ok {
		return time.Time{}, cerrors.Newf(cerrors.InvalidArgument,
			"Security configuration with name '%s' already exists.", name)
	}

	now := s.clock.Now().UTC()
	s.secConfig[name] = &securityConfig{name: name, config: config, created: now}
	s.secOrder = append(s.secOrder, name)

	return now, nil
}

// describeSecurityConfig returns a security configuration by name.
func (s *store) describeSecurityConfig(name string) (*securityConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sc, ok := s.secConfig[name]
	if !ok {
		return nil, secConfigNotFound(name)
	}

	return sc, nil
}

// deleteSecurityConfig removes a security configuration by name.
func (s *store) deleteSecurityConfig(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.secConfig[name]; !ok {
		return secConfigNotFound(name)
	}

	delete(s.secConfig, name)

	for i, n := range s.secOrder {
		if n == name {
			s.secOrder = append(s.secOrder[:i], s.secOrder[i+1:]...)

			break
		}
	}

	return nil
}

// listSecurityConfigs returns configuration summaries newest-first.
func (s *store) listSecurityConfigs() []securityConfigurationSummaryWire {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]securityConfigurationSummaryWire, 0, len(s.secOrder))

	for i := len(s.secOrder) - 1; i >= 0; i-- {
		sc := s.secConfig[s.secOrder[i]]
		out = append(out, securityConfigurationSummaryWire{Name: sc.name, CreationDateTime: epoch(sc.created)})
	}

	return out
}

// secConfigNotFound builds the InvalidRequestException EMR returns for an
// unknown security configuration. terraform-provider-aws matches the
// "does not exist" phrasing to treat a delete of a missing config as a no-op.
func secConfigNotFound(name string) error {
	return cerrors.Newf(cerrors.InvalidArgument,
		"Security configuration with name '%s' does not exist.", name)
}

// --- handlers ---

// createSecurityConfiguration handles CreateSecurityConfiguration.
func (h *Handler) createSecurityConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *createSecurityConfigurationInput) (any, error) {
		created, err := h.store.createSecurityConfig(deref(in.Name), deref(in.SecurityConfiguration))
		if err != nil {
			return nil, err
		}

		return createSecurityConfigurationOutput{Name: deref(in.Name), CreationDateTime: epoch(created)}, nil
	})
}

// describeSecurityConfiguration handles DescribeSecurityConfiguration.
func (h *Handler) describeSecurityConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *describeSecurityConfigurationInput) (any, error) {
		sc, err := h.store.describeSecurityConfig(deref(in.Name))
		if err != nil {
			return nil, err
		}

		return describeSecurityConfigurationOutput{
			Name:                  sc.name,
			SecurityConfiguration: sc.config,
			CreationDateTime:      epoch(sc.created),
		}, nil
	})
}

// deleteSecurityConfiguration handles DeleteSecurityConfiguration.
func (h *Handler) deleteSecurityConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *deleteSecurityConfigurationInput) (any, error) {
		if err := h.store.deleteSecurityConfig(deref(in.Name)); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

// listSecurityConfigurations handles ListSecurityConfigurations.
func (h *Handler) listSecurityConfigurations(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, _ *struct{}) (any, error) {
		return listSecurityConfigurationsOutput{SecurityConfigurations: h.store.listSecurityConfigs()}, nil
	})
}
