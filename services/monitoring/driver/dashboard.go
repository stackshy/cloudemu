package driver

import "time"

// DashboardInfo is a stored CloudWatch dashboard returned by GetDashboard: its
// name, the JSON DashboardBody, its ARN, last-modified time, and the body size
// in bytes.
type DashboardInfo struct {
	Name         string
	Body         string
	ARN          string
	LastModified time.Time
	Size         int
}

// DashboardEntry is a ListDashboards summary row: the dashboard's name, ARN,
// last-modified time, and body size, without the body itself.
type DashboardEntry struct {
	Name         string
	ARN          string
	LastModified time.Time
	Size         int
}

// CompositeAlarmConfig describes a composite alarm to create or update. The
// AlarmRule is a boolean expression over other alarms' states (e.g.
// `ALARM("cpu-high") OR ALARM("mem-high")`).
type CompositeAlarmConfig struct {
	Name                    string
	AlarmRule               string
	AlarmDescription        string
	ActionsEnabled          *bool // nil defaults to true (AWS semantics)
	AlarmActions            []string
	OKActions               []string
	InsufficientDataActions []string
	Tags                    map[string]string
	// ActionsSuppressor is the name or ARN of the alarm whose ALARM state
	// holds back this alarm's actions. Both periods are required with it.
	ActionsSuppressor                string
	ActionsSuppressorWaitPeriod      *int
	ActionsSuppressorExtensionPeriod *int
}

// CompositeAlarmInfo describes a stored composite alarm.
type CompositeAlarmInfo struct {
	Name                    string
	ARN                     string
	AlarmRule               string
	AlarmDescription        string
	State                   string // "OK", "ALARM", "INSUFFICIENT_DATA"
	StateReason             string
	StateUpdatedTimestamp   time.Time
	ActionsEnabled          bool
	AlarmActions            []string
	OKActions               []string
	InsufficientDataActions []string
	StateReasonData         string
	// StateTransitionedTimestamp is when State last changed.
	StateTransitionedTimestamp         time.Time
	AlarmConfigurationUpdatedTimestamp time.Time
	ActionsSuppressor                  string
	ActionsSuppressorWaitPeriod        int
	ActionsSuppressorExtensionPeriod   int
	// ActionsSuppressedBy is WaitPeriod, ExtensionPeriod or Alarm while
	// actions are held back, and empty otherwise.
	ActionsSuppressedBy     string
	ActionsSuppressedReason string
}

// Alarm types of a DescribeAlarms query.
const (
	AlarmTypeMetric    = "MetricAlarm"
	AlarmTypeComposite = "CompositeAlarm"
)

// AlarmQuery is the filter set of a DescribeAlarms call. AlarmTypes empty
// means metric alarms only, as on AWS. ChildrenOf and ParentsOf cannot be
// combined with each other or with the name, state and action filters.
type AlarmQuery struct {
	Names        []string
	NamePrefix   string
	StateValue   string
	ActionPrefix string
	AlarmTypes   []string
	ChildrenOf   string
	ParentsOf    string
}

// AlarmQueryResult holds the metric and composite alarms a query matched,
// each sorted by name.
type AlarmQueryResult struct {
	MetricAlarms    []AlarmInfo
	CompositeAlarms []CompositeAlarmInfo
}
