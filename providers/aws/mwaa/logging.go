package mwaa

import "encoding/json"

// defaultLogLevel is the log level reported for a log type the caller did not
// configure. It is a computed default, so it never drifts a Terraform plan.
const defaultLogLevel = "INFO"

// logType names an Apache Airflow log module and the log-group name suffix MWAA
// uses for it. The key is the field name under LoggingConfiguration; the suffix
// is the airflow-{env}-{suffix} log-group segment.
type logType struct {
	key    string
	suffix string
}

// logTypes is the fixed set of Apache Airflow log modules, in the wire order
// MWAA reports them. Every environment's LoggingConfiguration reports all five.
//
//nolint:gochecknoglobals // immutable descriptor set, read-only after init.
var logTypes = []logType{
	{"DagProcessingLogs", "DAGProcessing"},
	{"SchedulerLogs", "Scheduler"},
	{"TaskLogs", "Task"},
	{"WebserverLogs", "WebServer"},
	{"WorkerLogs", "Worker"},
}

// logInput is the modeled subset of one log-type block a caller can send.
type logInput struct {
	Enabled  *bool  `json:"Enabled"`
	LogLevel string `json:"LogLevel"`
}

// augmentLogging normalizes a caller's LoggingConfiguration into the full,
// five-module block MWAA reports, injecting the stable per-module
// CloudWatchLogGroupArn for every enabled module. A module the caller omitted is
// reported disabled with the default log level. The result is deterministic in
// the environment name, so it is stable across every read and after an update.
func (m *Mock) augmentLogging(name string, raw json.RawMessage) json.RawMessage {
	in := map[string]logInput{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &in)
	}

	out := make(map[string]any, len(logTypes))

	for _, lt := range logTypes {
		cfg := in[lt.key]

		enabled := false
		if cfg.Enabled != nil {
			enabled = *cfg.Enabled
		}

		level := cfg.LogLevel
		if level == "" {
			level = defaultLogLevel
		}

		block := map[string]any{
			"Enabled":  enabled,
			"LogLevel": level,
		}
		if enabled {
			block["CloudWatchLogGroupArn"] = m.logGroupARN(name, lt.suffix)
		}

		out[lt.key] = block
	}

	b, _ := json.Marshal(out)

	return b
}
