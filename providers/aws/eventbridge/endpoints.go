package eventbridge

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

const (
	endpointStateActive = "ACTIVE"

	replicationEnabled  = "ENABLED"
	replicationDisabled = "DISABLED"

	maxEndpointNameLen   = 64
	maxEndpoints         = 100
	maxEndpointPageSize  = 100
	endpointBusCount     = 2
	maxHealthCheckLen    = 1600
	maxEndpointBusARNLen = 512
	maxRoleARNLen        = 256
	minRegionLen         = 9
	maxRegionLen         = 20
	maxEndpointIDLen     = 50

	// endpointIDHostLen and endpointIDZoneLen size the two labels of a
	// generated EndpointId, as in "abcdefghij.xyz".
	endpointIDHostLen = 10
	endpointIDZoneLen = 3
	base36            = 36

	// endpointURLSuffix is the host suffix the AWS SDKs resolve a global
	// endpoint to: https://<EndpointId>.endpoint.events.amazonaws.com.
	endpointURLSuffix = ".endpoint.events.amazonaws.com"

	msgEndpointLimit = "The requested resource exceeds the maximum number allowed."
	msgBadNextPage   = "The NextToken provided is not valid."
)

// Patterns of the global endpoint members, as the EventBridge API model spells them.
const (
	healthCheckPattern = `^arn:aws([a-z]|\-)*:route53:::healthcheck/[\-a-z0-9]+$`
	regionPattern      = `^[\-a-z0-9]+$`
	endpointBusPattern = `^arn:aws[a-z-]*:events:[a-z]+-[a-z-]+-\d+:\d{12}:event-bus/[\w.-]+$`
	roleARNPattern     = `^arn:aws[a-z-]*:iam::\d{12}:role\/[\w+=,.@/-]+$`
	endpointIDPattern  = `^[A-Za-z0-9\-]+[\.][A-Za-z0-9\-]+$`
)

var (
	healthCheckRE = regexp.MustCompile(healthCheckPattern)
	regionRE      = regexp.MustCompile(regionPattern)
	endpointBusRE = regexp.MustCompile(endpointBusPattern)
	// roleARNRE and endpointIDRE are roleARNPattern and endpointIDPattern
	// without their redundant escapes.
	roleARNRE    = regexp.MustCompile(`^arn:aws[a-z-]*:iam::\d{12}:role/[\w+=,.@/-]+$`)
	endpointIDRE = regexp.MustCompile(`^[A-Za-z0-9\-]+\.[A-Za-z0-9\-]+$`)
)

func errEndpointNotExist(name string) error {
	return errors.Newf(errors.NotFound, "Endpoint %s does not exist.", name)
}

func errInvalidParam(param, reason string) error {
	return errors.Newf(errors.InvalidArgument, "Parameter %s is not valid. Reason: %s", param, reason)
}

// endpointIDFor derives a stable EndpointId from the endpoint's account, Region
// and name: ten and three lowercase alphanumerics joined by a dot.
func endpointIDFor(account, region, name string) string {
	sum := sha256.Sum256([]byte(account + "/" + region + "/" + name))
	s := new(big.Int).SetBytes(sum[:]).Text(base36)

	if n := endpointIDHostLen + endpointIDZoneLen; len(s) < n {
		s = strings.Repeat("0", n-len(s)) + s
	}

	return s[:endpointIDHostLen] + "." + s[endpointIDHostLen:endpointIDHostLen+endpointIDZoneLen]
}

// regionOfBusARN returns the Region field of an event bus ARN.
func regionOfBusARN(arn string) string {
	const regionField = 3

	parts := strings.Split(arn, ":")
	if len(parts) <= regionField {
		return ""
	}

	return parts[regionField]
}

// endpointViolations checks every member of an endpoint configuration against
// the API model's required flags, lengths, patterns and enums.
func endpointViolations(cfg *driver.EndpointConfig) violations {
	var v violations

	v.name(cfg.Name, "name", maxEndpointNameLen)
	v.maxLen(cfg.Description, "description", maxDescriptionLen)

	if v.present(cfg.Routing.HealthCheck, "routingConfig.failoverConfig.primary.healthCheck") {
		v.lengthBetween(cfg.Routing.HealthCheck, "routingConfig.failoverConfig.primary.healthCheck", 1, maxHealthCheckLen)
		v.pattern(cfg.Routing.HealthCheck, "routingConfig.failoverConfig.primary.healthCheck", healthCheckRE, healthCheckPattern)
	}

	if v.present(cfg.Routing.SecondaryRoute, "routingConfig.failoverConfig.secondary.route") {
		v.region(cfg.Routing.SecondaryRoute, "routingConfig.failoverConfig.secondary.route")
	}

	if cfg.ReplicationState != "" {
		v.enum(cfg.ReplicationState, "replicationConfig.state", []string{replicationEnabled, replicationDisabled})
	}

	v.endpointBuses(cfg.EventBusARNs)

	if cfg.RoleARN != "" {
		v.lengthBetween(cfg.RoleARN, "roleArn", 1, maxRoleARNLen)
		v.pattern(cfg.RoleARN, "roleArn", roleARNRE, roleARNPattern)
	}

	return v
}

func (v *violations) region(value, field string) {
	v.lengthBetween(value, field, minRegionLen, maxRegionLen)
	v.pattern(value, field, regionRE, regionPattern)
}

func (v *violations) endpointBuses(arns []string) {
	if len(arns) != endpointBusCount {
		v.add("["+strings.Join(arns, ", ")+"]", "eventBuses",
			fmt.Sprintf("Member must have length less than or equal to %d and greater than or equal to %d",
				endpointBusCount, endpointBusCount))
	}

	for i, arn := range arns {
		field := fmt.Sprintf("eventBuses.%d.member.eventBusArn", i+1)
		if v.present(arn, field) {
			v.lengthBetween(arn, field, 1, maxEndpointBusARNLen)
			v.pattern(arn, field, endpointBusRE, endpointBusPattern)
		}
	}
}

// endpointRuleError checks the rules that tie the members together: the two
// event buses share a name, sit in different Regions, one of them in the home
// Region and the other in the secondary Region, and replication needs a role.
func endpointRuleError(cfg *driver.EndpointConfig, home string) error {
	first, second := cfg.EventBusARNs[0], cfg.EventBusARNs[1]

	switch r1, r2 := regionOfBusARN(first), regionOfBusARN(second); {
	case busNameFromARN(first) != busNameFromARN(second):
		return errInvalidParam("EventBuses", "The event bus names must be identical in each Region.")
	case r1 == r2:
		return errInvalidParam("EventBuses", "The event buses must be in different Regions.")
	case r1 != home && r2 != home:
		return errInvalidParam("EventBuses", "One event bus must be in the endpoint's Region "+home+".")
	case cfg.Routing.SecondaryRoute == home || (cfg.Routing.SecondaryRoute != r1 && cfg.Routing.SecondaryRoute != r2):
		return errInvalidParam("RoutingConfig.FailoverConfig.Secondary.Route",
			"The secondary Region must be the Region of the other event bus.")
	}

	return nil
}

func (m *Mock) validateEndpoint(cfg *driver.EndpointConfig) error {
	if err := endpointViolations(cfg).err(); err != nil {
		return err
	}

	if err := endpointRuleError(cfg, m.opts.Region); err != nil {
		return err
	}

	if cfg.ReplicationState != replicationDisabled && cfg.RoleARN == "" {
		return errInvalidParam("RoleArn", "RoleArn is required when event replication is ENABLED.")
	}

	return nil
}

// cloneEndpoint copies e so callers never share its event bus slice.
func cloneEndpoint(e *driver.Endpoint) *driver.Endpoint {
	out := *e
	out.EventBusARNs = append([]string(nil), e.EventBusARNs...)

	return &out
}

// CreateEndpoint creates a global endpoint homed in the emulator's Region. It
// answers CREATING, as EventBridge does. Reads then show ACTIVE, after a short
// CREATING window when async settling is on.
func (m *Mock) CreateEndpoint(_ context.Context, cfg *driver.EndpointConfig) (*driver.Endpoint, error) {
	if err := m.validateEndpoint(cfg); err != nil {
		return nil, err
	}

	if m.endpoints.Len() >= maxEndpoints {
		return nil, errors.New(errors.ResourceExhausted, msgEndpointLimit)
	}

	now := m.opts.Clock.Now().UTC()
	id := endpointIDFor(m.opts.AccountID, m.opts.Region, cfg.Name)

	replication := cfg.ReplicationState
	if replication == "" {
		replication = replicationEnabled
	}

	ep := driver.Endpoint{
		Name:             cfg.Name,
		Description:      cfg.Description,
		ARN:              idgen.AWSARN("events", m.opts.Region, m.opts.AccountID, "endpoint/"+cfg.Name),
		Routing:          cfg.Routing,
		ReplicationState: replication,
		EventBusARNs:     append([]string(nil), cfg.EventBusARNs...),
		RoleARN:          cfg.RoleARN,
		EndpointID:       id,
		EndpointURL:      "https://" + id + endpointURLSuffix,
		HomeRegion:       m.opts.Region,
		State:            endpointStateActive,
		CreatedAt:        now,
		LastModifiedAt:   now,
	}

	if !m.endpoints.SetIfAbsent(cfg.Name, ep) {
		return nil, errors.Newf(errors.AlreadyExists, "Endpoint %s already exists.", cfg.Name)
	}

	m.endpointSettle.Begin(cfg.Name, stateCreating, now, m.opts.SettleDuration(settle.DefaultEventBridgeEndpointSettle))

	out := cloneEndpoint(&ep)
	out.State = stateCreating

	return out, nil
}

// observedEndpoint looks up an endpoint and overlays its settle window: with
// async settling on, a fresh create or update reads as CREATING or UPDATING
// for a moment before ACTIVE; otherwise it is ACTIVE at once.
func (m *Mock) observedEndpoint(name string) (driver.Endpoint, bool) {
	ep, ok := m.endpoints.Get(name)
	if !ok {
		return driver.Endpoint{}, false
	}

	ep.EventBusARNs = append([]string(nil), ep.EventBusARNs...)
	ep.State = m.endpointSettle.State(name, m.opts.Clock.Now(), ep.State)

	return ep, true
}

func validateHomeRegion(home string) error {
	if home == "" {
		return nil
	}

	var v violations

	v.region(home, "homeRegion")

	return v.err()
}

// DescribeEndpoint returns a global endpoint. A non-empty homeRegion must be
// the endpoint's home Region, or the endpoint is not found.
func (m *Mock) DescribeEndpoint(_ context.Context, name, homeRegion string) (*driver.Endpoint, error) {
	var v violations

	v.name(name, "name", maxEndpointNameLen)

	if err := v.err(); err != nil {
		return nil, err
	}

	if err := validateHomeRegion(homeRegion); err != nil {
		return nil, err
	}

	ep, ok := m.observedEndpoint(name)
	if !ok || (homeRegion != "" && homeRegion != ep.HomeRegion) {
		return nil, errEndpointNotExist(name)
	}

	return &ep, nil
}

// mergedEndpointConfig is the endpoint configuration e would have after upd.
func mergedEndpointConfig(e *driver.Endpoint, upd *driver.EndpointUpdate) driver.EndpointConfig {
	cfg := driver.EndpointConfig{
		Name: e.Name, Description: e.Description, Routing: e.Routing,
		ReplicationState: e.ReplicationState, EventBusARNs: e.EventBusARNs, RoleARN: e.RoleARN,
	}

	if upd.Description != nil {
		cfg.Description = *upd.Description
	}

	if upd.Routing != nil {
		cfg.Routing = *upd.Routing
	}

	if upd.ReplicationState != nil {
		cfg.ReplicationState = *upd.ReplicationState
	}

	if upd.EventBusARNs != nil {
		cfg.EventBusARNs = append([]string(nil), upd.EventBusARNs...)
	}

	if upd.RoleARN != nil {
		cfg.RoleARN = *upd.RoleARN
	}

	return cfg
}

// UpdateEndpoint changes a global endpoint's description, routing, replication,
// event buses or role. Omitted fields keep their values. It answers UPDATING;
// reads then show ACTIVE, after a short UPDATING window when async settling is
// on.
func (m *Mock) UpdateEndpoint(_ context.Context, upd *driver.EndpointUpdate) (*driver.Endpoint, error) {
	var v violations

	v.name(upd.Name, "name", maxEndpointNameLen)

	if err := v.err(); err != nil {
		return nil, err
	}

	var (
		out      driver.Endpoint
		applyErr error
	)

	now := m.opts.Clock.Now().UTC()

	found := m.endpoints.Update(upd.Name, func(e driver.Endpoint) driver.Endpoint {
		cfg := mergedEndpointConfig(&e, upd)
		if applyErr = m.validateEndpoint(&cfg); applyErr != nil {
			return e
		}

		e.Description, e.Routing, e.RoleARN = cfg.Description, cfg.Routing, cfg.RoleARN
		e.ReplicationState, e.EventBusARNs = cfg.ReplicationState, cfg.EventBusARNs
		e.StateReason, e.LastModifiedAt = "", now
		out = e

		return e
	})

	if !found {
		return nil, errEndpointNotExist(upd.Name)
	}

	if applyErr != nil {
		return nil, applyErr
	}

	m.endpointSettle.Begin(upd.Name, stateUpdating, now, m.opts.SettleDuration(settle.DefaultEventBridgeEndpointSettle))

	res := cloneEndpoint(&out)
	res.State = stateUpdating

	return res, nil
}

// DeleteEndpoint deletes a global endpoint. Real EventBridge passes through
// DELETING first; here the endpoint is gone as soon as the call returns.
func (m *Mock) DeleteEndpoint(_ context.Context, name string) error {
	var v violations

	v.name(name, "name", maxEndpointNameLen)

	if err := v.err(); err != nil {
		return err
	}

	if !m.endpoints.Delete(name) {
		return errEndpointNotExist(name)
	}

	m.endpointSettle.Clear(name)

	return nil
}

func validateEndpointFilter(f *driver.EndpointFilter) error {
	var v violations

	if f.NamePrefix != "" {
		v.name(f.NamePrefix, "namePrefix", maxEndpointNameLen)
	}

	if f.HomeRegion != "" {
		v.region(f.HomeRegion, "homeRegion")
	}

	if f.MaxResults < 0 || f.MaxResults > maxEndpointPageSize {
		v.add(fmt.Sprint(f.MaxResults), "maxResults",
			fmt.Sprintf("Member must have value less than or equal to %d and greater than or equal to 1", maxEndpointPageSize))
	}

	return v.err()
}

// ListEndpoints lists global endpoints sorted by name, narrowed by name prefix
// and home Region, one page at a time. It returns the page and the token of
// the next one, or "" on the last page.
func (m *Mock) ListEndpoints(_ context.Context, f driver.EndpointFilter) ([]driver.Endpoint, string, error) {
	if err := validateEndpointFilter(&f); err != nil {
		return nil, "", err
	}

	names := m.endpoints.Keys()
	matched := make([]driver.Endpoint, 0, len(names))

	for _, name := range names {
		ep, ok := m.observedEndpoint(name)
		if !ok || !strings.HasPrefix(name, f.NamePrefix) || (f.HomeRegion != "" && ep.HomeRegion != f.HomeRegion) {
			continue
		}

		matched = append(matched, ep)
	}

	page, err := pagination.PaginateSorted(matched, func(a, b driver.Endpoint) bool { return a.Name < b.Name },
		f.NextToken, f.MaxResults)
	if err != nil {
		return nil, "", errors.New(errors.InvalidArgument, msgBadNextPage)
	}

	return page.Items, page.NextPageToken, nil
}

// endpointByID finds the global endpoint whose EndpointId is id.
func (m *Mock) endpointByID(id string) (driver.Endpoint, bool) {
	all := m.endpoints.All()

	for name := range all {
		if all[name].EndpointID == id {
			return all[name], true
		}
	}

	return driver.Endpoint{}, false
}

// PutEndpointEventEntries publishes a PutEvents batch sent through the global
// endpoint endpointID. The emulator runs a single Region, so the endpoint always
// routes to its primary event bus here: an entry with no EventBusName, or one
// naming either of the endpoint's buses by name or ARN, goes to the bus of that
// name in this Region. Failover and replication to the secondary Region are not
// modeled. An entry naming some other bus is published to it as usual.
func (m *Mock) PutEndpointEventEntries(
	ctx context.Context, endpointID string, events []driver.Event,
) ([]driver.PutEventsEntryResult, error) {
	var v violations

	v.lengthBetween(endpointID, "endpointId", 1, maxEndpointIDLen)
	v.pattern(endpointID, "endpointId", endpointIDRE, endpointIDPattern)

	if err := v.err(); err != nil {
		return nil, err
	}

	ep, ok := m.endpointByID(endpointID)
	if !ok {
		return nil, errInvalidParam("EndpointId", "Endpoint "+endpointID+" does not exist.")
	}

	primary := busNameFromARN(ep.EventBusARNs[0])

	for i := range events {
		name := events[i].EventBus
		if n := busNameFromARN(name); n != "" {
			name = n
		}

		if name == "" {
			name = primary
		}

		events[i].EventBus = name
	}

	return m.PutEventEntries(ctx, events)
}
