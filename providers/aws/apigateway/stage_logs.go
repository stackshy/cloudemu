package apigateway

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// SetLogSink wires CloudWatch Logs so stages with execution or access logging
// enabled write their log lines there. Nil-safe: with no sink nothing is logged.
func (m *Mock) SetLogSink(l logdriver.Logging) { m.logs = l }

// executionLogGroup is the log group a stage's execution logs go to.
func executionLogGroup(apiID, stage string) string {
	return "API-Gateway-Execution-Logs_" + apiID + "/" + stage
}

// execLog collects the execution log lines of one request and writes them to
// CloudWatch Logs when the request finishes. Lines are gated by the stage's
// logging level for the method: INFO keeps everything, ERROR only failures.
type execLog struct {
	m     *Mock
	route *resolvedRoute
	reqID string
	level string
	trace bool
	lines []string
}

func (m *Mock) newExecLog(route *resolvedRoute, req *driver.ProxyRequest, reqID string) *execLog {
	lg := &execLog{m: m, route: route, reqID: reqID, level: driver.LogLevelOff}

	if ms := methodSetting(&route.stage, route); ms != nil && m.logs != nil {
		lg.level, lg.trace = orDefault(ms.LoggingLevel, driver.LogLevelOff), ms.DataTraceEnabled
	}

	lg.infof("Extended Request Id: %s", reqID)
	lg.infof("Method request path: %s", fmt.Sprint(route.pathParameters))
	lg.infof("Method request query string: %s", fmt.Sprint(req.Query))

	if lg.trace {
		lg.infof("Method request body before transformations: %s", req.Body)
	}

	return lg
}

func (l *execLog) infof(format string, args ...any) {
	if l != nil && l.level == driver.LogLevelInfo {
		l.lines = append(l.lines, "("+l.reqID+") "+fmt.Sprintf(format, args...))
	}
}

func (l *execLog) errorf(format string, args ...any) {
	if l != nil && (l.level == driver.LogLevelInfo || l.level == driver.LogLevelError) {
		l.lines = append(l.lines, "("+l.reqID+") "+fmt.Sprintf(format, args...))
	}
}

// finish records the outcome and flushes the lines to the stage's log group.
func (l *execLog) finish(ctx context.Context, status int) {
	if l == nil || l.level == driver.LogLevelOff || l.m.logs == nil {
		return
	}

	l.infof("Method completed with status: %d", status)

	if len(l.lines) == 0 {
		return
	}

	group := executionLogGroup(l.route.apiID, l.route.stage.StageName)
	stream := l.m.opts.Clock.Now().UTC().Format("2006/01/02") + "/" + l.route.apiID

	l.m.putLines(ctx, group, stream, l.lines)
}

// putLines creates the group and stream on first use and appends the lines.
func (m *Mock) putLines(ctx context.Context, group, stream string, lines []string) {
	if m.logs == nil {
		return
	}

	_, _ = m.logs.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: group})
	_, _ = m.logs.CreateLogStream(ctx, group, stream)

	now := m.opts.Clock.Now()
	events := make([]logdriver.LogEvent, len(lines))

	for i, ln := range lines {
		events[i] = logdriver.LogEvent{Timestamp: now, Message: ln}
	}

	_ = m.logs.PutLogEvents(ctx, group, stream, events)
}

// accessLogGroup extracts the log group name from a CloudWatch Logs ARN
// (arn:aws:logs:region:account:log-group:NAME[:*]).
func accessLogGroup(arn string) string {
	_, name, ok := strings.Cut(arn, ":log-group:")
	if !ok {
		return ""
	}

	return strings.TrimSuffix(name, ":*")
}

// writeAccessLog writes one access log line, formatted by the stage's
// accessLogSettings.format, to its destination log group.
func (m *Mock) writeAccessLog(
	ctx context.Context, req *driver.ProxyRequest, route *resolvedRoute, resp *driver.ProxyResponse, reqID string, latency time.Duration,
) {
	als := route.stage.AccessLogSettings
	if als == nil || m.logs == nil || als.Format == "" {
		return
	}

	group := accessLogGroup(als.DestinationARN)
	if group == "" {
		return
	}

	line := strings.NewReplacer(
		"$context.requestId", reqID, "$context.extendedRequestId", reqID,
		"$context.identity.sourceIp", req.SourceIP, "$context.httpMethod", req.HTTPMethod,
		"$context.path", "/"+req.StageName+req.Path, "$context.resourcePath", route.resourcePath,
		"$context.status", strconv.Itoa(resp.StatusCode), "$context.protocol", orDefault(req.Protocol, "HTTP/1.1"),
		"$context.stage", req.StageName, "$context.apiId", route.apiID,
		"$context.responseLength", strconv.Itoa(len(resp.Body)),
		"$context.requestTime", m.opts.Clock.Now().UTC().Format(requestTimeLayout),
		"$context.responseLatency", strconv.FormatInt(latency.Milliseconds(), 10),
		"$context.domainName", req.Host, "$context.accountId", m.opts.AccountID,
	).Replace(als.Format)

	m.putLines(ctx, group, route.apiID+"-"+req.StageName, []string{line})
}

// emitMethodMetrics publishes the detailed per-method metrics when the stage
// enables them for the method.
func (m *Mock) emitMethodMetrics(
	ctx context.Context, req *driver.ProxyRequest, route *resolvedRoute, status int, latency, integration time.Duration,
) {
	ms := methodSetting(&route.stage, route)
	if m.monitoring == nil || ms == nil || !ms.MetricsEnabled {
		return
	}

	dims := map[string]string{
		"ApiName": route.apiName, "Resource": route.resourcePath, "Method": route.method.HTTPMethod, "Stage": req.StageName,
	}

	data := []mondriver.MetricDatum{
		{MetricName: "Count", Value: 1, Unit: unitCount},
		{MetricName: "4XXError", Value: flag(status >= status4xxMin && status < status5xxMin), Unit: unitCount},
		{MetricName: "5XXError", Value: flag(status >= status5xxMin && status < status5xxMax), Unit: unitCount},
		{MetricName: "Latency", Value: millis(latency), Unit: unitMilliseconds},
	}

	if integration >= 0 {
		data = append(data, mondriver.MetricDatum{MetricName: "IntegrationLatency", Value: millis(integration), Unit: unitMilliseconds})
	}

	now := m.opts.Clock.Now()

	for i := range data {
		data[i].Namespace, data[i].Dimensions, data[i].Timestamp = metricsNamespace, dims, now
	}

	_ = m.monitoring.PutMetricData(ctx, data)
}
