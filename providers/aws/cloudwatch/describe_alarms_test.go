package cloudwatch

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

func queryNames(res *driver.AlarmQueryResult) (metric, composite string) {
	var m, c []string

	for i := range res.MetricAlarms {
		m = append(m, res.MetricAlarms[i].Name)
	}

	for i := range res.CompositeAlarms {
		c = append(c, res.CompositeAlarms[i].Name)
	}

	return strings.Join(m, ","), strings.Join(c, ",")
}

// DescribeAlarms returns metric alarms only when AlarmTypes is omitted, and
// honours ChildrenOfAlarmName and ParentsOfAlarmName. Before, the wire sent
// both types and ignored the two filters.
func TestQueryAlarms(t *testing.T) {
	ctx := context.Background()
	m, _, _ := newClockMock()
	requireNoError(t, m.CreateAlarm(ctx, childAlarm("a")))
	requireNoError(t, m.CreateAlarm(ctx, childAlarm("b")))
	putComposite(t, m, "mid", "ALARM(a)")
	putComposite(t, m, "top", "ALARM(mid) OR ALARM(b)")

	both := []string{driver.AlarmTypeMetric, driver.AlarmTypeComposite}
	compositeOnly := []string{driver.AlarmTypeComposite}

	cases := []struct {
		name              string
		q                 driver.AlarmQuery
		metric, composite string
	}{
		{"types omitted", driver.AlarmQuery{}, "a,b", ""},
		{"composite only", driver.AlarmQuery{AlarmTypes: compositeOnly}, "", "mid,top"},
		// Children and parents take no AlarmTypes and return both types.
		{"children", driver.AlarmQuery{ChildrenOf: "top"}, "b", "mid"},
		{"parents", driver.AlarmQuery{ParentsOf: "a"}, "", "mid"},
		{"parents of composite", driver.AlarmQuery{ParentsOf: "mid"}, "", "top"},
		{"prefix", driver.AlarmQuery{NamePrefix: "t", AlarmTypes: both}, "", "top"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := m.QueryAlarms(ctx, &tc.q)
			requireNoError(t, err)

			metric, comp := queryNames(res)
			assertEqual(t, tc.metric, metric)
			assertEqual(t, tc.composite, comp)
		})
	}

	// A parents query returns only the name and ARN.
	res, err := m.QueryAlarms(ctx, &driver.AlarmQuery{ParentsOf: "a"})
	requireNoError(t, err)
	assertEqual(t, "", res.CompositeAlarms[0].AlarmRule)
	assertEqual(t, "arn:aws:cloudwatch:us-east-1:123456789012:alarm:mid", res.CompositeAlarms[0].ARN)

	for _, q := range []driver.AlarmQuery{
		{ChildrenOf: "top", ParentsOf: "a"},
		{ChildrenOf: "top", StateValue: stateOK},
		{ParentsOf: "a", Names: []string{"mid"}},
		{ParentsOf: "a", AlarmTypes: compositeOnly},
		{ChildrenOf: "top", AlarmTypes: both},
	} {
		if _, err := m.QueryAlarms(ctx, &q); !errors.IsInvalidArgument(err) {
			t.Fatalf("%+v: want InvalidArgument, got %v", q, err)
		}
	}
}
