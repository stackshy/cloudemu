package apigateway

import (
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

const (
	msgLoggingRole      = "CloudWatch Logs role ARN must be set in account settings to enable logging"
	msgAccessLogFormat  = "Log format must include either $context.requestId or $context.extendedRequestId"
	msgAccessLogARN     = "Invalid destination ARN: must be a CloudWatch Logs log group ARN"
	msgLogLevel         = "Invalid logging level: must be OFF, ERROR or INFO"
	msgMethodSetting    = "Invalid method setting patch path"
	msgCacheClusterSize = "Invalid cache cluster size"
	pathAccessLog       = "/accessLogSettings"
	fieldEnabled        = "enabled"
	fieldBurstLimit     = "burstLimit"
	fieldRateLimit      = "rateLimit"
)

// applyStageSettingsPatch handles the stage settings paths: tracing, cache
// cluster, access log settings and per-method settings ("/{resourcePath}/{method}/
// {section}/{field}", with ~1 escaping "/" in the resource path). handled is
// false for a path it does not own.
func applyStageSettingsPatch(st *driver.Stage, op driver.PatchOperation) (handled bool, err error) {
	switch {
	case op.Path == "/tracingEnabled":
		st.TracingEnabled = parseBool(op.Value)
	case op.Path == "/cacheClusterEnabled":
		st.CacheClusterEnabled = parseBool(op.Value)
	case op.Path == "/cacheClusterSize":
		st.CacheClusterSize = op.Value
	case op.Path == pathAccessLog && op.Op == opRemove:
		st.AccessLogSettings = nil
	case strings.HasPrefix(op.Path, pathAccessLog+"/"):
		applyAccessLogPatch(st, op)
	case strings.Count(op.Path, "/") >= methodSettingDepth:
		return true, applyMethodSettingPatch(st, op)
	case op.Op == opRemove && isMethodSettingPath(op.Path):
		return true, removeMethodSetting(st, op.Path)
	default:
		return false, nil
	}

	return true, nil
}

// minMethodSettingSegs is the segment count of "/{resourcePath}/{method}".
const minMethodSettingSegs = 2

// isMethodSettingPath reports a "/{resourcePath}/{method}" path (or "/*/*"): the
// whole setting of one method, which Terraform removes on delete.
func isMethodSettingPath(path string) bool {
	toks := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(toks) < minMethodSettingSegs {
		return false
	}

	method := toks[len(toks)-1]

	return method == "*" || validHTTPMethod(method)
}

// removeMethodSetting deletes one method's setting. As in the AWS provider's
// delete, a method with no setting is a BadRequestException.
func removeMethodSetting(st *driver.Stage, path string) error {
	toks := strings.Split(strings.TrimPrefix(path, "/"), "/")
	n := len(toks)
	key := methodSettingKey(unescapePointer(strings.Join(toks[:n-1], "/")), toks[n-1])

	if _, ok := st.MethodSettings[key]; !ok {
		return cerrors.New(cerrors.InvalidArgument, msgMethodSetting)
	}

	delete(st.MethodSettings, key)

	return nil
}

// methodSettingDepth is the slash count of "/{path}/{method}/{section}/{field}".
const methodSettingDepth = 4

func applyAccessLogPatch(st *driver.Stage, op driver.PatchOperation) {
	if st.AccessLogSettings == nil {
		st.AccessLogSettings = &driver.AccessLogSettings{}
	}

	switch strings.TrimPrefix(op.Path, pathAccessLog+"/") {
	case "destinationArn":
		st.AccessLogSettings.DestinationARN = patchRef(op)
	case "format":
		st.AccessLogSettings.Format = patchRef(op)
	}
}

func applyMethodSettingPatch(st *driver.Stage, op driver.PatchOperation) error {
	// Parse from the right: field, section, method, and whatever remains is the
	// resource path (a nested "pets/child" keeps its slashes, "~1" escapes them).
	toks := strings.Split(strings.TrimPrefix(op.Path, "/"), "/")
	if len(toks) < methodSettingDepth {
		return cerrors.New(cerrors.InvalidArgument, msgMethodSetting)
	}

	n := len(toks)
	key := methodSettingKey(unescapePointer(strings.Join(toks[:n-3], "/")), toks[n-3])

	if st.MethodSettings == nil {
		st.MethodSettings = map[string]*driver.MethodSetting{}
	}

	ms, ok := st.MethodSettings[key]
	if !ok {
		ms = &driver.MethodSetting{LoggingLevel: driver.LogLevelOff}
		st.MethodSettings[key] = ms
	}

	return setMethodSettingField(ms, toks[n-2]+"/"+toks[n-1], op.Value)
}

// methodSettingKey is the one canonical key of a method setting, used on write
// and on read: the resource path without its leading slash, then the method
// ("mock/GET", "pets/child/GET", "*/*").
func methodSettingKey(resourcePath, method string) string {
	return strings.TrimPrefix(resourcePath, "/") + "/" + method
}

func setMethodSettingField(ms *driver.MethodSetting, field, value string) error {
	section, name, _ := strings.Cut(field, "/")

	switch section {
	case "metrics":
		if name != fieldEnabled {
			return cerrors.New(cerrors.InvalidArgument, msgMethodSetting)
		}

		ms.MetricsEnabled = parseBool(value)
	case "logging":
		return setLoggingField(ms, name, value)
	case "caching":
		return setCachingField(ms, name, value)
	case "throttling":
		return setThrottlingField(ms, name, value)
	default:
		return cerrors.New(cerrors.InvalidArgument, msgMethodSetting)
	}

	return nil
}

func setLoggingField(ms *driver.MethodSetting, name, value string) error {
	switch name {
	case "loglevel":
		if value != driver.LogLevelOff && value != driver.LogLevelError && value != driver.LogLevelInfo {
			return cerrors.New(cerrors.InvalidArgument, msgLogLevel)
		}

		ms.LoggingLevel = value
	case "dataTrace":
		ms.DataTraceEnabled = parseBool(value)
	default:
		return cerrors.New(cerrors.InvalidArgument, msgMethodSetting)
	}

	return nil
}

func setCachingField(ms *driver.MethodSetting, name, value string) error {
	switch name {
	case fieldEnabled:
		ms.CachingEnabled = parseBool(value)
	case "ttlInSeconds":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return cerrors.New(cerrors.InvalidArgument, msgMethodSetting)
		}

		ms.CacheTTLInSeconds = n
	default:
		return cerrors.New(cerrors.InvalidArgument, msgMethodSetting)
	}

	return nil
}

func setThrottlingField(ms *driver.MethodSetting, name, value string) error {
	switch name {
	case fieldBurstLimit:
		n, err := strconv.Atoi(value)
		if err != nil || n < -1 {
			return cerrors.New(cerrors.InvalidArgument, msgMethodSetting)
		}

		ms.ThrottlingBurstLimit = n
	case fieldRateLimit:
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || f < -1 {
			return cerrors.New(cerrors.InvalidArgument, msgMethodSetting)
		}

		ms.ThrottlingRateLimit = f
	default:
		return cerrors.New(cerrors.InvalidArgument, msgMethodSetting)
	}

	return nil
}

// validateStageLogging checks the stage's logging configuration the way API
// Gateway does when it is saved: execution or access logging needs the account's
// CloudWatch Logs role, and an access log needs a log group destination and a
// format carrying a request id.
func validateStageLogging(st *driver.Stage, roleSet bool) error {
	if als := st.AccessLogSettings; als != nil {
		if !strings.Contains(als.DestinationARN, ":log-group:") {
			return cerrors.New(cerrors.InvalidArgument, msgAccessLogARN)
		}

		if !strings.Contains(als.Format, "$context.requestId") && !strings.Contains(als.Format, "$context.extendedRequestId") {
			return cerrors.New(cerrors.InvalidArgument, msgAccessLogFormat)
		}

		if !roleSet {
			return cerrors.New(cerrors.InvalidArgument, msgLoggingRole)
		}
	}

	for _, ms := range st.MethodSettings {
		if ms.LoggingLevel != "" && ms.LoggingLevel != driver.LogLevelOff && !roleSet {
			return cerrors.New(cerrors.InvalidArgument, msgLoggingRole)
		}
	}

	return nil
}
