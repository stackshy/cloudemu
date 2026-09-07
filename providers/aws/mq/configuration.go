package mq

import (
	"context"
	"encoding/base64"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/mq/driver"
)

// authStrategySimple is the default authentication strategy for a configuration.
const authStrategySimple = "SIMPLE"

// Minimal engine-appropriate default configuration bodies. A newly created
// configuration starts at revision 1 with one of these; callers overwrite the
// data by appending a revision via UpdateConfiguration.
const (
	activeMQDefaultXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<broker xmlns="http://activemq.apache.org/schema/core"></broker>`
	rabbitMQDefaultXML = `# Default RabbitMQ configuration` + "\n"
)

// CreateConfiguration creates a new configuration at revision 1 with stable
// computed fields (id of the form c-<uuid>, arn, created). The initial revision
// holds an engine-appropriate default body; callers overwrite it via
// UpdateConfiguration.
func (m *Mock) CreateConfiguration(_ context.Context,
	in *driver.CreateConfigurationInput,
) (*driver.Configuration, error) {
	if in.Name == "" {
		return nil, badRequest("name is required")
	}

	engine := strings.ToUpper(in.EngineType)
	if engine == "" {
		return nil, badRequest("engineType is required")
	}

	auth := in.AuthenticationStrategy
	if auth == "" {
		auth = authStrategySimple
	}

	configID := "c-" + idgen.UUID()
	now := m.now()

	c := driver.Configuration{
		ID:                     configID,
		Arn:                    m.configurationARN(configID),
		Name:                   in.Name,
		EngineType:             in.EngineType,
		EngineVersion:          in.EngineVersion,
		AuthenticationStrategy: auth,
		Created:                now,
		Revisions: []driver.ConfigurationRevision{{
			Revision:    1,
			Created:     now,
			Description: "Auto-generated default for " + in.Name,
			Data:        defaultConfigData(engine),
		}},
		Tags: copyTags(in.Tags),
	}

	m.configs.Set(configID, c)

	out := copyConfiguration(&c)

	return &out, nil
}

// DescribeConfiguration returns a copy of the configuration with its stable
// computed fields and latest revision.
func (m *Mock) DescribeConfiguration(_ context.Context, configurationID string) (*driver.Configuration, error) {
	c, ok := m.configs.Get(configurationID)
	if !ok {
		return nil, notFound("Configuration %s not found", configurationID)
	}

	out := copyConfiguration(&c)

	return &out, nil
}

// UpdateConfiguration appends a new immutable revision carrying the supplied
// base64 data and description. The revision number increases monotonically; the
// configuration's id, arn and created are preserved.
func (m *Mock) UpdateConfiguration(_ context.Context,
	in *driver.UpdateConfigurationInput,
) (*driver.Configuration, error) {
	var updated driver.Configuration

	ok := m.configs.Update(in.ConfigurationID, func(c driver.Configuration) driver.Configuration {
		nextRev := len(c.Revisions) + 1
		revs := make([]driver.ConfigurationRevision, len(c.Revisions), nextRev)
		copy(revs, c.Revisions)

		revs = append(revs, driver.ConfigurationRevision{
			Revision:    int32(nextRev), //nolint:gosec // revision count is tiny; never overflows int32
			Created:     m.now(),
			Description: in.Description,
			Data:        in.Data,
		})
		c.Revisions = revs
		c.Description = in.Description
		updated = c

		return c
	})
	if !ok {
		return nil, notFound("Configuration %s not found", in.ConfigurationID)
	}

	out := copyConfiguration(&updated)

	return &out, nil
}

// ListConfigurations returns a deterministic page of configurations ordered by
// configuration id.
func (m *Mock) ListConfigurations(_ context.Context, page driver.Page) ([]*driver.Configuration, string, error) {
	stored := m.configs.SortedValues()
	start, end, next := paginate(len(stored), page)
	out := make([]*driver.Configuration, 0, end-start)

	for i := start; i < end; i++ {
		c := copyConfiguration(&stored[i])
		out = append(out, &c)
	}

	return out, next, nil
}

// DescribeConfigurationRevision returns the base64 data and metadata for a
// specific revision of a configuration.
func (m *Mock) DescribeConfigurationRevision(_ context.Context,
	configurationID string, revision int32,
) (*driver.ConfigurationRevision, error) {
	c, ok := m.configs.Get(configurationID)
	if !ok {
		return nil, notFound("Configuration %s not found", configurationID)
	}

	for i := range c.Revisions {
		if c.Revisions[i].Revision == revision {
			r := c.Revisions[i]

			return &r, nil
		}
	}

	return nil, notFound("Configuration %s revision %d not found", configurationID, revision)
}

// defaultConfigData returns the base64-encoded engine-appropriate default
// configuration body for a new configuration's revision 1.
func defaultConfigData(engine string) string {
	xml := activeMQDefaultXML
	if engine == driver.EngineRabbitMQ {
		xml = rabbitMQDefaultXML
	}

	return base64.StdEncoding.EncodeToString([]byte(xml))
}
