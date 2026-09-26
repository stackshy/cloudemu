package ssm

import (
	"context"
	"time"
)

// eventPolicyAction is the EventBridge detail-type Parameter Store uses when
// a parameter policy acts.
const eventPolicyAction = "Parameter Store Policy Action"

// policyActionDetail is the detail of a "Parameter Store Policy Action" event.
type policyActionDetail struct {
	ParameterName string `json:"parameter-name"`
	ParameterType string `json:"parameter-type"`
	PolicyType    string `json:"policy-type"`
	ActionStatus  string `json:"action-status"`
	ActionReason  string `json:"action-reason"`
}

// policyNotice is one policy action waiting to be published. It is built
// under the parameter lock and published after the lock is released, so a
// rule target that calls back into this mock cannot deadlock.
type policyNotice struct {
	name   string
	detail policyActionDetail
}

// Tick runs every parameter policy that is due at now. Expired parameters are
// deleted and each action publishes a "Parameter Store Policy Action" event.
// It reports whether anything changed. It has the Tickable signature of the
// shared scheduler. Reads also evaluate policies, so nothing has to call it.
func (m *Mock) Tick(now time.Time) bool {
	return m.evaluateAllPolicies(context.Background(), now)
}

// evaluateAllPolicies runs the due policies of every parameter.
func (m *Mock) evaluateAllPolicies(ctx context.Context, now time.Time) bool {
	changed := false

	for _, pd := range m.params.All() {
		if m.evaluateParamPolicies(ctx, pd, now) {
			changed = true
		}
	}

	return changed
}

// evaluateNamedPolicies runs the due policies of one parameter at the mock's
// clock, so an expired parameter reads as missing.
func (m *Mock) evaluateNamedPolicies(ctx context.Context, name string) {
	if pd, ok := m.params.Get(name); ok {
		m.evaluateParamPolicies(ctx, pd, m.opts.Clock.Now())
	}
}

// evaluateParamPolicies runs pd's due policies, removes pd when it expired and
// then publishes the actions. It must be called without any lock held.
func (m *Mock) evaluateParamPolicies(ctx context.Context, pd *paramData, now time.Time) bool {
	notices := pd.duePolicies(now, func() {
		// Remove only this record. A parameter re-created under the same name
		// after this one was read is left alone.
		m.params.UpdateOrDelete(pd.name, func(cur *paramData) (*paramData, bool) {
			return cur, cur != pd
		})
	})

	for i := range notices {
		m.events.Emit(ctx, eventSource, eventPolicyAction, notices[i].detail, m.arn(notices[i].name))
	}

	return len(notices) > 0
}

// duePolicies marks the policies of pd that are due at now as Finished and
// returns their notices. When the Expiration time has passed it marks pd
// deleted and calls remove, both under pd.mu. So only the first caller emits
// the Expiration notice, and an overwrite waiting on pd.mu sees the deletion.
func (pd *paramData) duePolicies(now time.Time, remove func()) []policyNotice {
	pd.mu.Lock()
	defer pd.mu.Unlock()

	if pd.deleted {
		return nil
	}

	v, ok := pd.versionByNumber(pd.latest)
	if !ok || len(v.policies) == 0 {
		return nil
	}

	var notices []policyNotice

	exp := expiration(v.policies)

	for _, p := range v.policies {
		if p.status != policyStatusPending {
			continue
		}

		if reason, due := p.notificationDue(exp, v, now); due {
			p.status = policyStatusFinished
			notices = append(notices, pd.notice(v, p.typ, reason))
		}
	}

	if exp != nil && exp.status == policyStatusPending && !now.Before(exp.at) {
		exp.status = policyStatusFinished
		pd.deleted = true

		remove()

		notices = append(notices, pd.notice(v, policyExpiration, "The parameter expired and was deleted."))
	}

	return notices
}

// notificationDue reports whether a notification policy fires at now, with
// the reason it carries. An ExpirationNotification needs an Expiration policy.
func (p *policy) notificationDue(exp *policy, v *version, now time.Time) (string, bool) {
	switch p.typ {
	case policyExpirationNotification:
		if exp != nil && !now.Before(exp.at.Add(-p.offset)) {
			return "The parameter will expire at " + exp.at.Format(time.RFC3339) + ".", true
		}
	case policyNoChangeNotification:
		last, err := time.Parse(time.RFC3339, v.lastModified)
		if err == nil && !now.Before(last.Add(p.offset)) {
			return "The parameter has not been changed for " + p.period + ".", true
		}
	}

	return "", false
}

// notice builds the event for one policy action on pd.
func (pd *paramData) notice(v *version, policyType, reason string) policyNotice {
	return policyNotice{name: pd.name, detail: policyActionDetail{
		ParameterName: pd.name,
		ParameterType: v.typ,
		PolicyType:    policyType,
		ActionStatus:  policyStatusFinished,
		ActionReason:  reason,
	}}
}
