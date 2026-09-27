package ssm

import (
	"bytes"
	"encoding/json"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

// Parameter policy types and statuses. See "Assigning parameter policies in
// Parameter Store" in the Systems Manager user guide.
const (
	policyExpiration             = "Expiration"
	policyExpirationNotification = "ExpirationNotification"
	policyNoChangeNotification   = "NoChangeNotification"

	policyStatusPending  = "Pending"
	policyStatusFinished = "Finished"

	policyVersion     = "1.0"
	maxPolicies       = 10
	maxPoliciesLength = 4096
)

// policy is one parsed parameter policy. at is the Expiration time. offset is
// the Before or After period of a notification policy.
type policy struct {
	typ    string
	text   string
	status string
	at     time.Time
	offset time.Duration
	// period is the notification period as sent, such as "15 Days".
	period string
}

// rawPolicy is the wire form of one element of the Policies array.
type rawPolicy struct {
	Type       *string            `json:"Type"`
	Version    *string            `json:"Version"`
	Attributes map[string]*string `json:"Attributes"`
}

// parsePolicies validates a Policies JSON array. An empty array, or one that
// holds only empty objects, returns no policies, which clears them. When
// requireFuture is set an Expiration timestamp must be after now.
func parsePolicies(raw string, now time.Time, requireFuture bool) ([]*policy, error) {
	if len(raw) > maxPoliciesLength {
		return nil, errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value at 'policies' failed to satisfy constraint: "+
				"Member must have length less than or equal to %d", maxPoliciesLength)
	}

	var elems []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &elems); err != nil {
		//nolint:revive // user-facing ValidationException text
		return nil, errors.New(errors.InvalidArgument, "Policies must be a JSON array of policy objects.")
	}

	var out []*policy

	for _, elem := range elems {
		p, err := parsePolicy(elem, now, requireFuture)
		if err != nil {
			return nil, err
		}

		if p != nil {
			out = append(out, p)
		}
	}

	if len(out) > maxPolicies {
		return nil, driver.ErrPoliciesLimitExceeded
	}

	if err := checkCompatible(out); err != nil {
		return nil, err
	}

	return out, nil
}

// parsePolicy parses one element. An empty object returns nil.
func parsePolicy(elem json.RawMessage, now time.Time, requireFuture bool) (*policy, error) {
	var rp rawPolicy
	if err := json.Unmarshal(elem, &rp); err != nil {
		return nil, driver.ErrInvalidPolicyAttribute
	}

	if rp.Type == nil && rp.Version == nil && rp.Attributes == nil {
		return nil, nil
	}

	if err := rp.checkHeader(); err != nil {
		return nil, err
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, elem); err != nil {
		return nil, driver.ErrInvalidPolicyAttribute
	}

	p := &policy{typ: *rp.Type, text: compact.String(), status: policyStatusPending}
	if err := p.parseAttributes(rp.Attributes, now, requireFuture); err != nil {
		return nil, err
	}

	return p, nil
}

// parseAttributes reads the Attributes of p's policy type.
func (p *policy) parseAttributes(attrs map[string]*string, now time.Time, requireFuture bool) error {
	var err error

	switch p.typ {
	case policyExpiration:
		p.at, err = parseExpiration(attrs, now, requireFuture)
	case policyExpirationNotification:
		p.offset, p.period, err = parsePeriod(attrs, "Before")
	case policyNoChangeNotification:
		p.offset, p.period, err = parsePeriod(attrs, "After")
	}

	return err
}

// checkHeader validates the policy Type and Version.
func (rp *rawPolicy) checkHeader() error {
	if rp.Type == nil || !knownPolicyType(*rp.Type) {
		return driver.ErrInvalidPolicyType
	}

	if rp.Version == nil || *rp.Version != policyVersion {
		return driver.ErrInvalidPolicyAttribute
	}

	return nil
}

// knownPolicyType reports whether t is a policy type Parameter Store supports.
func knownPolicyType(t string) bool {
	switch t {
	case policyExpiration, policyExpirationNotification, policyNoChangeNotification:
		return true
	default:
		return false
	}
}

// parseTimestamp parses an Expiration Timestamp. It accepts the ISO_INSTANT
// and ISO_OFFSET_DATE_TIME forms. The user guide shows an offset with
// seconds, such as +10:30:00.
func parseTimestamp(s string) (time.Time, bool) {
	layouts := [...]string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999999-07:00:00",
		"2006-01-02T15:04Z07:00",
	}

	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}

	return time.Time{}, false
}

// parseExpiration reads the Timestamp attribute, in ISO_INSTANT or
// ISO_OFFSET_DATE_TIME form.
func parseExpiration(attrs map[string]*string, now time.Time, requireFuture bool) (time.Time, error) {
	if len(attrs) != 1 || attrs["Timestamp"] == nil {
		return time.Time{}, driver.ErrInvalidPolicyAttribute
	}

	at, ok := parseTimestamp(*attrs["Timestamp"])
	if !ok {
		return time.Time{}, driver.ErrInvalidPolicyAttribute
	}

	if requireFuture && !at.After(now) {
		return time.Time{}, driver.ErrInvalidPolicyAttribute
	}

	return at.UTC(), nil
}

// parsePeriod reads a positive whole number under key and a Unit of Days or
// Hours. It returns the period and its text, such as "15 Days".
func parsePeriod(attrs map[string]*string, key string) (time.Duration, string, error) {
	const hoursPerDay = 24

	if len(attrs) != 2 || attrs[key] == nil || attrs["Unit"] == nil {
		return 0, "", driver.ErrInvalidPolicyAttribute
	}

	n, err := strconv.Atoi(*attrs[key])
	if err != nil || n < 1 {
		return 0, "", driver.ErrInvalidPolicyAttribute
	}

	unit := *attrs["Unit"]
	text := strconv.Itoa(n) + " " + unit

	switch unit {
	case "Days":
		return time.Duration(n) * hoursPerDay * time.Hour, text, nil
	case "Hours":
		return time.Duration(n) * time.Hour, text, nil
	default:
		return 0, "", driver.ErrInvalidPolicyAttribute
	}
}

// checkCompatible rejects a second Expiration or NoChangeNotification policy.
// Several ExpirationNotification policies are allowed.
func checkCompatible(ps []*policy) error {
	seen := map[string]bool{}

	for _, p := range ps {
		if p.typ == policyExpirationNotification {
			continue
		}

		if seen[p.typ] {
			return driver.ErrIncompatiblePolicy
		}

		seen[p.typ] = true
	}

	return nil
}

// expiration returns the Expiration policy, or nil.
func expiration(ps []*policy) *policy {
	for _, p := range ps {
		if p.typ == policyExpiration {
			return p
		}
	}

	return nil
}

// clonePolicies copies policies, so a new version can change their status
// without changing an older version.
func clonePolicies(ps []*policy) []*policy {
	if len(ps) == 0 {
		return nil
	}

	out := make([]*policy, 0, len(ps))

	for _, p := range ps {
		c := *p
		out = append(out, &c)
	}

	return out
}

// resetNoChange sets NoChangeNotification policies back to Pending. A change
// to the parameter restarts their period.
func resetNoChange(ps []*policy) {
	for _, p := range ps {
		if p.typ == policyNoChangeNotification {
			p.status = policyStatusPending
		}
	}
}

// toDriverPolicies renders policies for the read paths. It is never nil, so
// the wire shows an empty list when there are none.
func toDriverPolicies(ps []*policy) []driver.ParameterPolicy {
	out := make([]driver.ParameterPolicy, 0, len(ps))
	for _, p := range ps {
		out = append(out, driver.ParameterPolicy{Text: p.text, Type: p.typ, Status: p.status})
	}

	return out
}
