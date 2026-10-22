package eventbridge

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

const (
	destStateActive   = "ACTIVE"
	destStateInactive = "INACTIVE"

	// defaultInvocationRate is the InvocationRateLimitPerSecond an API
	// destination gets when the request omits it.
	defaultInvocationRate = 300

	connectionARNPattern = `^arn:aws([a-z]|\-)*:events:([a-z]|\d|\-)*:([0-9]{12})?:connection\/[\.\-_A-Za-z0-9]+\/[\-A-Za-z0-9]+$`
	httpsEndpointPattern = `^((%[0-9A-Fa-f]{2}|[-()_.!~*';/?:@\x26=+$,A-Za-z0-9])+)([).!';/?:,])?$`
)

var (
	connectionARNRE = regexp.MustCompile(connectionARNPattern)
	httpsEndpointRE = regexp.MustCompile(httpsEndpointPattern)
)

func apiDestinationMethods() []string {
	return []string{httpPOST, httpGET, "HEAD", "OPTIONS", httpPUT, "PATCH", "DELETE"}
}

func errDestinationNotExist(name string) error {
	return errors.Newf(errors.NotFound, "An api-destination '%s' does not exist.", name)
}

// destinationFields checks the fields an API destination request sets, in the
// order of the API's members.
func destinationFields(v *violations, connARN, endpoint, method *string, rate *int) {
	if connARN != nil {
		v.pattern(*connARN, "connectionArn", connectionARNRE, connectionARNPattern)
	}

	if endpoint != nil {
		v.required(*endpoint, "invocationEndpoint", maxEndpointLen)
		v.pattern(*endpoint, "invocationEndpoint", httpsEndpointRE, httpsEndpointPattern)
	}

	if method != nil {
		v.enum(*method, "httpMethod", apiDestinationMethods())
	}

	if rate != nil && *rate < 1 {
		v.add(fmt.Sprint(*rate), "invocationRateLimitPerSecond", "Member must have value greater than or equal to 1")
	}
}

// connectionNameFromARN returns the connection name an ARN of the form
// arn:...:connection/<name>/<id> names.
func connectionNameFromARN(arn string) string {
	const infix = ":connection/"

	idx := strings.Index(arn, infix)
	if idx == -1 {
		return ""
	}

	rest := arn[idx+len(infix):]
	if slash := strings.Index(rest, "/"); slash != -1 {
		return rest[:slash]
	}

	return rest
}

// requireConnection checks that a connection with exactly this ARN exists.
func (m *Mock) requireConnection(arn string) error {
	name := connectionNameFromARN(arn)

	cd, ok := m.connections.Get(name)
	if !ok || cd.Info.ARN != arn {
		return errConnectionNotExist(name)
	}

	return nil
}

// destinationState reports ACTIVE when the destination's connection exists and
// is authorized, INACTIVE otherwise. settle lets a read move the connection out
// of a transient state first.
func (m *Mock) destinationState(connARN string, settle bool) string {
	name := connectionNameFromARN(connARN)

	var (
		cd connectionData
		ok bool
	)

	if settle {
		cd, ok = m.settleConnection(name)
	} else {
		cd, ok = m.connections.Get(name)
	}

	if ok && cd.Info.ARN == connARN && cd.Info.State == connStateAuthorized {
		return destStateActive
	}

	return destStateInactive
}

// CreateAPIDestination creates an API destination that calls an HTTP endpoint
// through an existing connection.
func (m *Mock) CreateAPIDestination(_ context.Context, cfg *driver.APIDestinationConfig) (*driver.APIDestination, error) {
	var v violations

	v.name(cfg.Name, "name", maxConnectionNameLen)
	v.maxLen(cfg.Description, "description", maxDescriptionLen)
	destinationFields(&v, &cfg.ConnectionARN, &cfg.InvocationEndpoint, &cfg.HTTPMethod, cfg.InvocationRateLimitPerSecond)

	if err := v.err(); err != nil {
		return nil, err
	}

	if err := m.requireConnection(cfg.ConnectionARN); err != nil {
		return nil, err
	}

	rate := defaultInvocationRate
	if cfg.InvocationRateLimitPerSecond != nil {
		rate = *cfg.InvocationRateLimitPerSecond
	}

	now := m.opts.Clock.Now().UTC()
	d := driver.APIDestination{
		Name:                         cfg.Name,
		ARN:                          idgen.AWSARN("events", m.opts.Region, m.opts.AccountID, "api-destination/"+cfg.Name+"/"+idgen.UUID()),
		Description:                  cfg.Description,
		ConnectionARN:                cfg.ConnectionARN,
		InvocationEndpoint:           cfg.InvocationEndpoint,
		HTTPMethod:                   cfg.HTTPMethod,
		InvocationRateLimitPerSecond: rate,
		CreatedAt:                    now,
		LastModifiedAt:               now,
	}

	if !m.apiDestinations.SetIfAbsent(cfg.Name, d) {
		return nil, errors.Newf(errors.AlreadyExists, "An api-destination '%s' already exists.", cfg.Name)
	}

	d.State = m.destinationState(d.ConnectionARN, false)

	return &d, nil
}

// DescribeAPIDestination returns an API destination and its current state.
func (m *Mock) DescribeAPIDestination(_ context.Context, name string) (*driver.APIDestination, error) {
	d, ok := m.apiDestinations.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound,
			"Failed to describe the api-destination(s). An api-destination '%s' does not exist.", name)
	}

	d.State = m.destinationState(d.ConnectionARN, true)

	return &d, nil
}

// UpdateAPIDestination changes the fields of an API destination that the
// request sets.
func (m *Mock) UpdateAPIDestination(_ context.Context, upd *driver.APIDestinationUpdate) (*driver.APIDestination, error) {
	var v violations

	if upd.Description != nil {
		v.maxLen(*upd.Description, "description", maxDescriptionLen)
	}

	destinationFields(&v, upd.ConnectionARN, upd.InvocationEndpoint, upd.HTTPMethod, upd.InvocationRateLimitPerSecond)

	if err := v.err(); err != nil {
		return nil, err
	}

	if upd.ConnectionARN != nil {
		if err := m.requireConnection(*upd.ConnectionARN); err != nil {
			return nil, err
		}
	}

	var out driver.APIDestination

	now := m.opts.Clock.Now().UTC()

	if !m.apiDestinations.Update(upd.Name, func(d driver.APIDestination) driver.APIDestination {
		applyDestinationUpdate(&d, upd)
		d.LastModifiedAt = now
		out = d

		return d
	}) {
		return nil, errDestinationNotExist(upd.Name)
	}

	out.State = m.destinationState(out.ConnectionARN, true)

	return &out, nil
}

func applyDestinationUpdate(d *driver.APIDestination, upd *driver.APIDestinationUpdate) {
	if upd.Description != nil {
		d.Description = *upd.Description
	}

	if upd.ConnectionARN != nil {
		d.ConnectionARN = *upd.ConnectionARN
	}

	if upd.InvocationEndpoint != nil {
		d.InvocationEndpoint = *upd.InvocationEndpoint
	}

	if upd.HTTPMethod != nil {
		d.HTTPMethod = *upd.HTTPMethod
	}

	if upd.InvocationRateLimitPerSecond != nil {
		d.InvocationRateLimitPerSecond = *upd.InvocationRateLimitPerSecond
	}
}

// DeleteAPIDestination deletes an API destination.
func (m *Mock) DeleteAPIDestination(_ context.Context, name string) error {
	if !m.apiDestinations.Delete(name) {
		return errDestinationNotExist(name)
	}

	return nil
}

// ListAPIDestinations lists API destinations sorted by name, narrowed by name
// prefix and connection.
func (m *Mock) ListAPIDestinations(_ context.Context, filter driver.APIDestinationFilter) ([]driver.APIDestination, error) {
	all := m.apiDestinations.All()
	out := make([]driver.APIDestination, 0, len(all))

	for name := range all {
		d := all[name]
		if !strings.HasPrefix(d.Name, filter.NamePrefix) || (filter.ConnectionARN != "" && d.ConnectionARN != filter.ConnectionARN) {
			continue
		}

		d.State = m.destinationState(d.ConnectionARN, true)
		out = append(out, d)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}
