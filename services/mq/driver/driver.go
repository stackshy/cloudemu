// Package driver defines the interface and types for the Amazon MQ
// control-plane API (managed message broker for ActiveMQ and RabbitMQ). It
// models MQ brokers, their broker users, configurations (with immutable
// revisions), and resource tagging.
//
// The emulator is control-plane only: it does NOT run an ActiveMQ or RabbitMQ
// engine. A broker is created directly in the RUNNING state with stable
// computed fields (brokerId of the form b-<uuid>, brokerArn, created and the
// per-instance consoleUrl/endpoints/ipAddress) so IaC waiters that block on the
// broker state do not hang. The request body's descriptive fields
// (engineType, engineVersion, hostInstanceType, deploymentMode, the security
// groups and subnet ids, the logs block, the maintenance window, encryption
// options, storage type) are carried verbatim as map[string]json.RawMessage, so
// a round-tripped broker reflects exactly what the caller sent — a bool that
// must round-trip false and an int that must round-trip 0 both survive because
// the raw JSON is preserved. Configuration revision data (base64-encoded XML)
// round-trips verbatim per revision. Broker user passwords are write-only and
// are never echoed back on DescribeUser, mirroring the real API.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Page is the pagination cursor shared by the list operations.
type Page struct {
	NextToken  string
	MaxResults int32
}

// Broker states. A newly created broker is RUNNING so Terraform's create waiter
// (which blocks until the broker state is RUNNING) completes without the
// real-cloud provisioning wait.
const (
	StateCreationInProgress = "CREATION_IN_PROGRESS"
	StateRunning            = "RUNNING"
	StateRebootInProgress   = "REBOOT_IN_PROGRESS"
	StateDeletionInProgress = "DELETION_IN_PROGRESS"
	StateCriticalActionReq  = "CRITICAL_ACTION_REQUIRED"
)

// Engine types Amazon MQ supports.
const (
	EngineActiveMQ = "ACTIVEMQ"
	EngineRabbitMQ = "RABBITMQ"
)

// Instance is one allocated broker node: its web console URL, the wire-level
// protocol endpoints, and (ActiveMQ only) the ENI IP address. All three are
// computed once at create and stored, so repeated reads never drift.
type Instance struct {
	ConsoleURL string
	Endpoints  []string
	IPAddress  string
}

// ConfigRef references a configuration revision applied to a broker.
type ConfigRef struct {
	ID       string
	Revision int32
}

// User is a broker user. Password is write-only: it is stored so an update can
// preserve it but is never echoed on DescribeUser, mirroring the real API.
type User struct {
	Username        string
	Password        string
	ConsoleAccess   bool
	Groups          []string
	ReplicationUser bool
}

// Broker is an Amazon MQ broker. BrokerID, BrokerArn, BrokerState, Created and
// Instances are computed once at create and never regenerated, so repeated
// DescribeBroker/ListBrokers reads never drift. Config carries the request
// body's descriptive fields verbatim (BrokerName is kept in Config too); Tags,
// Users and Configuration are modeled separately because they are mutated by
// their own operations.
type Broker struct {
	BrokerID      string
	BrokerName    string
	BrokerArn     string
	BrokerState   string
	Created       time.Time
	Instances     []Instance
	Config        map[string]json.RawMessage
	Users         []User
	Tags          map[string]string
	Configuration *ConfigRef
}

// ConfigurationRevision is one immutable revision of a configuration. Data is
// the base64-encoded engine XML exactly as the caller supplied it.
type ConfigurationRevision struct {
	Revision    int32
	Created     time.Time
	Description string
	Data        string
}

// Configuration is an Amazon MQ configuration. Arn, ID and Created are computed
// once at create and stable across reads; each UpdateConfiguration appends an
// immutable revision, so LatestRevision() reports a monotonically increasing,
// stable revision number.
type Configuration struct {
	ID                     string
	Arn                    string
	Name                   string
	Description            string
	EngineType             string
	EngineVersion          string
	AuthenticationStrategy string
	Created                time.Time
	Revisions              []ConfigurationRevision
	Tags                   map[string]string
}

// LatestRevision returns the configuration's most recent revision, or nil when
// it somehow holds none.
func (c *Configuration) LatestRevision() *ConfigurationRevision {
	if len(c.Revisions) == 0 {
		return nil
	}

	return &c.Revisions[len(c.Revisions)-1]
}

// CreateBrokerInput is the input to CreateBroker. Config carries the descriptive
// fields the emulator round-trips verbatim; Tags, Users and Configuration are
// modeled separately.
type CreateBrokerInput struct {
	BrokerName    string
	EngineType    string
	Config        map[string]json.RawMessage
	Tags          map[string]string
	Users         []User
	Configuration *ConfigRef
}

// UpdateBrokerInput is the input to UpdateBroker. Config carries only the fields
// the PUT request supplied, so unmentioned fields survive; Configuration, when
// non-nil, re-points the broker at a configuration revision.
type UpdateBrokerInput struct {
	BrokerID      string
	Config        map[string]json.RawMessage
	Configuration *ConfigRef
}

// CreateConfigurationInput is the input to CreateConfiguration.
type CreateConfigurationInput struct {
	Name                   string
	EngineType             string
	EngineVersion          string
	AuthenticationStrategy string
	Tags                   map[string]string
}

// UpdateConfigurationInput is the input to UpdateConfiguration. Data is the
// base64-encoded engine XML; a new immutable revision is appended.
type UpdateConfigurationInput struct {
	ConfigurationID string
	Data            string
	Description     string
}

// MQ is the Amazon MQ control-plane surface: brokers, broker users,
// configurations (with revisions) and resource tags.
type MQ interface {
	CreateBroker(ctx context.Context, in *CreateBrokerInput) (*Broker, error)
	DescribeBroker(ctx context.Context, brokerID string) (*Broker, error)
	UpdateBroker(ctx context.Context, in *UpdateBrokerInput) (*Broker, error)
	DeleteBroker(ctx context.Context, brokerID string) error
	ListBrokers(ctx context.Context, page Page) (brokers []*Broker, nextToken string, err error)
	RebootBroker(ctx context.Context, brokerID string) error

	CreateConfiguration(ctx context.Context, in *CreateConfigurationInput) (*Configuration, error)
	DescribeConfiguration(ctx context.Context, configurationID string) (*Configuration, error)
	UpdateConfiguration(ctx context.Context, in *UpdateConfigurationInput) (*Configuration, error)
	ListConfigurations(ctx context.Context, page Page) (configs []*Configuration, nextToken string, err error)
	DescribeConfigurationRevision(ctx context.Context, configurationID string,
		revision int32) (*ConfigurationRevision, error)

	CreateUser(ctx context.Context, brokerID string, user *User) error
	DescribeUser(ctx context.Context, brokerID, username string) (*User, error)
	UpdateUser(ctx context.Context, brokerID string, user *User) error
	DeleteUser(ctx context.Context, brokerID, username string) error
	ListUsers(ctx context.Context, brokerID string, page Page) (users []User, nextToken string, err error)

	CreateTags(ctx context.Context, resourceArn string, tags map[string]string) error
	DeleteTags(ctx context.Context, resourceArn string, tagKeys []string) error
	ListTags(ctx context.Context, resourceArn string) (map[string]string, error)
}
