package costmanagement

import (
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/azure/resourcegraph"
	"github.com/stackshy/cloudemu/v2/services/cost"
	"github.com/stackshy/cloudemu/v2/services/pricing"
)

// keySep separates the per-dimension values of a composite grouping key. It is
// a control byte that cannot occur in any dimension value.
const keySep = "\x00"

// trailingSlots is the capacity headroom a column/row slice reserves for the
// two trailing entries every layout may carry: the optional granularity date
// column and the always-present Currency column.
const trailingSlots = 2

// Granularity tokens the request may carry (lower-cased on read).
const (
	granularityDaily   = "daily"
	granularityMonthly = "monthly"
)

// hoursPerDay pro-rates a monthly estimate down to one day together with
// pricing.HoursPerMonth (the shared 730-hours-per-month convention AWS Cost
// Explorer's perBucketRate also uses), so a Daily query's per-day rows sum to
// ~= the monthly figure both FinOps surfaces report and the two can't drift.
const hoursPerDay = 24.0

// shape turns the priced cost lines into the Cost Management columns/rows for
// the requested granularity and grouping. Column order follows the real API:
// the cost aggregation column(s) first, then one column per grouping dimension,
// then the granularity date column (if any), then Currency last; every row is
// ordered to match.
//
// Granularity drives how the estate's monthly cost is spread over the query's
// time period, mirroring AWS Cost Explorer: Daily emits one row (per group) per
// day in the period, each pro-rated to a per-day rate; Monthly emits one row per
// group at the full monthly figure; an absent/None granularity emits a single
// aggregate over the period.
func shape(def *queryDefinition, lines []cost.Line) (columns []column, rows [][]any) {
	costCols := costColumnNames(def)
	groupNames := groupingNames(def)
	gran := strings.ToLower(def.Dataset.Granularity)

	dateCol, hasDate := granularityColumn(gran)
	columns = buildColumns(costCols, dateCol, hasDate, groupNames)

	switch gran {
	case granularityDaily:
		rows = dailyRows(def, costCols, groupNames, lines)
	case granularityMonthly:
		rows = bucketRows(costCols, groupNames, lines, monthlyDateValue(), true, 1.0)
	default:
		rows = bucketRows(costCols, groupNames, lines, nil, false, 1.0)
	}

	return columns, rows
}

// buildColumns assembles the column descriptors in wire order.
func buildColumns(costCols []string, dateCol column, hasDate bool, groupNames []string) []column {
	columns := make([]column, 0, len(costCols)+len(groupNames)+trailingSlots)

	for _, name := range costCols {
		columns = append(columns, column{Name: name, Type: "Number"})
	}

	for _, name := range groupNames {
		columns = append(columns, column{Name: name, Type: "String"})
	}

	if hasDate {
		columns = append(columns, dateCol)
	}

	return append(columns, column{Name: "Currency", Type: "String"})
}

// dailyRows spreads the estate over each day in the query's time period, one row
// (per group) per day carrying the per-day pro-rated cost stamped with that
// day's yyyymmdd UsageDate. An empty estate yields no rows, matching the
// aggregate path.
func dailyRows(def *queryDefinition, costCols, groupNames []string, lines []cost.Line) [][]any {
	if len(lines) == 0 {
		return [][]any{}
	}

	start, end := queryDateRange(def)
	scale := hoursPerDay / float64(pricing.HoursPerMonth)

	var rows [][]any

	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		ymd := d.Year()*10000 + int(d.Month())*100 + d.Day()
		rows = append(rows, bucketRows(costCols, groupNames, lines, ymd, true, scale)...)
	}

	return rows
}

// bucketRows shapes the rows for a single time bucket (a day, a month, or the
// whole period), scaling each cost by scale and stamping the given date value.
// It dispatches to the ungrouped aggregate or the grouped-by-dimension layout.
func bucketRows(costCols, groupNames []string, lines []cost.Line, dateVal any, hasDate bool, scale float64) [][]any {
	if len(groupNames) == 0 {
		return ungroupedRows(costCols, lines, dateVal, hasDate, scale)
	}

	return groupedRows(costCols, groupNames, lines, dateVal, hasDate, scale)
}

// ungroupedRows returns the single aggregate row (or none, when the estate is
// empty) for a query with no grouping dimensions, scaled to the bucket rate.
func ungroupedRows(costCols []string, lines []cost.Line, dateVal any, hasDate bool, scale float64) [][]any {
	if len(lines) == 0 {
		return [][]any{}
	}

	var total float64
	for i := range lines {
		total += lines[i].MonthlyUSD
	}

	return [][]any{buildRow(costCols, total*scale, dateVal, hasDate, nil)}
}

// groupedRows buckets the lines by the composite grouping key and returns one
// row per bucket, scaled to the bucket rate, sorted for deterministic output.
func groupedRows(costCols, groupNames []string, lines []cost.Line, dateVal any, hasDate bool, scale float64) [][]any {
	type bucket struct {
		dims  []string
		total float64
	}

	buckets := map[string]*bucket{}

	for i := range lines {
		dims := make([]string, len(groupNames))
		for j, name := range groupNames {
			dims[j] = groupValue(name, &lines[i])
		}

		key := strings.Join(dims, keySep)
		if b := buckets[key]; b != nil {
			b.total += lines[i].MonthlyUSD
		} else {
			buckets[key] = &bucket{dims: dims, total: lines[i].MonthlyUSD}
		}
	}

	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	rows := make([][]any, 0, len(keys))

	for _, k := range keys {
		b := buckets[k]
		rows = append(rows, buildRow(costCols, b.total*scale, dateVal, hasDate, b.dims))
	}

	return rows
}

// buildRow lays out one row: the cost value repeated for each cost column, the
// grouping dimension values, the date value (if the query is granular), and the
// currency last — matching buildColumns.
func buildRow(costCols []string, total, dateVal any, hasDate bool, dims []string) []any {
	row := make([]any, 0, len(costCols)+len(dims)+trailingSlots)

	for range costCols {
		row = append(row, total)
	}

	for _, d := range dims {
		row = append(row, d)
	}

	if hasDate {
		row = append(row, dateVal)
	}

	return append(row, currency)
}

// costColumnNames resolves the response column name(s) for the cost
// aggregation. Real Cost Management names each cost column after the
// aggregation's own column name (e.g. {name:"PreTaxCost"} -> "PreTaxCost"), not
// the dictionary alias; aliases are sorted so the order is deterministic. A
// request with no aggregation gets a single default "Cost" column.
func costColumnNames(def *queryDefinition) []string {
	aggs := def.Dataset.Aggregation
	if len(aggs) == 0 {
		return []string{defaultCostColumn}
	}

	aliases := make([]string, 0, len(aggs))
	for alias := range aggs {
		aliases = append(aliases, alias)
	}

	sort.Strings(aliases)

	names := make([]string, 0, len(aliases))

	for _, alias := range aliases {
		name := aggs[alias].Name
		if name == "" {
			name = defaultCostColumn
		}

		names = append(names, name)
	}

	return names
}

// groupingNames returns the requested grouping dimension column names in order.
func groupingNames(def *queryDefinition) []string {
	if len(def.Dataset.Grouping) == 0 {
		return nil
	}

	names := make([]string, 0, len(def.Dataset.Grouping))

	for _, g := range def.Dataset.Grouping {
		if g.Name != "" {
			names = append(names, g.Name)
		}
	}

	return names
}

// granularityColumn returns the date column for a granularity and whether the
// query is granular at all. Daily buckets carry a numeric yyyymmdd UsageDate (as
// real Cost Management does); Monthly carries a BillingMonth timestamp.
// "None"/absent granularity has no date column — a single aggregate over the
// period.
func granularityColumn(gran string) (column, bool) {
	switch gran {
	case granularityDaily:
		return column{Name: "UsageDate", Type: "Number"}, true
	case granularityMonthly:
		return column{Name: "BillingMonth", Type: "Datetime"}, true
	default:
		return column{}, false
	}
}

// monthlyDateValue is the BillingMonth value for a Monthly query: the first of
// the current month, in the timestamp form real Cost Management emits.
func monthlyDateValue() any {
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	return month.Format("2006-01-02T15:04:05")
}

// queryDateRange resolves the inclusive [start, end] day range a granular query
// spans from its timeframe. MonthToDate (the default) runs from the first of the
// current month to today; TheLastMonth spans the whole previous calendar month;
// Custom uses the request's explicit time period. An unrecognized or malformed
// timeframe falls back to month-to-date, so a granular query always yields at
// least one day.
func queryDateRange(def *queryDefinition) (start, end time.Time) {
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	switch strings.ToLower(def.Timeframe) {
	case "custom":
		if from, to, ok := customRange(def.TimePeriod); ok {
			return from, to
		}
	case "thelastmonth", "thelastbillingmonth":
		lastEnd := monthStart.AddDate(0, 0, -1)
		lastStart := time.Date(lastEnd.Year(), lastEnd.Month(), 1, 0, 0, 0, 0, time.UTC)

		return lastStart, lastEnd
	}

	return monthStart, truncateDay(now)
}

// customRange resolves a Custom timeframe's explicit [from, to] day range,
// reporting ok=false when the period is absent, unparsable, or inverted.
func customRange(tp *timePeriod) (start, end time.Time, ok bool) {
	if tp == nil {
		return time.Time{}, time.Time{}, false
	}

	from, okFrom := parseCMDate(tp.From)
	to, okTo := parseCMDate(tp.To)

	if !okFrom || !okTo || to.Before(from) {
		return time.Time{}, time.Time{}, false
	}

	return truncateDay(from), truncateDay(to), true
}

// parseCMDate parses a Cost Management date, accepting both the RFC3339
// timestamp the SDK marshals and a bare yyyy-mm-dd.
func parseCMDate(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}

	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), true
	}

	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t.UTC(), true
	}

	return time.Time{}, false
}

// truncateDay drops the time-of-day so day stepping lands on midnight
// boundaries.
func truncateDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// groupValue resolves one line's value for a grouping dimension. The supported
// dimensions map onto the discovery attributes the estimator carries; an
// unrecognized dimension resolves to empty, as real Cost Management does when a
// resource has no value for the requested dimension.
func groupValue(dimension string, line *cost.Line) string {
	switch strings.ToLower(dimension) {
	case "servicename":
		return serviceLabel(line.Service)
	case "resourcetype":
		return resourcegraph.AzureType(line.Service, line.Type)
	case "resourcegroup", "resourcegroupname":
		return resourcegraph.ResourceGroupOf(line.ARN)
	case "resourcelocation":
		return line.Region
	default:
		return ""
	}
}

// serviceServiceNameMap maps a portable service to the Azure Cost Management
// ServiceName meter category a real bill groups it under. Unmapped services
// fall back to the portable name so a grouped query always yields a value.
var serviceServiceNameMap = map[string]string{ //nolint:gochecknoglobals // static lookup table
	"compute":           "Virtual Machines",
	"storage":           "Storage",
	"database":          "Azure Cosmos DB",
	"relationaldb":      "SQL Database",
	"networking":        "Virtual Network",
	"serverless":        "Functions",
	"cache":             "Redis Cache",
	"kubernetes":        "Azure Kubernetes Service",
	"loadbalancer":      "Load Balancer",
	"containerregistry": "Container Registry",
	"databricks":        "Azure Databricks",
	"appservice":        "Azure App Service",
	"dns":               "Azure DNS",
	"secrets":           "Key Vault",
	"logging":           "Log Analytics",
}

func serviceLabel(service string) string {
	if label, ok := serviceServiceNameMap[service]; ok {
		return label
	}

	return service
}
