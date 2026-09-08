package driver

import "context"

// Contributor Insights status constants. Enabling/disabling is synchronous in
// the emulator, so an ENABLE lands ENABLED and a DISABLE lands DISABLED.
const (
	ContributorInsightsEnabled  = "ENABLED"
	ContributorInsightsDisabled = "DISABLED"
)

// ContributorInsightsSummary describes the Contributor Insights state of one
// table or table index. Index is empty for the table itself, otherwise the GSI
// name. Status is ContributorInsightsEnabled / ContributorInsightsDisabled.
type ContributorInsightsSummary struct {
	Table  string
	Index  string
	Status string
}

// ContributorInsighter is an OPTIONAL capability, discovered by type assertion
// (like Backuper): a provider whose tables report Contributor Insights state
// implements it. Only the AWS DynamoDB mock does; Cosmos DB / Firestore don't,
// so it stays off the cross-cloud Database interface.
type ContributorInsighter interface {
	// UpdateContributorInsights enables or disables Contributor Insights for the
	// table (index empty) or one of its GSIs, returning the resulting status.
	UpdateContributorInsights(ctx context.Context, table, index string, enable bool) (string, error)
	// DescribeContributorInsights returns the Contributor Insights status of the
	// table or index (DISABLED when never enabled), plus the last update time as
	// a Unix-seconds value (0 when never changed).
	DescribeContributorInsights(ctx context.Context, table, index string) (status string, lastUpdateUnix float64, err error)
	// ListContributorInsights returns a summary for every table/index that has a
	// Contributor Insights record, ordered by table then index. When table is
	// non-empty only that table's records are returned.
	ListContributorInsights(ctx context.Context, table string) ([]ContributorInsightsSummary, error)
}
