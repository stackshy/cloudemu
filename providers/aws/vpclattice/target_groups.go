package vpclattice

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

const targetGroupStatusActive = "ACTIVE"

func targetGroupNotFound(id string) error {
	return errors.Newf(errors.NotFound, "target group %q not found", id)
}

// cloneTargetGroup deep-copies a target group and fills the read-time fields:
// the status overlay and the ARNs of the services forwarding to it. Caller
// holds m.mu.
func (m *Mock) cloneTargetGroup(t *driver.TargetGroup) driver.TargetGroup {
	out := *t
	out.Config = append([]byte(nil), t.Config...)
	out.ServiceARNs = m.targetGroupServiceARNs(t.ID)
	out.Status = m.observe(t.ID, t.Status)

	return out
}

// tgConfigExtract pulls the summary-relevant fields out of a raw config blob.
type tgConfigExtract struct {
	Port                        int32  `json:"port"`
	Protocol                    string `json:"protocol"`
	VpcIdentifier               string `json:"vpcIdentifier"`
	IPAddressType               string `json:"ipAddressType"`
	LambdaEventStructureVersion string `json:"lambdaEventStructureVersion"`
}

func (m *Mock) CreateTargetGroup(_ context.Context, in *driver.CreateTargetGroupInput) (*driver.TargetGroup, error) {
	if err := validateTargetGroupType(in.Type); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var cfg tgConfigExtract
	if len(in.Config) > 0 {
		_ = json.Unmarshal(in.Config, &cfg)
	}

	id, err := idgen.GenerateLongID("tg-")
	if err != nil {
		return nil, err
	}

	tg := &driver.TargetGroup{
		ID:                          id,
		ARN:                         m.arn("targetgroup/" + id),
		Name:                        in.Name,
		Type:                        in.Type,
		Status:                      targetGroupStatusActive,
		Config:                      append([]byte(nil), in.Config...),
		Port:                        cfg.Port,
		Protocol:                    cfg.Protocol,
		VpcID:                       cfg.VpcIdentifier,
		IPAddressType:               cfg.IPAddressType,
		LambdaEventStructureVersion: cfg.LambdaEventStructureVersion,
		CreatedAt:                   m.now(),
		LastUpdatedAt:               m.now(),
	}
	m.targetGroups.Set(id, tg)
	m.targets.Set(id, nil)
	m.writeTags(tg.ARN, in.Tags)
	m.beginSettle(id)

	out := m.cloneTargetGroup(tg)

	return &out, nil
}

func (m *Mock) GetTargetGroup(_ context.Context, identifier string) (*driver.TargetGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	tg, ok := m.targetGroups.Get(id)
	if !ok {
		return nil, targetGroupNotFound(id)
	}

	out := m.cloneTargetGroup(tg)

	return &out, nil
}

func (m *Mock) UpdateTargetGroup(
	_ context.Context, identifier string, healthCheck []byte,
) (*driver.TargetGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	tg, ok := m.targetGroups.Get(id)
	if !ok {
		return nil, targetGroupNotFound(id)
	}

	if len(healthCheck) > 0 {
		tg.Config = mergeHealthCheck(tg.Config, healthCheck)
	}

	tg.LastUpdatedAt = m.now()

	out := m.cloneTargetGroup(tg)

	return &out, nil
}

// mergeHealthCheck sets the healthCheck member on a raw config blob.
func mergeHealthCheck(config, healthCheck []byte) []byte {
	obj := map[string]json.RawMessage{}
	if len(config) > 0 {
		_ = json.Unmarshal(config, &obj)
	}

	obj["healthCheck"] = json.RawMessage(healthCheck)

	merged, err := json.Marshal(obj)
	if err != nil {
		return config
	}

	return merged
}

func (m *Mock) DeleteTargetGroup(_ context.Context, identifier string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idFromIdentifier(identifier)

	if !m.targetGroups.Has(id) {
		return targetGroupNotFound(id)
	}

	// A target group a listener or rule still forwards to cannot be deleted.
	if m.targetGroupInUse(id) {
		return inUsef("target group %q is in use by a listener or rule", id)
	}

	cur, _ := m.targets.Get(id)
	for i := range cur {
		m.statusSettle.Clear(targetSettleKey(id, cur[i]))
	}

	m.statusSettle.Clear(id)
	m.targetGroups.Delete(id)
	m.targets.Delete(id)

	return nil
}

func (m *Mock) ListTargetGroups(_ context.Context) ([]driver.TargetGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.targetGroups.All(), m.cloneTargetGroup), nil
}
