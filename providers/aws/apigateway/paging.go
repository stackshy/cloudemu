package apigateway

import (
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// Page sizes for the position/limit collections: 25 when limit is omitted and
// at most 500, as API Gateway documents.
const (
	defaultPageLimit = 25
	maxPageLimit     = 500
)

// shortIDLen is the length of client certificate and documentation part ids
// (six lowercase alphanumerics, e.g. "a1b2c3").
const shortIDLen = 6

// genShortID returns a random six-character lowercase-alphanumeric id.
func genShortID() string { return randomID(shortIDLen) }

// pageOf slices one page out of items, which the caller has already put in a
// stable order. It returns the page and the position of the next one (empty on
// the last page).
func pageOf[T any](items []T, in driver.PageInput) (page []T, next string, err error) {
	limit := in.Limit
	if limit == 0 {
		limit = defaultPageLimit
	}

	if limit < 0 || limit > maxPageLimit {
		return nil, "", cerrors.Newf(cerrors.InvalidArgument, "limit must be between 1 and %d", maxPageLimit)
	}

	p, err := pagination.Paginate(items, in.Position, limit)
	if err != nil {
		return nil, "", cerrors.New(cerrors.InvalidArgument, "Invalid position parameter")
	}

	return p.Items, p.NextPageToken, nil
}
