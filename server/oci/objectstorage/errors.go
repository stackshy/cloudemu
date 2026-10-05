package objectstorage

import (
	"errors"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	osprovider "github.com/stackshy/cloudemu/v2/providers/oci/objectstorage"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// serviceStatus is the HTTP status of each Object Storage-specific code.
//
//nolint:gochecknoglobals // a fixed lookup table
var serviceStatus = map[string]int{
	osprovider.CodeBucketAlreadyExists: http.StatusConflict,
	osprovider.CodeBucketNotEmpty:      http.StatusConflict,
	osprovider.CodeIfMatchFailed:       http.StatusPreconditionFailed,
	osprovider.CodeIfNoneMatchFailed:   http.StatusPreconditionFailed,
}

// writeDriverError reports the Object Storage code a provider error carries,
// falling back to the shared OCI codec for portable errors.
func writeDriverError(w http.ResponseWriter, r *http.Request, err error) {
	var se *osprovider.ServiceError
	if errors.As(err, &se) {
		if status, ok := serviceStatus[se.Code]; ok {
			ocirest.WriteError(w, r, status, se.Code, cerrors.Message(err))
			return
		}
	}

	ocirest.WriteDriverError(w, r, err)
}

// writePage pages a full listing by OCI's limit and page parameters, stamping
// opc-next-page when more remain. An absent limit takes Object Storage's own
// page size of 1000, as ListObjects does.
func writePage[T any](w http.ResponseWriter, r *http.Request, items []T) {
	writePageAs(w, r, items, func(page []T) any { return page })
}

// writePageAs is writePage for a listing wrapped in an envelope.
func writePageAs[T any](w http.ResponseWriter, r *http.Request, items []T, wrap func([]T) any) {
	limit := listLimit(r)
	if limit <= 0 {
		limit = ocirest.MaxLimit
	}

	page, err := pagination.Paginate(items, ocirest.Page(r), limit)
	if err != nil {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "invalid page token: "+err.Error())
		return
	}

	if page.Items == nil {
		page.Items = []T{}
	}

	ocirest.SetNextPage(w, page.NextPageToken)
	ocirest.WriteJSON(w, r, http.StatusOK, wrap(page.Items))
}
