package mq

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/mq/driver"
)

// Request-body fields the emulator handles specially and therefore strips from
// the verbatim broker configuration passthrough. Tags, Users and Configuration
// are modeled separately; CreatorRequestId is an idempotency token, not broker
// state; BrokerId is the URI label, not body state.
const (
	fieldTags             = "tags"
	fieldUsers            = "users"
	fieldConfiguration    = "configuration"
	fieldCreatorRequestID = "creatorRequestId"
	fieldBrokerID         = "brokerId"
	fieldDeploymentMode   = "deploymentMode"
)

// CreateBroker provisions a new broker directly in the RUNNING state with stable
// computed fields (brokerId, brokerArn, created and the per-instance
// consoleUrl/endpoints/ipAddress). The descriptive request fields are carried
// verbatim so a DescribeBroker reflects exactly what the caller sent. A broker
// name already in use yields a ConflictException.
func (m *Mock) CreateBroker(_ context.Context, in *driver.CreateBrokerInput) (*driver.Broker, error) {
	if in.BrokerName == "" {
		return nil, badRequest("brokerName is required")
	}

	engine := strings.ToUpper(in.EngineType)
	if err := validateBrokerInput(engine, in); err != nil {
		return nil, err
	}

	if m.brokerNameExists(in.BrokerName) {
		return nil, conflict("Broker name %s already exists in this account", in.BrokerName)
	}

	cfg := copyConfig(in.Config)
	if cfg == nil {
		cfg = map[string]json.RawMessage{}
	}

	delete(cfg, fieldTags)
	delete(cfg, fieldUsers)
	delete(cfg, fieldConfiguration)
	delete(cfg, fieldCreatorRequestID)

	brokerID := "b-" + idgen.UUID()
	now := m.now()

	b := driver.Broker{
		BrokerID:      brokerID,
		BrokerName:    in.BrokerName,
		BrokerArn:     m.brokerARN(brokerID),
		BrokerState:   driver.StateRunning,
		Created:       now,
		Instances:     m.computeInstances(brokerID, engine, rawString(in.Config, fieldDeploymentMode)),
		Config:        cfg,
		Users:         copyUsers(in.Users),
		Tags:          copyTags(in.Tags),
		Configuration: in.Configuration,
	}

	m.brokers.Set(brokerID, b)

	out := copyBroker(&b)

	return &out, nil
}

// DescribeBroker returns a copy of the broker. The stored computed fields are
// returned unchanged so repeated reads never drift.
func (m *Mock) DescribeBroker(_ context.Context, brokerID string) (*driver.Broker, error) {
	b, ok := m.brokers.Get(brokerID)
	if !ok {
		return nil, notFound("Broker %s not found", brokerID)
	}

	out := copyBroker(&b)

	return &out, nil
}

// UpdateBroker merges the fields the request supplied, leaving unmentioned
// fields untouched. The computed brokerId, brokerArn, brokerState, created and
// instances are preserved.
func (m *Mock) UpdateBroker(_ context.Context, in *driver.UpdateBrokerInput) (*driver.Broker, error) {
	var updated driver.Broker

	ok := m.brokers.Update(in.BrokerID, func(b driver.Broker) driver.Broker {
		b.Config = copyConfig(b.Config)
		if b.Config == nil {
			b.Config = map[string]json.RawMessage{}
		}

		for k, v := range in.Config {
			switch k {
			case fieldTags, fieldUsers, fieldConfiguration, fieldBrokerID:
				continue
			default:
				b.Config[k] = append(json.RawMessage(nil), v...)
			}
		}

		if in.Configuration != nil {
			b.Configuration = in.Configuration
		}

		updated = b

		return b
	})
	if !ok {
		return nil, notFound("Broker %s not found", in.BrokerID)
	}

	out := copyBroker(&updated)

	return &out, nil
}

// DeleteBroker removes a broker.
func (m *Mock) DeleteBroker(_ context.Context, brokerID string) error {
	if !m.brokers.Delete(brokerID) {
		return notFound("Broker %s not found", brokerID)
	}

	return nil
}

// ListBrokers returns a deterministic page of brokers ordered by broker id.
func (m *Mock) ListBrokers(_ context.Context, page driver.Page) ([]*driver.Broker, string, error) {
	stored := m.brokers.SortedValues()
	start, end, next := paginate(len(stored), page)
	out := make([]*driver.Broker, 0, end-start)

	for i := start; i < end; i++ {
		b := copyBroker(&stored[i])
		out = append(out, &b)
	}

	return out, next, nil
}

// RebootBroker is a no-op reboot: the emulator keeps the broker in the RUNNING
// state so IaC waiters do not hang.
func (m *Mock) RebootBroker(_ context.Context, brokerID string) error {
	if !m.brokers.Has(brokerID) {
		return notFound("Broker %s not found", brokerID)
	}

	return nil
}

// brokerNameExists reports whether a broker with the given name already exists.
func (m *Mock) brokerNameExists(name string) bool {
	stored := m.brokers.SortedValues()
	for i := range stored {
		if stored[i].BrokerName == name {
			return true
		}
	}

	return false
}

// rawString decodes a JSON string field from a raw request body, returning ""
// when the field is absent or not a string.
func rawString(raw map[string]json.RawMessage, key string) string {
	v, ok := raw[key]
	if !ok {
		return ""
	}

	var s string
	if json.Unmarshal(v, &s) != nil {
		return ""
	}

	return s
}

// Engine types and deployment modes Amazon MQ accepts.
var (
	engineTypes     = []string{"ACTIVEMQ", "RABBITMQ"}                                           //nolint:gochecknoglobals // enum
	deploymentModes = []string{"SINGLE_INSTANCE", "ACTIVE_STANDBY_MULTI_AZ", "CLUSTER_MULTI_AZ"} //nolint:gochecknoglobals // enum
)

// validateBrokerInput applies CreateBroker's request rules: a known engine
// type and deployment mode, and at least one user with a valid password.
func validateBrokerInput(engine string, in *driver.CreateBrokerInput) error {
	if engine == "" {
		return badRequest("engineType is required")
	}

	if !slices.Contains(engineTypes, engine) {
		return badRequest("engineType %s is not one of %s", in.EngineType, strings.Join(engineTypes, ", "))
	}

	if mode := rawString(in.Config, fieldDeploymentMode); mode != "" && !slices.Contains(deploymentModes, mode) {
		return badRequest("deploymentMode %s is not one of %s", mode, strings.Join(deploymentModes, ", "))
	}

	if len(in.Users) == 0 {
		return badRequest("users is required: a broker needs at least one user")
	}

	for i := range in.Users {
		if err := validatePassword(in.Users[i].Password); err != nil {
			return err
		}
	}

	return nil
}

// Password rules Amazon MQ applies to broker users.
const (
	minPasswordLen    = 12
	minPasswordUnique = 4
)

// validatePassword applies Amazon MQ's broker user password rules: at least 12
// characters, at least 4 of them unique, and none of ',' ':' '='.
func validatePassword(password string) error {
	if len([]rune(password)) < minPasswordLen {
		return badRequest("password must be at least %d characters long", minPasswordLen)
	}

	if strings.ContainsAny(password, ",:=") {
		return badRequest("password must not contain commas, colons, or equal signs")
	}

	unique := map[rune]bool{}
	for _, r := range password {
		unique[r] = true
	}

	if len(unique) < minPasswordUnique {
		return badRequest("password must contain at least %d unique characters", minPasswordUnique)
	}

	return nil
}
