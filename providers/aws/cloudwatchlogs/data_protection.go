package cloudwatchlogs

import (
	"context"
	"regexp"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs/dataprotection"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// maxLogGroupIdentifierLength is the logGroupIdentifier length limit of the
// data protection operations.
const maxLogGroupIdentifierLength = 2048

var logGroupIdentifierRE = regexp.MustCompile(`^[\w#+=/:,.@-]+$`)

// groupPolicy is a log group's data protection policy: the document as the
// caller sent it, and its parsed form.
type groupPolicy struct {
	document string
	updated  time.Time
	policy   *dataprotection.Policy
}

// groupByIdentifier resolves a log group name or ARN to its group.
func (m *Mock) groupByIdentifier(id string) (*logGroup, error) {
	if id == "" || len(id) > maxLogGroupIdentifierLength || !logGroupIdentifierRE.MatchString(id) {
		return nil, errors.New(errors.InvalidArgument,
			"1 validation error detected: Value at 'logGroupIdentifier' failed to satisfy constraint: "+
				`Member must satisfy regular expression pattern: [\w#+=/:,.@-]*`)
	}

	name := logGroupFromIdentifier(id)

	g, ok := m.groups.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "The specified log group does not exist: %s", name)
	}

	return g, nil
}

// parseDataProtection validates a policy document and checks that a findings
// destination log group exists. S3 and Firehose destinations are not checked.
func (m *Mock) parseDataProtection(doc string) (*dataprotection.Policy, error) {
	p, err := dataprotection.Parse(doc)
	if err != nil {
		return nil, err
	}

	if fg := p.FindingsLogGroup(); fg != "" && !m.groups.Has(fg) {
		return nil, errors.Newf(errors.InvalidArgument,
			"Invalid data protection policy: the findings destination log group %s does not exist", fg)
	}

	return p, nil
}

// PutDataProtectionPolicy sets a log group's data protection policy. Events
// ingested from now on are masked; earlier events are left as they are.
func (m *Mock) PutDataProtectionPolicy(_ context.Context, id, doc string) (*driver.DataProtectionPolicy, error) {
	g, err := m.groupByIdentifier(id)
	if err != nil {
		return nil, err
	}

	p, err := m.parseDataProtection(doc)
	if err != nil {
		return nil, err
	}

	now := m.opts.Clock.Now().UTC()
	g.dataProtection.Store(&groupPolicy{document: doc, updated: now, policy: p})

	return &driver.DataProtectionPolicy{LogGroupIdentifier: id, Document: doc, LastUpdatedTime: now}, nil
}

// GetDataProtectionPolicy returns a log group's policy. A group without one
// returns a policy with an empty document, as AWS does.
func (m *Mock) GetDataProtectionPolicy(_ context.Context, id string) (*driver.DataProtectionPolicy, error) {
	g, err := m.groupByIdentifier(id)
	if err != nil {
		return nil, err
	}

	out := &driver.DataProtectionPolicy{LogGroupIdentifier: id}
	if gp := g.dataProtection.Load(); gp != nil {
		out.Document, out.LastUpdatedTime = gp.document, gp.updated
	}

	return out, nil
}

// DeleteDataProtectionPolicy removes a log group's policy. Events masked while
// it was in force stay masked.
func (m *Mock) DeleteDataProtectionPolicy(_ context.Context, id string) error {
	g, err := m.groupByIdentifier(id)
	if err != nil {
		return err
	}

	if g.dataProtection.Swap(nil) == nil {
		return errors.Newf(errors.NotFound, "No data protection policy found for log group %s", g.info.Name)
	}

	return nil
}

// maskingPolicies are the policies that apply to a group: its own and the
// account's. They are cumulative.
func (m *Mock) maskingPolicies(g *logGroup) []*dataprotection.Policy {
	var out []*dataprotection.Policy

	if gp := g.dataProtection.Load(); gp != nil {
		out = append(out, gp.policy)
	}

	if p := m.accountDataProtection(); p != nil {
		out = append(out, p)
	}

	return out
}

// maskEvents masks accepted events at ingest. A masked event keeps its
// original text in Unmasked for unmask reads. It returns new events; the
// caller's slice is not changed.
func (m *Mock) maskEvents(g *logGroup, events []driver.LogEvent) []driver.LogEvent {
	policies := m.maskingPolicies(g)
	out := make([]driver.LogEvent, len(events))

	for i := range events {
		out[i] = events[i]
		out[i].Unmasked = ""

		if len(policies) == 0 {
			continue
		}

		if masked, ok := dataprotection.Mask(events[i].Message, policies...); ok {
			out[i].Unmasked = events[i].Message
			out[i].Message = masked
		}
	}

	return out
}

// eventView is what a read returns for a stored event: the masked text, or
// with unmask the original text. Unmasked never leaves the provider.
func eventView(e *driver.LogEvent, unmask bool) driver.LogEvent {
	out := *e
	if unmask && out.Unmasked != "" {
		out.Message = out.Unmasked
	}

	out.Unmasked = ""

	return out
}
