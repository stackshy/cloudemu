package driver

import "context"

// IpamMetricNamespace is the CloudWatch namespace IPAM publishes to.
const IpamMetricNamespace = "AWS/IPAM"

// IpamMetric is one AWS/IPAM CloudWatch datapoint derived from IPAM state.
// It is deliberately neutral (no dependency on the monitoring driver) so the
// CloudWatch server can adapt it without coupling networking to monitoring.
type IpamMetric struct {
	Namespace  string
	MetricName string
	Value      float64
	Unit       string
	Dimensions map[string]string
}

// IPAMMetrics is an OPTIONAL capability that exposes the AWS/IPAM CloudWatch
// metrics (IPAM/pool/scope/public-IP + resource-utilization) derived from the
// current IPAM and VPC state. The CloudWatch handler surfaces these under the
// AWS/IPAM namespace via ListMetrics/GetMetricData.
type IPAMMetrics interface {
	IpamMetrics(ctx context.Context) []IpamMetric
}
