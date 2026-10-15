package batch

import (
	"context"
	"encoding/json"
	"regexp"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

// Fair-share policy bounds from the FairsharePolicy and ShareAttributes API
// reference.
const (
	maxComputeReservation = 99
	maxShareDecaySeconds  = 604800
	minWeightFactor       = 0.0001
	maxWeightFactor       = 999.9999
	maxShareIdentifierLen = 255
	maxDescribeArns       = 100
)

// resourceNameRE is the CreateSchedulingPolicy / CreateJobQueue name rule: up
// to 128 letters, numbers, hyphens and underscores.
var resourceNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// fairsharePolicy is the subset of the FairsharePolicy shape the emulator
// validates; the raw JSON the caller sent is what is stored and echoed.
type fairsharePolicy struct {
	ComputeReservation *int64            `json:"computeReservation"`
	ShareDecaySeconds  *int64            `json:"shareDecaySeconds"`
	ShareDistribution  []shareAttributes `json:"shareDistribution"`
}

// shareAttributes is one ShareAttributes entry of a fair-share policy.
type shareAttributes struct {
	ShareIdentifier *string  `json:"shareIdentifier"`
	WeightFactor    *float64 `json:"weightFactor"`
}

//nolint:gocritic // clone intentionally takes a value to return an independent copy-on-write copy
func cloneSchedulingPolicy(p driver.SchedulingPolicy) driver.SchedulingPolicy {
	p.FairsharePolicy = copyRaw(p.FairsharePolicy)
	p.QuotaSharePolicy = copyRaw(p.QuotaSharePolicy)
	p.Tags = copyTags(p.Tags)

	return p
}

// isSet reports whether a raw JSON member was actually supplied (not absent or
// an explicit null).
func isSet(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}

// validateFairshare enforces the documented FairsharePolicy bounds.
func validateFairshare(raw json.RawMessage) error {
	if !isSet(raw) {
		return nil
	}

	var fp fairsharePolicy
	if err := json.Unmarshal(raw, &fp); err != nil {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid fairsharePolicy: %v", err)
	}

	if err := checkRange("computeReservation", fp.ComputeReservation, maxComputeReservation); err != nil {
		return err
	}

	if err := checkRange("shareDecaySeconds", fp.ShareDecaySeconds, maxShareDecaySeconds); err != nil {
		return err
	}

	for i := range fp.ShareDistribution {
		if err := validateShareAttributes(i, &fp.ShareDistribution[i]); err != nil {
			return err
		}
	}

	return nil
}

// checkRange rejects a set fair-share integer outside [0, maxValue].
func checkRange(field string, v *int64, maxValue int64) error {
	if v != nil && (*v < 0 || *v > maxValue) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"fairsharePolicy.%s must be between 0 and %d, got %d", field, maxValue, *v)
	}

	return nil
}

// validateShareAttributes enforces the ShareAttributes bounds of one
// shareDistribution entry: a required identifier of at most 255 characters
// (plus an optional trailing '*') and a weight factor in [0.0001, 999.9999].
func validateShareAttributes(i int, sd *shareAttributes) error {
	if sd.ShareIdentifier == nil || *sd.ShareIdentifier == "" {
		return cerrors.Newf(cerrors.InvalidArgument, "fairsharePolicy.shareDistribution[%d].shareIdentifier is required", i)
	}

	if len(*sd.ShareIdentifier) > maxShareIdentifierLen+1 {
		return cerrors.Newf(cerrors.InvalidArgument,
			"fairsharePolicy.shareDistribution[%d].shareIdentifier is longer than %d characters", i, maxShareIdentifierLen)
	}

	if w := sd.WeightFactor; w != nil && (*w < minWeightFactor || *w > maxWeightFactor) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"fairsharePolicy.shareDistribution[%d].weightFactor must be between %v and %v, got %v",
			i, minWeightFactor, maxWeightFactor, *w)
	}

	return nil
}

// CreateSchedulingPolicy creates a fair-share (or quota-share) scheduling
// policy. Only one of the two policy kinds may be set.
func (m *Mock) CreateSchedulingPolicy(
	_ context.Context, in driver.CreateSchedulingPolicyInput,
) (*driver.SchedulingPolicy, error) {
	if in.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "name is required")
	}

	if !resourceNameRE.MatchString(in.Name) {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"scheduling policy name %q must be up to 128 letters, numbers, hyphens and underscores", in.Name)
	}

	if isSet(in.FairsharePolicy) && isSet(in.QuotaSharePolicy) {
		return nil, cerrors.New(cerrors.InvalidArgument, "only one of fairsharePolicy or quotaSharePolicy can be set")
	}

	if err := validateTags(in.Tags); err != nil {
		return nil, err
	}

	if err := validateFairshare(in.FairsharePolicy); err != nil {
		return nil, err
	}

	p := driver.SchedulingPolicy{
		Name:             in.Name,
		ARN:              m.arn(kindSchedulingPolicy, in.Name),
		FairsharePolicy:  copyRaw(nonNull(in.FairsharePolicy)),
		QuotaSharePolicy: copyRaw(nonNull(in.QuotaSharePolicy)),
		Tags:             copyTags(in.Tags),
	}

	if !m.schedPolicies.SetIfAbsent(in.Name, p) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "scheduling policy %q already exists", in.Name)
	}

	out := cloneSchedulingPolicy(p)

	return &out, nil
}

// nonNull drops an explicit JSON null so it is stored as "absent".
func nonNull(raw json.RawMessage) json.RawMessage {
	if !isSet(raw) {
		return nil
	}

	return raw
}

// DescribeSchedulingPolicies returns the named policies (ARN or name) in
// request order; unknown entries are omitted. At least one and at most 100
// ARNs are required.
func (m *Mock) DescribeSchedulingPolicies(_ context.Context, arns []string) ([]driver.SchedulingPolicy, error) {
	if len(arns) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "arns is required")
	}

	if len(arns) > maxDescribeArns {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "arns accepts at most %d entries, got %d", maxDescribeArns, len(arns))
	}

	return describeByNames(m.schedPolicies, arns, kindSchedulingPolicy, cloneSchedulingPolicy), nil
}

// ListSchedulingPolicies returns every policy, ordered by name.
func (m *Mock) ListSchedulingPolicies(_ context.Context) ([]driver.SchedulingPolicy, error) {
	return describeByNames(m.schedPolicies, nil, kindSchedulingPolicy, cloneSchedulingPolicy), nil
}

// UpdateSchedulingPolicy replaces the policy details. A fair-share policy can't
// be turned into a quota-share policy or vice versa.
func (m *Mock) UpdateSchedulingPolicy(_ context.Context, in driver.UpdateSchedulingPolicyInput) error {
	if in.ARN == "" {
		return cerrors.New(cerrors.InvalidArgument, "arn is required")
	}

	if isSet(in.FairsharePolicy) && isSet(in.QuotaSharePolicy) {
		return cerrors.New(cerrors.InvalidArgument, "only one of fairsharePolicy or quotaSharePolicy can be set")
	}

	if err := validateFairshare(in.FairsharePolicy); err != nil {
		return err
	}

	name := nameFromARN(kindSchedulingPolicy, in.ARN)

	cur, ok := m.schedPolicies.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "scheduling policy %q does not exist", in.ARN)
	}

	if err := checkPolicyKind(&cur, &in); err != nil {
		return err
	}

	m.schedPolicies.Update(name, func(p driver.SchedulingPolicy) driver.SchedulingPolicy {
		next := cloneSchedulingPolicy(p)
		if isSet(in.FairsharePolicy) {
			next.FairsharePolicy = copyRaw(in.FairsharePolicy)
		}

		if isSet(in.QuotaSharePolicy) {
			next.QuotaSharePolicy = copyRaw(in.QuotaSharePolicy)
		}

		return next
	})

	return nil
}

// checkPolicyKind rejects an update that would turn a fair-share policy into a
// quota-share one or vice versa.
func checkPolicyKind(cur *driver.SchedulingPolicy, in *driver.UpdateSchedulingPolicyInput) error {
	if isSet(in.FairsharePolicy) && isSet(cur.QuotaSharePolicy) {
		return cerrors.New(cerrors.InvalidArgument, "a quotaSharePolicy can't be changed to a fairsharePolicy")
	}

	if isSet(in.QuotaSharePolicy) && isSet(cur.FairsharePolicy) {
		return cerrors.New(cerrors.InvalidArgument, "a fairsharePolicy can't be changed to a quotaSharePolicy")
	}

	return nil
}

// DeleteSchedulingPolicy deletes a policy. A policy still referenced by a job
// queue can't be deleted.
func (m *Mock) DeleteSchedulingPolicy(_ context.Context, arn string) error {
	if arn == "" {
		return cerrors.New(cerrors.InvalidArgument, "arn is required")
	}

	name := nameFromARN(kindSchedulingPolicy, arn)

	p, ok := m.schedPolicies.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "scheduling policy %q does not exist", arn)
	}

	queues := m.jobQueues.SortedValues()
	for i := range queues {
		q := &queues[i]
		if q.SchedulingPolicyARN != "" && nameFromARN(kindSchedulingPolicy, q.SchedulingPolicyARN) == name {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"scheduling policy %q is in use by job queue %q", p.ARN, q.Name)
		}
	}

	m.schedPolicies.Delete(name)

	return nil
}

// resolveSchedulingPolicy returns the canonical ARN of an existing policy, or a
// ClientException-mapped error when it does not exist.
func (m *Mock) resolveSchedulingPolicy(ref string) (string, error) {
	p, ok := m.schedPolicies.Get(nameFromARN(kindSchedulingPolicy, ref))
	if !ok {
		return "", cerrors.Newf(cerrors.InvalidArgument, "scheduling policy %q does not exist", ref)
	}

	return p.ARN, nil
}
