package redshift

import rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"

// Redshift parameter apply types.
const (
	applyTypeStatic  = "static"
	applyTypeDynamic = "dynamic"
	sourceEngine     = "engine-default"
)

// redshiftDefaultParameter describes one seeded engine-default parameter.
type redshiftDefaultParameter struct {
	name      string
	value     string
	dataType  string
	applyType string
}

// redshiftDefaultParameterSpecs is the curated set of default parameters real
// Redshift exposes in a parameter group family (values from the AWS "Default
// parameter values" table). It doubles as the validation set: a
// ModifyClusterParameterGroup naming a parameter outside this set is rejected,
// matching Redshift's rejection of parameters not in the family.
//
//nolint:gochecknoglobals // static default-parameter catalog.
var redshiftDefaultParameterSpecs = []redshiftDefaultParameter{
	{"auto_analyze", "true", "boolean", applyTypeDynamic},
	{"auto_mv", "true", "boolean", applyTypeDynamic},
	{"datestyle", "ISO, MDY", "string", applyTypeDynamic},
	{"enable_case_sensitive_identifier", "false", "boolean", applyTypeDynamic},
	{"enable_user_activity_logging", "false", "boolean", applyTypeStatic},
	{"extra_float_digits", "0", "integer", applyTypeDynamic},
	{"max_concurrency_scaling_clusters", "1", "integer", applyTypeDynamic},
	{"max_cursor_result_set_size", "0", "integer", applyTypeDynamic},
	{"query_group", "default", "string", applyTypeDynamic},
	{"require_ssl", "false", "boolean", applyTypeStatic},
	{"search_path", "$user, public", "string", applyTypeDynamic},
	{"statement_timeout", "0", "integer", applyTypeDynamic},
	{"use_fips_ssl", "false", "boolean", applyTypeStatic},
	{"wlm_json_configuration", `[{"auto_wlm":true}]`, "string", applyTypeDynamic},
}

// defaultRedshiftParameters returns a fresh copy of the seeded engine-default
// parameters, keyed by name. Each call returns a new map so a group never
// shares mutable parameter state with another.
func defaultRedshiftParameters() map[string]rdbdriver.Parameter {
	out := make(map[string]rdbdriver.Parameter, len(redshiftDefaultParameterSpecs))
	for _, p := range redshiftDefaultParameterSpecs {
		out[p.name] = rdbdriver.Parameter{
			Name:      p.name,
			Value:     p.value,
			Source:    sourceEngine,
			DataType:  p.dataType,
			ApplyType: p.applyType,
		}
	}

	return out
}
