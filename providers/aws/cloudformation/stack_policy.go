package cloudformation

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// Error texts of the stack policy inputs.
const (
	msgBothPolicies       = "You cannot specify both StackPolicyURL and StackPolicyBody"
	msgBothDuringPolicies = "You cannot specify both StackPolicyDuringUpdateURL and StackPolicyDuringUpdateBody"
	msgPolicyRequired     = "Either StackPolicyBody or StackPolicyURL must be specified. " +
		"A stack policy cannot be removed, only replaced."
	msgPolicyNotS3URL = "StackPolicyURL must be an Amazon S3 URL."
	msgPolicyAccess   = "StackPolicyURL must reference a valid S3 object to which you have access."
)

// SetStackPolicy replaces a stack's policy. There is no way to remove one:
// a body or URL is required, and a policy that allows everything is how a
// stack is unprotected again.
func (m *Mock) SetStackPolicy(ctx context.Context, in *cfn.SetStackPolicyInput) error {
	m.settle(ctx)

	sd, err := m.activeStack(in.StackName)
	if err != nil {
		return err
	}

	body, err := m.policyBody(ctx, in.StackPolicyBody, in.StackPolicyURL, msgBothPolicies)
	if err != nil {
		return err
	}

	if body == "" {
		return cerrors.New(cerrors.InvalidArgument, msgPolicyRequired)
	}

	sd.setStackPolicy(body)

	return nil
}

// GetStackPolicy returns a stack's policy body, or "" when it has none.
func (m *Mock) GetStackPolicy(ctx context.Context, name string) (string, error) {
	m.settle(ctx)

	sd, err := m.activeStack(name)
	if err != nil {
		return "", err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	return sd.stackPolicy, nil
}

// policyBody returns the policy a request names by body or S3 URL, checked
// against the stack policy grammar, or "" when it names none.
func (m *Mock) policyBody(ctx context.Context, body, policyURL, bothMsg string) (string, error) {
	switch {
	case body != "" && policyURL != "":
		return "", cerrors.New(cerrors.InvalidArgument, bothMsg)
	case body == "" && policyURL == "":
		return "", nil
	case policyURL != "":
		fetched, err := m.fetchPolicy(ctx, policyURL)
		if err != nil {
			return "", err
		}

		body = fetched
	}

	if _, err := cfn.ParseStackPolicy(body); err != nil {
		return "", err
	}

	return body, nil
}

func (m *Mock) fetchPolicy(ctx context.Context, policyURL string) (string, error) {
	obj, ok := parseS3URL(policyURL)
	if !ok {
		return "", cerrors.New(cerrors.InvalidArgument, msgPolicyNotS3URL)
	}

	if m.fetchTemplate == nil {
		return "", cerrors.New(cerrors.InvalidArgument, msgPolicyAccess)
	}

	data, err := m.fetchTemplate(ctx, obj.bucket, obj.key, obj.versionID)
	if err != nil {
		return "", cerrors.New(cerrors.InvalidArgument, msgPolicyAccess)
	}

	return string(data), nil
}

// updatePolicy works out the policy an update is held to: the one given for
// the update only, else the stack's own. It is nil when there is neither.
// The stored body was checked when it was set.
func updatePolicy(sd *stackData, during string) (*cfn.StackPolicy, error) {
	body := during
	if body == "" {
		sd.mu.RLock()
		body = sd.stackPolicy
		sd.mu.RUnlock()
	}

	if body == "" {
		return nil, nil
	}

	return cfn.ParseStackPolicy(body)
}

func (sd *stackData) setStackPolicy(body string) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.stackPolicy = body
}

// policyDenies fails a resource whose update the stack policy does not
// allow. The resource is left as it is.
func (m *Mock) policyDenies(sd *stackData, policy *cfn.StackPolicy, action, id string, live *liveResource) *applyFailure {
	if policy == nil {
		return nil
	}

	reason, ok := policy.Allows(action, id, live.typ)
	if ok {
		return nil
	}

	m.emitResourceEvent(sd, id, live.resolved.RefValue, live.typ, cfn.ResourceUpdateFailed, reason)

	return &applyFailure{logicalID: id, verb: verbUpdate, err: cerrors.New(cerrors.InvalidArgument, reason)}
}

// checkDrops checks the resources an update drops from the template against
// the stack policy's Update:Delete, before the cleanup deletes any of them.
func (m *Mock) checkDrops(sd *stackData, t *cfn.Template, o *convergeOpts) []applyFailure {
	if o.policy == nil || o.rollback {
		return nil
	}

	sd.mu.RLock()
	order := append([]string(nil), sd.provisionOrder...)
	sd.mu.RUnlock()

	for i := len(order) - 1; i >= 0; i-- {
		id := order[i]
		if _, kept := t.Resources[id]; kept || o.skip[id] {
			continue
		}

		live, ok := sd.live(id)
		if !ok {
			continue
		}

		if f := m.policyDenies(sd, o.policy, cfn.StackPolicyDelete, id, &live); f != nil {
			return []applyFailure{*f}
		}
	}

	return nil
}
