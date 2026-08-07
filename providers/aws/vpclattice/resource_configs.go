package vpclattice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

const resourceStatusActive = "ACTIVE"

func resourceConfigNotFound(id string) error {
	return errors.Newf(errors.NotFound, "resource configuration %q not found", id)
}

func cloneResourceConfig(c *driver.ResourceConfiguration) driver.ResourceConfiguration {
	out := *c
	out.PortRanges = append([]string(nil), c.PortRanges...)
	out.Definition = append([]byte(nil), c.Definition...)

	return out
}

func (m *Mock) CreateResourceConfiguration(
	_ context.Context, in *driver.CreateResourceConfigurationInput,
) (*driver.ResourceConfiguration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idgen.GenerateID("rcfg-")
	c := &driver.ResourceConfiguration{
		ID:                       id,
		ARN:                      m.arn("resourceconfiguration/" + id),
		Name:                     in.Name,
		Type:                     in.Type,
		Status:                   resourceStatusActive,
		Protocol:                 in.Protocol,
		CustomDomainName:         in.CustomDomainName,
		GroupDomain:              in.GroupDomain,
		PortRanges:               append([]string(nil), in.PortRanges...),
		Definition:               append([]byte(nil), in.Definition...),
		ResourceGatewayID:        idFromIdentifier(in.ResourceGatewayID),
		ResourceConfigGroupID:    idFromIdentifier(in.ResourceConfigGroupID),
		AllowAssociationToShared: in.AllowAssociationToShared,
		CreatedAt:                m.now(),
		LastUpdatedAt:            m.now(),
	}
	m.resourceConfigs.Set(id, c)
	m.writeTags(c.ARN, in.Tags)

	out := cloneResourceConfig(c)

	return &out, nil
}

func (m *Mock) GetResourceConfiguration(_ context.Context, identifier string) (*driver.ResourceConfiguration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	c, ok := m.resourceConfigs.Get(id)
	if !ok {
		return nil, resourceConfigNotFound(id)
	}

	out := cloneResourceConfig(c)

	return &out, nil
}

func (m *Mock) UpdateResourceConfiguration(
	_ context.Context, in *driver.UpdateResourceConfigurationInput,
) (*driver.ResourceConfiguration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(in.ID)

	c, ok := m.resourceConfigs.Get(id)
	if !ok {
		return nil, resourceConfigNotFound(id)
	}

	if in.PortRanges != nil {
		c.PortRanges = append([]string(nil), in.PortRanges...)
	}

	if len(in.Definition) > 0 {
		c.Definition = append([]byte(nil), in.Definition...)
	}

	if in.AllowAssociationToShared != nil {
		c.AllowAssociationToShared = *in.AllowAssociationToShared
	}

	c.LastUpdatedAt = m.now()

	out := cloneResourceConfig(c)

	return &out, nil
}

func (m *Mock) DeleteResourceConfiguration(_ context.Context, identifier string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	if !m.resourceConfigs.Has(id) {
		return resourceConfigNotFound(id)
	}

	m.resourceConfigs.Delete(id)

	return nil
}

func (m *Mock) ListResourceConfigurations(_ context.Context) ([]driver.ResourceConfiguration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.resourceConfigs.All(), cloneResourceConfig), nil
}
