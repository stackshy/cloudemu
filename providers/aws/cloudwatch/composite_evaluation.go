package cloudwatch

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmrule"
)

// Values of ActionsSuppressedBy.
const (
	suppressedByWait      = "WaitPeriod"
	suppressedByExtension = "ExtensionPeriod"
	suppressedByAlarm     = "Alarm"
)

// compositeReasonTimeFormat is the time layout of a composite's state reason,
// as in "... transitioned to ALARM at Friday 22 July, 2022 15:57:45 UTC".
const compositeReasonTimeFormat = "Monday 02 January, 2006 15:04:05 UTC"

// alarmARNMarker separates an alarm ARN's prefix from the alarm name.
const alarmARNMarker = ":alarm:"

// compositeTrigger says why a composite is evaluated: its creation, its
// update, or a child alarm that changed state.
type compositeTrigger struct {
	created bool
	updated bool
	child   string
}

// alarmNameFromRef turns a rule reference, a name or an alarm ARN, into the
// alarm name.
func alarmNameFromRef(ref string) string {
	if !strings.HasPrefix(ref, "arn:") {
		return ref
	}

	if i := strings.Index(ref, alarmARNMarker); i >= 0 {
		return ref[i+len(alarmARNMarker):]
	}

	return ref
}

// alarmStateLocked returns the state of a metric or composite alarm by name.
// The caller holds alarmMu.
func (m *Mock) alarmStateLocked(name string) (string, bool) {
	if a, ok := m.alarms.Get(name); ok {
		return a.State, true
	}

	if c, ok := m.compositeAlarms.Get(name); ok {
		return c.State, true
	}

	return "", false
}

// alarmARNAndTimeLocked returns an alarm's ARN and when its state last
// changed. The caller holds alarmMu.
func (m *Mock) alarmARNAndTimeLocked(name string) (arn string, at time.Time) {
	if a, ok := m.alarms.Get(name); ok {
		return a.AlarmArn, a.StateTransitionedTimestamp
	}

	if c, ok := m.compositeAlarms.Get(name); ok {
		return c.ARN, c.StateTransitionedTimestamp
	}

	return "", time.Time{}
}

// stateOfRefLocked resolves a rule reference to a state. An alarm that no
// longer exists reads as INSUFFICIENT_DATA.
func (m *Mock) stateOfRefLocked(ref string) string {
	if s, ok := m.alarmStateLocked(alarmNameFromRef(ref)); ok {
		return s
	}

	return stateInsufficientData
}

// compositeRule parses a stored rule. Put validated it, so an error only
// comes from damaged restored state and the composite is then not evaluated.
func compositeRule(c *compositeAlarmData) *alarmrule.Rule {
	r, err := alarmrule.Parse(c.AlarmRule)
	if err != nil {
		return nil
	}

	return r
}

// compositeChildren returns the alarm names a composite's rule references.
func compositeChildren(c *compositeAlarmData) []string {
	r := compositeRule(c)
	if r == nil {
		return nil
	}

	refs := r.Refs()
	out := make([]string, 0, len(refs))

	for _, ref := range refs {
		out = append(out, alarmNameFromRef(ref))
	}

	return out
}

// onCycleLocked reports whether c can reach itself through the rules of
// composite alarms. AWS stops evaluating a composite on such a cycle.
func (m *Mock) onCycleLocked(c *compositeAlarmData) bool {
	visited := map[string]bool{}
	stack := compositeChildren(c)

	for len(stack) > 0 {
		name := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if name == c.Name {
			return true
		}

		if visited[name] {
			continue
		}

		visited[name] = true

		if child, ok := m.compositeAlarms.Get(name); ok {
			stack = append(stack, compositeChildren(child)...)
		}
	}

	return false
}

// evaluateCompositeLocked evaluates c's rule and moves it to ALARM or OK. A
// composite on a cycle keeps its state. The caller holds alarmMu. It returns
// the notice to publish, or nil.
func (m *Mock) evaluateCompositeLocked(c *compositeAlarmData, trigger compositeTrigger, now time.Time) *alarmNotice {
	rule := compositeRule(c)
	if rule == nil || m.onCycleLocked(c) {
		return nil
	}

	newState := stateOK
	if rule.Eval(m.stateOfRefLocked) {
		newState = stateAlarm
	}

	if newState == c.State {
		return nil
	}

	reason, data := m.compositeReasonLocked(c, newState, trigger)

	return m.transitionCompositeLocked(c, newState, reason, data, now)
}

// compositeReasonLocked builds the state reason and reasonData of a rule
// evaluation. A child trigger names that child. Creation and update list
// every child.
func (m *Mock) compositeReasonLocked(c *compositeAlarmData, newState string, trigger compositeTrigger) (reason, data string) {
	children := []string{trigger.child}

	switch {
	case trigger.created:
		reason = c.ARN + " was created and its alarm rule evaluates to " + newState
		children = compositeChildren(c)
	case trigger.updated:
		reason = c.ARN + " was updated and its alarm rule evaluates to " + newState
		children = compositeChildren(c)
	default:
		arn, at := m.alarmARNAndTimeLocked(trigger.child)
		state, _ := m.alarmStateLocked(trigger.child)
		reason = arn + " transitioned to " + state + " at " + at.UTC().Format(compositeReasonTimeFormat)
	}

	return reason, m.triggeringAlarmsLocked(children)
}

// triggeringAlarmsLocked renders the reasonData of a composite transition.
func (m *Mock) triggeringAlarmsLocked(names []string) string {
	type childState struct {
		Value     string `json:"value"`
		Timestamp string `json:"timestamp"`
	}

	type child struct {
		ARN   string     `json:"arn"`
		State childState `json:"state"`
	}

	out := struct {
		TriggeringAlarms []child `json:"triggeringAlarms"`
	}{TriggeringAlarms: []child{}}

	for _, name := range names {
		arn, at := m.alarmARNAndTimeLocked(name)
		if arn == "" {
			continue
		}

		state, _ := m.alarmStateLocked(name)
		out.TriggeringAlarms = append(out.TriggeringAlarms, child{
			ARN:   arn,
			State: childState{Value: state, Timestamp: at.UTC().Format(reasonDataTimeFormat)},
		})
	}

	b, err := json.Marshal(out)
	if err != nil {
		return ""
	}

	return string(b)
}

// transitionCompositeLocked moves c to newState, records history and decides
// whether the actions fire now or are held back by the suppressor. The state
// change event is always sent. The caller holds alarmMu.
func (m *Mock) transitionCompositeLocked(c *compositeAlarmData, newState, reason, data string, now time.Time) *alarmNotice {
	oldState := c.State

	m.appendHistoryEntry(c.Name, oldState, newState, reason, c.StateReasonData, data, now)

	prev := compositeEventStateOf(c)

	c.State = newState
	c.StateReason = reason
	c.StateReasonData = data
	c.StateUpdatedTimestamp = now
	c.StateTransitionedTimestamp = now

	fire := m.gateActionsLocked(c, oldState, now)

	notice := &alarmNotice{event: compositeStateEventLocked(c, &prev)}
	if fire {
		notice.topics, notice.message = m.compositeActionTopics(c, oldState, now)
	}

	return notice
}

// gateActionsLocked applies the suppressor to a transition and reports
// whether its actions fire now. With the suppressor in ALARM they are held
// until it leaves ALARM and its ExtensionPeriod ends. An active
// ExtensionPeriod keeps holding them until it ends. A move into ALARM with the
// suppressor not in ALARM is held for the WaitPeriod, which gives the
// suppressor time to enter ALARM. API_PutCompositeAlarm ties the WaitPeriod to
// the suppressor going into ALARM, so other transitions do not wait.
func (m *Mock) gateActionsLocked(c *compositeAlarmData, oldState string, now time.Time) bool {
	if c.ActionsSuppressor == "" {
		return true
	}

	if m.stateOfRefLocked(c.ActionsSuppressor) == stateAlarm {
		c.holdActions(suppressedByAlarm, time.Time{}, oldState)

		return false
	}

	if c.ActionsSuppressedBy == suppressedByExtension && now.Before(c.SuppressUntil) {
		c.holdActions(suppressedByExtension, c.SuppressUntil, oldState)

		return false
	}

	if c.State != stateAlarm || c.WaitPeriod <= 0 {
		c.clearSuppression()

		return true
	}

	c.holdActions(suppressedByWait, now.Add(time.Duration(c.WaitPeriod)*time.Second), oldState)

	return false
}

// holdActions marks the actions of a transition out of from as held back.
func (c *compositeAlarmData) holdActions(by string, until time.Time, from string) {
	c.ActionsSuppressedBy = by
	c.ActionsSuppressedReason = "Actions suppressed by " + by
	c.SuppressUntil = until
	c.PendingActions = true
	c.PendingFrom = from
}

// clearSuppression ends suppression and drops held actions.
func (c *compositeAlarmData) clearSuppression() {
	c.ActionsSuppressedBy = ""
	c.ActionsSuppressedReason = ""
	c.SuppressUntil = time.Time{}
	c.PendingActions = false
	c.PendingFrom = ""
}

// suppressorChangedLocked reacts to a state change of c's suppressor. Entering
// ALARM replaces an active period. Leaving ALARM starts the ExtensionPeriod.
// It returns a notice when held actions are released at once.
func (m *Mock) suppressorChangedLocked(c *compositeAlarmData, now time.Time) *alarmNotice {
	if m.stateOfRefLocked(c.ActionsSuppressor) == stateAlarm {
		c.ActionsSuppressedBy = suppressedByAlarm
		c.ActionsSuppressedReason = "Actions suppressed by " + suppressedByAlarm
		c.SuppressUntil = time.Time{}

		return nil
	}

	if c.ActionsSuppressedBy != suppressedByAlarm {
		return nil
	}

	if c.ExtensionPeriod <= 0 {
		return m.releaseActionsLocked(c, now)
	}

	c.ActionsSuppressedBy = suppressedByExtension
	c.ActionsSuppressedReason = "Actions suppressed by " + suppressedByExtension
	c.SuppressUntil = now.Add(time.Duration(c.ExtensionPeriod) * time.Second)

	return nil
}

// releaseActionsLocked ends suppression. Held actions fire for the state the
// composite is in now, as the AWS user guide describes.
func (m *Mock) releaseActionsLocked(c *compositeAlarmData, now time.Time) *alarmNotice {
	pending, from := c.PendingActions, c.PendingFrom
	c.clearSuppression()

	if !pending {
		return nil
	}

	topics, message := m.compositeActionTopics(c, from, now)
	if len(topics) == 0 {
		return nil
	}

	return &alarmNotice{topics: topics, message: message}
}

// expireSuppressionLocked releases the actions of every composite whose
// WaitPeriod or ExtensionPeriod has ended by now.
func (m *Mock) expireSuppressionLocked(now time.Time) []*alarmNotice {
	var notices []*alarmNotice

	for _, c := range m.compositeAlarms.SortedValues() {
		if c.ActionsSuppressedBy != suppressedByWait && c.ActionsSuppressedBy != suppressedByExtension {
			continue
		}

		if now.Before(c.SuppressUntil) {
			continue
		}

		if n := m.releaseActionsLocked(c, now); n != nil {
			notices = append(notices, n)
		}
	}

	return notices
}

// settleLocked brings composites up to date after the named alarms changed
// state. A composite that changes in turn is settled too, so a composite of
// composites follows. Composites on a cycle are skipped, which ends the walk.
// The caller holds alarmMu.
func (m *Mock) settleLocked(changed []string, now time.Time) []*alarmNotice {
	var notices []*alarmNotice

	// A composite changes at most once per alarm that changed below it, so the
	// budget only guards against a bug.
	count := m.compositeAlarms.Len()
	budget := (count + 1) * (count + len(changed) + 1)

	for len(changed) > 0 && budget > 0 {
		name := changed[0]
		changed = changed[1:]

		for _, c := range m.compositeAlarms.SortedValues() {
			budget--

			if c.ActionsSuppressor != "" && alarmNameFromRef(c.ActionsSuppressor) == name {
				if n := m.suppressorChangedLocked(c, now); n != nil {
					notices = append(notices, n)
				}
			}

			if !hasChild(c, name) {
				continue
			}

			if n := m.evaluateCompositeLocked(c, compositeTrigger{child: name}, now); n != nil {
				notices = append(notices, n)
				changed = append(changed, c.Name)
			}
		}
	}

	return notices
}

func hasChild(c *compositeAlarmData, name string) bool {
	for _, child := range compositeChildren(c) {
		if child == name {
			return true
		}
	}

	return false
}

// compositeActionTopics returns the SNS topics and message of c's current
// state. Other action ARNs are stored but not fired.
func (m *Mock) compositeActionTopics(c *compositeAlarmData, oldState string, now time.Time) (topics []string, message string) {
	if m.sns == nil || !c.ActionsEnabled {
		return nil, ""
	}

	topics = snsTopics(stateActions(c.State, c.AlarmActions, c.OKActions, c.InsufficientDataActions))
	if len(topics) == 0 {
		return nil, ""
	}

	payload := map[string]any{
		"AlarmName":        c.Name,
		"AlarmDescription": c.AlarmDescription,
		"AlarmArn":         c.ARN,
		"AlarmRule":        c.AlarmRule,
		"AWSAccountId":     m.opts.AccountID,
		"Region":           m.opts.Region,
		"NewStateValue":    c.State,
		"NewStateReason":   c.StateReason,
		"OldStateValue":    oldState,
		"StateChangeTime":  now.UTC().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, ""
	}

	return topics, string(body)
}
