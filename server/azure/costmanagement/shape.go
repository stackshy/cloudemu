package costmanagement

import (
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/azure/resourcegraph"
	"github.com/stackshy/cloudemu/v2/services/cost"
)

// keySep separates the per-dimension values of a composite grouping key. It is
// a control byte that cannot occur in any dimension value.
const keySep = "\x00"

// trailingSlots is the capacity headroom a column/row slice reserves for the
// two trailing entries every layout may carry: the optional granularity date
// column and the always-present Currency column.
const trailingSlots = 2

// shape turns the priced cost lines into the Cost Management columns/rows for
// the requested granularity and grouping. Column order follows the real API:
// the cost aggregation column(s) first, then the granularity date column (if
// any), then one column per grouping dimension, then Currency last; every row
// is ordered to match.
func shape(def *queryDefinition, lines []cost.Line) (columns []column, rows [][]any) {
	costCols := costColumnNames(def)
	dateCol, dateVal, hasDate := dateColumn(def.Dataset.Granularity)
	groupNames := groupingNames(def)

	columns = buildColumns(costCols, dateCol, hasDate, groupNames)

	if len(groupNames) == 0 {
		rows = ungroupedRows(costCols, dateVal, hasDate, lines)
	} else {
		rows = groupedRows(costCols, dateVal, hasDate, groupNames, lines)
	}

	return columns, rows
}

// buildColumns assembles the column descriptors in wire order.
func buildColumns(costCols []string, dateCol column, hasDate bool, groupNames []string) []column {
	columns := make([]column, 0, len(costCols)+len(groupNames)+trailingSlots)

	for _, name := range costCols {
		columns = append(columns, column{Name: name, Type: "Number"})
	}

	if hasDate {
		columns = append(columns, dateCol)
	}

	for _, name := range groupNames {
		columns = append(columns, column{Name: name, Type: "String"})
	}

	return append(columns, column{Name: "Currency", Type: "String"})
}

// ungroupedRows returns the single aggregate row (or none, when the estate is
// empty) for a query with no grouping dimensions.
func ungroupedRows(costCols []string, dateVal any, hasDate bool, lines []cost.Line) [][]any {
	if len(lines) == 0 {
		return [][]any{}
	}

	var total float64
	for i := range lines {
		total += lines[i].MonthlyUSD
	}

	return [][]any{buildRow(costCols, total, dateVal, hasDate, nil)}
}

// groupedRows buckets the lines by the composite grouping key and returns one
// row per bucket, sorted for deterministic output.
func groupedRows(costCols []string, dateVal any, hasDate bool, groupNames []string, lines []cost.Line) [][]any {
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
		rows = append(rows, buildRow(costCols, b.total, dateVal, hasDate, b.dims))
	}

	return rows
}

// buildRow lays out one row: the cost value repeated for each cost column, the
// date value (if the query is granular), the grouping dimension values, and the
// currency last — matching buildColumns.
func buildRow(costCols []string, total, dateVal any, hasDate bool, dims []string) []any {
	row := make([]any, 0, len(costCols)+len(dims)+trailingSlots)

	for range costCols {
		row = append(row, total)
	}

	if hasDate {
		row = append(row, dateVal)
	}

	for _, d := range dims {
		row = append(row, d)
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

// dateColumn returns the granularity date column, its value for the emulator's
// single period bucket, and whether the query is granular at all. Daily buckets
// carry a numeric yyyymmdd UsageDate (as real Cost Management does); Monthly
// carries a BillingMonth timestamp. "None"/absent granularity has no date
// column — a single aggregate over the period.
func dateColumn(granularity string) (column, any, bool) {
	now := time.Now().UTC()

	switch strings.ToLower(granularity) {
	case "daily":
		ymd := now.Year()*10000 + int(now.Month())*100 + now.Day()

		return column{Name: "UsageDate", Type: "Number"}, ymd, true
	case "monthly":
		month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

		return column{Name: "BillingMonth", Type: "Datetime"}, month.Format("2006-01-02T15:04:05"), true
	default:
		return column{}, nil, false
	}
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
