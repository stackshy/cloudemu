package rds

import (
	"sort"
	"strings"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// Real RDS parameter groups always report the engine's full default parameter
// set (hundreds of entries) overlaid with any user modifications. Modeling every
// parameter is neither useful nor maintainable, so we seed a curated,
// representative set of the parameters IaC tooling and operators most commonly
// read/tune per engine family. DescribeDBParameters returns these
// engine-defaults merged with the group's user-modified values.

const (
	sourceEngineDefault = "engine-default"
	sourceUser          = "user"
	applyDynamic        = "dynamic"
	applyStatic         = "static"
	dataInteger         = "integer"
	dataString          = "string"
	dataBoolean         = "boolean"
	baseMySQL           = "mysql"
	basePostgres        = "postgres"
)

// engineDefaultParameters returns the curated engine-default parameters for a
// parameter-group family (e.g. "mysql8.0", "aurora-postgresql16"). Unknown
// families fall back to a small generic set so the result is never empty,
// matching real RDS which never returns an empty parameter list.
func engineDefaultParameters(family string) []rdsdriver.Parameter {
	switch familyBase(family) {
	case basePostgres:
		return append([]rdsdriver.Parameter(nil), postgresDefaults...)
	case baseMySQL:
		return append([]rdsdriver.Parameter(nil), mysqlDefaults...)
	default:
		return append([]rdsdriver.Parameter(nil), genericDefaults...)
	}
}

// familyBase reduces a parameter-group family to the base engine whose default
// set applies. Aurora and MariaDB share the MySQL/PostgreSQL parameter surface.
func familyBase(family string) string {
	switch {
	case strings.Contains(family, basePostgres):
		return basePostgres
	case strings.Contains(family, baseMySQL), strings.Contains(family, "mariadb"):
		return baseMySQL
	default:
		return ""
	}
}

//nolint:gochecknoglobals // static engine-default parameter table
var mysqlDefaults = []rdsdriver.Parameter{
	p("autocommit", "1", applyDynamic, dataBoolean, "The autocommit mode."),
	p("character_set_server", "latin1", applyDynamic, dataString, "The server's default character set."),
	p("collation_server", "latin1_swedish_ci", applyDynamic, dataString, "The server's default collation."),
	p("innodb_buffer_pool_size", "{DBInstanceClassMemory*3/4}", applyDynamic, dataInteger, "The size in bytes of the InnoDB buffer pool."),
	p("innodb_flush_log_at_trx_commit", "1", applyDynamic, dataInteger, "Controls InnoDB redo-log flush behavior on commit."),
	p("innodb_lock_wait_timeout", "50", applyDynamic, dataInteger, "InnoDB row-lock wait timeout in seconds."),
	p("log_bin_trust_function_creators", "0", applyDynamic, dataBoolean, "Relaxes binary-logging checks on stored functions."),
	p("long_query_time", "10", applyDynamic, dataString, "Slow-query threshold in seconds."),
	p("lower_case_table_names", "0", applyStatic, dataInteger, "Table-name case sensitivity."),
	p("max_allowed_packet", "4194304", applyDynamic, dataInteger, "Maximum size of one packet/row."),
	p("max_connections", "{DBInstanceClassMemory/12582880}", applyDynamic, dataInteger, "Maximum simultaneous client connections."),
	p("slow_query_log", "0", applyDynamic, dataBoolean, "Whether the slow query log is enabled."),
	p("sql_mode", "", applyDynamic, dataString, "The current SQL server mode."),
	p("time_zone", "UTC", applyDynamic, dataString, "The server time zone."),
}

//nolint:gochecknoglobals // static engine-default parameter table
var postgresDefaults = []rdsdriver.Parameter{
	p("autovacuum", "1", applyDynamic, dataBoolean, "Starts the autovacuum subprocess."),
	p("effective_cache_size", "{DBInstanceClassMemory/16384}", applyDynamic, dataInteger, "Planner estimate of total cache size."),
	p("idle_in_transaction_session_timeout", "86400000", applyDynamic, dataInteger, "Idle-in-transaction timeout (ms)."),
	p("log_min_duration_statement", "-1", applyDynamic, dataInteger, "Logs statements running at least this long (ms); -1 disables."),
	p("log_statement", "none", applyDynamic, dataString, "Sets the type of statements logged."),
	p("maintenance_work_mem", "65536", applyDynamic, dataInteger, "Maximum memory for maintenance operations (kB)."),
	p("max_connections", "LEAST({DBInstanceClassMemory/9531392},5000)", applyStatic, dataInteger, "Maximum concurrent connections."),
	p("max_wal_size", "2048", applyDynamic, dataInteger, "Sets the WAL size that triggers a checkpoint (MB)."),
	p("shared_buffers", "{DBInstanceClassMemory/32768}", applyStatic, dataInteger, "Number of shared memory buffers."),
	p("ssl", "1", applyStatic, dataBoolean, "Enables SSL connections."),
	p("statement_timeout", "0", applyDynamic, dataInteger, "Aborts any statement taking over this many ms; 0 disables."),
	p("timezone", "UTC", applyDynamic, dataString, "Sets the time zone for displaying and interpreting timestamps."),
	p("work_mem", "4096", applyDynamic, dataInteger, "Sets the maximum memory for query workspaces (kB)."),
}

//nolint:gochecknoglobals // static engine-default parameter table
var genericDefaults = []rdsdriver.Parameter{
	p("max_connections", "100", applyDynamic, dataInteger, "The maximum permitted number of simultaneous client connections."),
	p("time_zone", "UTC", applyDynamic, dataString, "The server time zone."),
}

// p builds an engine-default parameter (Source is always engine-default).
func p(name, value, applyType, dataType, desc string) rdsdriver.Parameter {
	return rdsdriver.Parameter{
		Name:        name,
		Value:       value,
		ApplyMethod: "pending-reboot",
		Source:      sourceEngineDefault,
		ApplyType:   applyType,
		DataType:    dataType,
		Description: desc,
	}
}

// mergedParameters overlays a group's user-modified parameters onto the engine
// defaults for its family. A modified parameter keeps the default's metadata but
// reports the user's value and Source=user; a user parameter with no matching
// default still appears. The result is sorted by name.
func mergedParameters(family string, userParams map[string]rdsdriver.Parameter) []rdsdriver.Parameter {
	byName := make(map[string]rdsdriver.Parameter)

	for _, d := range engineDefaultParameters(family) {
		byName[d.Name] = d
	}

	for name, up := range userParams {
		base, ok := byName[name]
		if !ok {
			base = rdsdriver.Parameter{Name: name, ApplyType: applyStatic, DataType: dataString}
		}

		base.Value = up.Value
		base.ApplyMethod = applyMethodOrDefault(up.ApplyMethod)
		base.Source = sourceUser
		byName[name] = base
	}

	out := make([]rdsdriver.Parameter, 0, len(byName))
	for _, param := range byName {
		out = append(out, param)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}
