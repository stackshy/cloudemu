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

// DashboardEntry is a ListDashboards summary row — the dashboard's name, ARN,
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
}
