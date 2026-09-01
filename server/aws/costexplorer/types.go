package costexplorer

// Granularity values a Cost Explorer request may ask for.
const (
	granularityHourly  = "HOURLY"
	granularityDaily   = "DAILY"
	granularityMonthly = "MONTHLY"
)

// hoursPerMonth is AWS's billing convention (730 h/month). The estimator yields
// a monthly figure; per-bucket amounts derive from it at this rate so HOURLY,
// DAILY and MONTHLY granularities stay mutually consistent.
const hoursPerMonth = 730.0

// costMetrics are the request metrics that carry a dollar amount. Any other
// requested metric (UsageQuantity, NormalizedUsageAmount) is a usage metric —
// the emulator does not track metered usage, so it reports zero with unit N/A.
var costMetrics = map[string]bool{ //nolint:gochecknoglobals // static lookup table
	"UnblendedCost":    true,
	"BlendedCost":      true,
	"AmortizedCost":    true,
	"NetUnblendedCost": true,
	"NetAmortizedCost": true,
}

// serviceDisplayNames maps a resource-discovery service token to the Cost
// Explorer SERVICE dimension display name AWS returns. Unmapped tokens fall
// through unchanged (see displayName).
var serviceDisplayNames = map[string]string{ //nolint:gochecknoglobals // static lookup table
	"compute":      "Amazon Elastic Compute Cloud - Compute",
	"storage":      "Amazon Simple Storage Service",
	"database":     "Amazon DynamoDB",
	"relationaldb": "Amazon Relational Database Service",
	"serverless":   "AWS Lambda",
	"cache":        "Amazon ElastiCache",
	"loadbalancer": "Elastic Load Balancing",
	"kubernetes":   "Amazon Elastic Container Service for Kubernetes",
	"networking":   "Amazon Virtual Private Cloud",
}

// displayName returns the Cost Explorer SERVICE display name for a
// resource-discovery service token, or the token unchanged when unmapped.
func displayName(token string) string {
	if name, ok := serviceDisplayNames[token]; ok {
		return name
	}

	return token
}

// dateInterval mirrors the Cost Explorer DateInterval (Start inclusive, End
// exclusive), serialized/parsed as the wire strings the SDK sends.
type dateInterval struct {
	Start string `json:"Start"`
	End   string `json:"End"`
}

// groupDefinition mirrors the Cost Explorer GroupDefinition.
type groupDefinition struct {
	Type string `json:"Type"`
	Key  string `json:"Key"`
}

// metricValue mirrors the Cost Explorer MetricValue.
type metricValue struct {
	Amount string `json:"Amount"`
	Unit   string `json:"Unit"`
}

// group mirrors the Cost Explorer Group (a GroupBy bucket within a period).
type group struct {
	Keys    []string               `json:"Keys"`
	Metrics map[string]metricValue `json:"Metrics"`
}

// resultByTime mirrors the Cost Explorer ResultByTime.
type resultByTime struct {
	TimePeriod dateInterval           `json:"TimePeriod"`
	Total      map[string]metricValue `json:"Total"`
	Groups     []group                `json:"Groups"`
	Estimated  bool                   `json:"Estimated"`
}

// getCostAndUsageInput mirrors the members of the SDK GetCostAndUsageInput this
// handler reads.
type getCostAndUsageInput struct {
	TimePeriod  *dateInterval     `json:"TimePeriod"`
	Granularity string            `json:"Granularity"`
	Metrics     []string          `json:"Metrics"`
	GroupBy     []groupDefinition `json:"GroupBy"`
}

// getCostAndUsageOutput mirrors the members of the SDK GetCostAndUsageOutput
// this handler populates.
type getCostAndUsageOutput struct {
	GroupDefinitions []groupDefinition `json:"GroupDefinitions,omitempty"`
	ResultsByTime    []resultByTime    `json:"ResultsByTime"`
}

// getCostForecastInput mirrors the members of the SDK GetCostForecastInput this
// handler reads.
type getCostForecastInput struct {
	TimePeriod  *dateInterval `json:"TimePeriod"`
	Metric      string        `json:"Metric"`
	Granularity string        `json:"Granularity"`
}

// forecastResult mirrors the Cost Explorer ForecastResult.
type forecastResult struct {
	TimePeriod dateInterval `json:"TimePeriod"`
	MeanValue  string       `json:"MeanValue"`
}

// getCostForecastOutput mirrors the SDK GetCostForecastOutput.
type getCostForecastOutput struct {
	Total                 metricValue      `json:"Total"`
	ForecastResultsByTime []forecastResult `json:"ForecastResultsByTime"`
}

// getDimensionValuesInput mirrors the members of the SDK
// GetDimensionValuesInput this handler reads.
type getDimensionValuesInput struct {
	Dimension  string        `json:"Dimension"`
	TimePeriod *dateInterval `json:"TimePeriod"`
}

// dimensionValuesWithAttributes mirrors the Cost Explorer
// DimensionValuesWithAttributes.
type dimensionValuesWithAttributes struct {
	Value      string            `json:"Value"`
	Attributes map[string]string `json:"Attributes,omitempty"`
}

// getDimensionValuesOutput mirrors the SDK GetDimensionValuesOutput.
type getDimensionValuesOutput struct {
	DimensionValues []dimensionValuesWithAttributes `json:"DimensionValues"`
	ReturnSize      int                             `json:"ReturnSize"`
	TotalSize       int                             `json:"TotalSize"`
}

// getTagsInput mirrors the members of the SDK GetTagsInput this handler reads.
type getTagsInput struct {
	TimePeriod *dateInterval `json:"TimePeriod"`
	TagKey     *string       `json:"TagKey"`
}

// getTagsOutput mirrors the SDK GetTagsOutput.
type getTagsOutput struct {
	Tags       []string `json:"Tags"`
	ReturnSize int      `json:"ReturnSize"`
	TotalSize  int      `json:"TotalSize"`
}
