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

// deploymentModes are the deployment modes each Amazon MQ engine accepts.
//
//nolint:gochecknoglobals // enum
var deploymentModes = map[string][]string{
	driver.EngineActiveMQ: {"SINGLE_INSTANCE", "ACTIVE_STANDBY_MULTI_AZ"},
	driver.EngineRabbitMQ: {"SINGLE_INSTANCE", "CLUSTER_MULTI_AZ"},
}

// fieldAuthenticationStrategy is the request field naming how users sign in.
// SIMPLE, the default, is the only strategy that needs broker users.
const (
	fieldAuthenticationStrategy = "authenticationStrategy"
	authSimple                  = "SIMPLE"
)

// validateBrokerInput applies CreateBroker's request rules: a known engine and
// a deployment mode that engine runs, users for SIMPLE authentication (one and
// only one on RabbitMQ), and valid usernames and passwords.
func validateBrokerInput(engine string, in *driver.CreateBrokerInput) error {
	if engine == "" {
		return badRequest("engineType is required")
	}

	modes, ok := deploymentModes[engine]
	if !ok {
		return badRequest("engineType %s is not one of %s, %s", in.EngineType, driver.EngineActiveMQ, driver.EngineRabbitMQ)
	}

	if mode := rawString(in.Config, fieldDeploymentMode); mode != "" && !slices.Contains(modes, mode) {
		return badRequest("deploymentMode %s is not available for %s; want one of %s",
			mode, engine, strings.Join(modes, ", "))
	}

	if err := validateBrokerUsers(engine, rawString(in.Config, fieldAuthenticationStrategy), in.Users); err != nil {
		return err
	}

	for i := range in.Users {
		if err := validateUser(engine, &in.Users[i]); err != nil {
			return err
		}
	}

	return nil
}

// validateBrokerUsers checks the user count: SIMPLE authentication needs at
// least one user, and RabbitMQ accepts one and only one. Other strategies,
// such as OAuth 2.0 through CONFIG_MANAGED, may create a broker with none.
func validateBrokerUsers(engine, strategy string, users []driver.User) error {
	simple := strategy == "" || strings.EqualFold(strategy, authSimple)

	switch {
	case simple && len(users) == 0:
		return badRequest("users is required: a broker with SIMPLE authentication needs at least one user")
	case engine == driver.EngineRabbitMQ && len(users) > 1:
		return badRequest("a RabbitMQ broker accepts one and only one administrative user, got %d", len(users))
	}

	return nil
}

// Username length bounds Amazon MQ enforces.
const (
	minUsernameLen = 2
	maxUsernameLen = 100
)

// validateUser applies Amazon MQ's user rules: a username of 2 to 100
// characters from letters, digits and - . _ ~ (no ~ and not "guest" on
// RabbitMQ), and a valid password.
func validateUser(engine string, user *driver.User) error {
	name := user.Username
	if n := len(name); n < minUsernameLen || n > maxUsernameLen {
		return badRequest("username must be %d to %d characters long", minUsernameLen, maxUsernameLen)
	}

	allowed := "-._~"
	if engine == driver.EngineRabbitMQ {
		allowed = "-._"

		if name == "guest" {
			return badRequest("username guest is reserved on RabbitMQ")
		}
	}

	if strings.IndexFunc(name, func(r rune) bool { return !usernameRune(r, allowed) }) >= 0 {
		return badRequest("username %s may only contain letters, digits and %s", name, allowed)
	}

	return validatePassword(user.Password)
}

// usernameRune reports whether r is an ASCII letter or digit, or one of extra.
func usernameRune(r rune, extra string) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(extra, r)
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
