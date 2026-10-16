package networkfirewall

import (
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

const (
	defaultPageSize = 100
	maxPageSize     = 100
)

// paginate slices one page out of items (already in deterministic order) using
// the caller's MaxResults (1-100, default 100) and NextToken. A bad range or an
// undecodable token is a request error, never "page 1".
func paginate[T any](items []T, opts nfdriver.ListOptions) (page []T, nextToken string, err error) {
	size := opts.MaxResults

	switch {
	case size == 0:
		size = defaultPageSize
	case size < 1 || size > maxPageSize:
		return nil, "", cerrors.Newf(cerrors.InvalidArgument, "MaxResults must be between 1 and %d", maxPageSize)
	}

	pg, err := pagination.Paginate(items, opts.NextToken, size)
	if err != nil {
		// The only way Paginate fails is an undecodable token.
		return nil, "", cerrors.New(cerrors.InvalidArgument, "NextToken is not valid")
	}

	return pg.Items, pg.NextPageToken, nil
}
