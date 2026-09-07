// Package mq provides an in-memory mock implementation of the Amazon MQ control
// plane (managed message broker for ActiveMQ and RabbitMQ): brokers, their
// broker users, configurations with immutable revisions, and resource tagging.
//
// The mock is control-plane only — it does NOT run an ActiveMQ or RabbitMQ
// engine. A broker is created immediately in the RUNNING state with a stable
// brokerId (b-<uuid>), brokerArn, created timestamp and per-instance
// consoleUrl/endpoints/ipAddress, so a round-tripped broker reflects exactly
// what the caller sent while IaC waiters that block on the broker state do not
// hang. Configuration revision data (base64 XML) round-trips verbatim. Broker
// user passwords are write-only and never echoed on DescribeUser.
package mq

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/mq/driver"
)

// Compile-time check that Mock implements driver.MQ.
var _ driver.MQ = (*Mock)(nil)

// defaultMaxResults caps a page when the caller requests none.
const defaultMaxResults = 100

// ARN resource kinds and markers.
const (
	kindBroker        = "broker"
	kindConfiguration = "configuration"
	arnMarker         = ":mq:"
)

// Mock is an in-memory implementation of the Amazon MQ control plane.
type Mock struct {
	brokers *memstore.Store[driver.Broker]
	configs *memstore.Store[driver.Configuration]
	opts    *config.Options
}

// New creates a new Amazon MQ mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		brokers: memstore.New[driver.Broker](),
		configs: memstore.New[driver.Configuration](),
		opts:    opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// brokerARN mints the stable ARN reported for a broker.
func (m *Mock) brokerARN(brokerID string) string {
	return idgen.AWSARN("mq", m.opts.Region, m.opts.AccountID, kindBroker+":"+brokerID)
}

// configurationARN mints the stable ARN reported for a configuration.
func (m *Mock) configurationARN(configID string) string {
	return idgen.AWSARN("mq", m.opts.Region, m.opts.AccountID, kindConfiguration+":"+configID)
}

// resourceRefFromARN extracts the (kind, id) pair from an MQ resource ARN of
// the form arn:aws:mq:{region}:{acct}:{kind}:{id}.
func resourceRefFromARN(arn string) (kind, id string) {
	if !strings.Contains(arn, arnMarker) {
		return "", ""
	}

	idx := strings.LastIndex(arn, ":")
	if idx < 0 {
		return "", ""
	}

	rest := arn[:idx]

	kidx := strings.LastIndex(rest, ":")
	if kidx < 0 {
		return "", ""
	}

	return rest[kidx+1:], arn[idx+1:]
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyConfig(in map[string]json.RawMessage) map[string]json.RawMessage {
	if in == nil {
		return nil
	}

	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = append(json.RawMessage(nil), v...)
	}

	return out
}

func copyInstances(in []driver.Instance) []driver.Instance {
	if in == nil {
		return nil
	}

	out := make([]driver.Instance, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Endpoints = append([]string(nil), in[i].Endpoints...)
	}

	return out
}

func copyUsers(in []driver.User) []driver.User {
	if in == nil {
		return nil
	}

	out := make([]driver.User, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Groups = append([]string(nil), in[i].Groups...)
	}

	return out
}

// copyBroker returns an alias-free copy of a broker so callers cannot mutate
// stored state through the result.
func copyBroker(b *driver.Broker) driver.Broker {
	out := *b
	out.Config = copyConfig(b.Config)
	out.Tags = copyTags(b.Tags)
	out.Users = copyUsers(b.Users)
	out.Instances = copyInstances(b.Instances)

	if b.Configuration != nil {
		ref := *b.Configuration
		out.Configuration = &ref
	}

	return out
}

// copyConfiguration returns an alias-free copy of a configuration.
func copyConfiguration(c *driver.Configuration) driver.Configuration {
	out := *c
	out.Tags = copyTags(c.Tags)
	out.Revisions = append([]driver.ConfigurationRevision(nil), c.Revisions...)

	return out
}

// paginate returns the offset window and next token for a slice of length n,
// honoring an opaque numeric offset token.
func paginate(n int, page driver.Page) (start, end int, next string) {
	start = decodeToken(page.NextToken)
	if start > n {
		start = n
	}

	limit := int(page.MaxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	end = start + limit
	if end >= n {
		return start, n, ""
	}

	return start, end, encodeToken(end)
}

func encodeToken(offset int) string {
	return strconv.Itoa(offset)
}

func decodeToken(token string) int {
	if token == "" {
		return 0
	}

	n, err := strconv.Atoi(token)
	if err != nil || n < 0 {
		return 0
	}

	return n
}
