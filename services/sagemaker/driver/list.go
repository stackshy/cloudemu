package driver

// Sort orders accepted by List operations (the SageMaker SortOrder enum).
const (
	SortAscending  = "Ascending"
	SortDescending = "Descending"
)

// Sort keys accepted by List operations (the SageMaker SortBy enums; the set a
// given operation accepts is a subset, validated by the provider).
const (
	SortByName             = "Name"
	SortByCreationTime     = "CreationTime"
	SortByLastModifiedTime = "LastModifiedTime"
	SortByStatus           = "Status"
)

// MaxListResults is the largest MaxResults a SageMaker List operation accepts.
const MaxListResults = 100

// ListOptions carries the filter, sort and paging inputs shared by every List
// operation. The zero value means "everything, default order" so internal
// callers (such as resource discovery) need no paging.
//
// Time filters use RFC3339 strings, matching how the mock stores times; an
// empty string disables the filter. MaxResults of zero means "all items" at
// the driver level; the wire handler applies the SageMaker default.
type ListOptions struct {
	NameContains           string
	NamePrefix             string
	StatusEquals           string
	CreationTimeAfter      string
	CreationTimeBefore     string
	LastModifiedTimeAfter  string
	LastModifiedTimeBefore string
	SortBy                 string
	SortOrder              string
	MaxResults             int
	NextToken              string
	// Filters carries operation-specific equality filters keyed by the SDK
	// input field name (for example "ModelApprovalStatus").
	Filters map[string]string
}

// ListPage is one page of List results. NextToken is empty on the last page.
type ListPage[T any] struct {
	Items     []T
	NextToken string
}
