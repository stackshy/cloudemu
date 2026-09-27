package cloudwatch

import (
	"context"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// QueryAlarms answers a DescribeAlarms call. With no AlarmTypes only metric
// alarms are returned, as the DescribeAlarms API documents. ChildrenOf returns
// the metric and composite alarms a composite's rule references, and ParentsOf
// the composites that reference an alarm. Both take no other filter. Due
// alarms are evaluated first.
func (m *Mock) QueryAlarms(ctx context.Context, q *driver.AlarmQuery) (*driver.AlarmQueryResult, error) {
	if err := validateAlarmQuery(q); err != nil {
		return nil, err
	}

	m.evaluateDue(ctx, m.opts.Clock.Now())

	m.alarmMu.Lock()
	defer m.alarmMu.Unlock()

	var metrics, composites []string

	switch {
	case q.ChildrenOf != "":
		metrics, composites = m.childrenOfLocked(q.ChildrenOf)
	case q.ParentsOf != "":
		composites = m.parentsOfLocked(q.ParentsOf)
	default:
		metrics, composites = m.alarms.Keys(), m.compositeAlarms.Keys()
	}

	out := &driver.AlarmQueryResult{MetricAlarms: []driver.AlarmInfo{}, CompositeAlarms: []driver.CompositeAlarmInfo{}}

	family := q.ChildrenOf != "" || q.ParentsOf != ""

	if family || wantsType(q.AlarmTypes, driver.AlarmTypeMetric) {
		out.MetricAlarms = m.metricRowsLocked(metrics, q)
	}

	if family || wantsType(q.AlarmTypes, driver.AlarmTypeComposite) {
		out.CompositeAlarms = m.compositeRowsLocked(composites, q)
	}

	return out, nil
}

// validateAlarmQuery rejects the filter combinations DescribeAlarms refuses.
func validateAlarmQuery(q *driver.AlarmQuery) error {
	if q.ChildrenOf == "" && q.ParentsOf == "" {
		return nil
	}

	if q.ChildrenOf != "" && q.ParentsOf != "" {
		return errors.New(errors.InvalidArgument, "ChildrenOfAlarmName and ParentsOfAlarmName cannot be used together")
	}

	// "you cannot specify any other parameters in the request except for
	// MaxRecords and NextToken" (DescribeAlarms), so AlarmTypes is refused too.
	if len(q.Names) > 0 || q.NamePrefix != "" || q.StateValue != "" || q.ActionPrefix != "" || len(q.AlarmTypes) > 0 {
		return errors.New(errors.InvalidArgument,
			"ChildrenOfAlarmName and ParentsOfAlarmName cannot be used with other filters except MaxRecords and NextToken")
	}

	return nil
}

// wantsType reports whether a query's AlarmTypes include want. An empty list
// means metric alarms only.
func wantsType(types []string, want string) bool {
	if len(types) == 0 {
		return want == driver.AlarmTypeMetric
	}

	for _, t := range types {
		if t == want {
			return true
		}
	}

	return false
}

// childrenOfLocked splits the alarms a composite's rule references into metric
// and composite names. An unknown composite has no children.
func (m *Mock) childrenOfLocked(name string) (metrics, composites []string) {
	c, ok := m.compositeAlarms.Get(name)
	if !ok {
		return nil, nil
	}

	for _, child := range compositeChildren(c) {
		switch {
		case m.alarms.Has(child):
			metrics = append(metrics, child)
		case m.compositeAlarms.Has(child):
			composites = append(composites, child)
		}
	}

	return metrics, composites
}

// parentsOfLocked returns the composites whose rule references name.
func (m *Mock) parentsOfLocked(name string) []string {
	var out []string

	for _, c := range m.compositeAlarms.All() {
		if hasChild(c, name) {
			out = append(out, c.Name)
		}
	}

	return out
}

// metricRowsLocked renders the named metric alarms that pass the filters.
// Children queries return only the name, ARN, state and state timestamp.
func (m *Mock) metricRowsLocked(names []string, q *driver.AlarmQuery) []driver.AlarmInfo {
	out := []driver.AlarmInfo{}

	for _, name := range names {
		a, ok := m.alarms.Get(name)
		if !ok || !matchesQuery(a.Name, a.State, q, a.AlarmActions, a.OKActions, a.InsufficientDataActions) {
			continue
		}

		if q.ChildrenOf != "" {
			out = append(out, driver.AlarmInfo{
				Name: a.Name, AlarmArn: a.AlarmArn, State: a.State, StateUpdatedTimestamp: a.StateUpdatedTimestamp,
			})

			continue
		}

		out = append(out, toAlarmInfo(a))
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// compositeRowsLocked renders the named composites that pass the filters.
// Children queries return the name, ARN, state and state timestamp, and
// parents queries only the name and ARN.
func (m *Mock) compositeRowsLocked(names []string, q *driver.AlarmQuery) []driver.CompositeAlarmInfo {
	out := []driver.CompositeAlarmInfo{}

	for _, name := range names {
		c, ok := m.compositeAlarms.Get(name)
		if !ok || !matchesQuery(c.Name, c.State, q, c.AlarmActions, c.OKActions, c.InsufficientDataActions) {
			continue
		}

		switch {
		case q.ChildrenOf != "":
			out = append(out, driver.CompositeAlarmInfo{
				Name: c.Name, ARN: c.ARN, State: c.State, StateUpdatedTimestamp: c.StateUpdatedTimestamp,
			})
		case q.ParentsOf != "":
			out = append(out, driver.CompositeAlarmInfo{Name: c.Name, ARN: c.ARN})
		default:
			out = append(out, toCompositeAlarmInfo(c))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// matchesQuery applies the name, prefix, state and action filters.
func matchesQuery(name, state string, q *driver.AlarmQuery, actions ...[]string) bool {
	if len(q.Names) > 0 && !containsString(q.Names, name) {
		return false
	}

	if q.NamePrefix != "" && !strings.HasPrefix(name, q.NamePrefix) {
		return false
	}

	if q.StateValue != "" && state != q.StateValue {
		return false
	}

	return q.ActionPrefix == "" || anyPrefixed(q.ActionPrefix, actions...)
}

// anyPrefixed reports whether any action starts with prefix.
func anyPrefixed(prefix string, actions ...[]string) bool {
	for _, list := range actions {
		for _, a := range list {
			if strings.HasPrefix(a, prefix) {
				return true
			}
		}
	}

	return false
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}

	return false
}
