package apigateway

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// AWS/ApiGateway REST API request metrics. Count/4XXError/5XXError are Count
// (0 or 1 per request, so Average is the error rate); Latency and
// IntegrationLatency are Milliseconds. Each request is published on both the
// {ApiName} and the {ApiName, Stage} dimension sets, as real API Gateway does.
const (
	metricsNamespace = "AWS/ApiGateway"

	unitCount        = "Count"
	unitMilliseconds = "Milliseconds"

	status4xxMin = 400
	status5xxMin = 500
	status5xxMax = 600
)

// SetMonitoring wires the CloudWatch backend that receives the AWS/ApiGateway
// request metrics. Nil-safe: with no backend wired nothing is published.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// stageAPIName returns the API's name when req targets an existing API and
// stage — the requests real API Gateway attributes metrics to.
func (m *Mock) stageAPIName(req *driver.ProxyRequest) (string, bool) {
	ad, err := m.getAPI(req.RestAPIID)
	if err != nil {
		return "", false
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	if _, ok := ad.stages[req.StageName]; !ok {
		return "", false
	}

	// API Gateway uses the API id as ApiName when the name has no ASCII.
	if ad.api.Name == "" {
		return req.RestAPIID, true
	}

	return ad.api.Name, true
}

func millis(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func flag(b bool) float64 {
	if b {
		return 1
	}

	return 0
}

// emitRequestMetrics publishes one data-plane request. integration is the
// backend latency, or negative when the request never reached a backend (no
// IntegrationLatency is published then).
func (m *Mock) emitRequestMetrics(
	ctx context.Context, apiName, stage string, status int, latency, integration time.Duration,
) {
	if m.monitoring == nil {
		return
	}

	values := []mondriver.MetricDatum{
		{MetricName: "Count", Value: 1, Unit: unitCount},
		{MetricName: "4XXError", Value: flag(status >= status4xxMin && status < status5xxMin), Unit: unitCount},
		{MetricName: "5XXError", Value: flag(status >= status5xxMin && status < status5xxMax), Unit: unitCount},
		{MetricName: "Latency", Value: millis(latency), Unit: unitMilliseconds},
	}

	if integration >= 0 {
		values = append(values, mondriver.MetricDatum{
			MetricName: "IntegrationLatency", Value: millis(integration), Unit: unitMilliseconds,
		})
	}

	now := m.opts.Clock.Now()
	dimSets := []map[string]string{
		{"ApiName": apiName},
		{"ApiName": apiName, "Stage": stage},
	}

	data := make([]mondriver.MetricDatum, 0, len(values)*len(dimSets))

	for _, dims := range dimSets {
		for i := range values {
			v := values[i]
			v.Namespace = metricsNamespace
			v.Dimensions = dims
			v.Timestamp = now
			data = append(data, v)
		}
	}

	_ = m.monitoring.PutMetricData(ctx, data)
}
