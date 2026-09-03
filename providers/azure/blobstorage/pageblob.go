package blobstorage

import (
	"context"
	"maps"
	"net/http"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

const (
	// blobTypePage is the Azure page-blob type.
	blobTypePage = "PageBlob"
	// pageSize is the fixed Azure page-blob page size, in bytes. Page blobs are
	// created at a size that is a multiple of it and every Put Page / Clear Page
	// range must be aligned to it.
	pageSize = 512
)

// Compile-time check that Mock satisfies the optional AzurePageBlob capability
// the blob wire handler reaches by type assertion.
var _ driver.AzurePageBlob = (*Mock)(nil)

// CreatePageBlob creates an empty page blob of size bytes, all pages zeroed.
func (m *Mock) CreatePageBlob(
	_ context.Context, container, blob string, size int64, props *driver.BlobProperties, metadata map[string]string,
) (*driver.ObjectInfo, error) {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	// Immutable storage (WORM): re-creating a page blob over a protected key
	// would replace its content with zero bytes — block it. A fresh key passes.
	if err := m.enforceImmutable(ctr, blob); err != nil {
		return nil, err
	}

	if size < 0 || size%pageSize != 0 {
		return nil, &driver.BlobOpError{
			Status: http.StatusBadRequest, Code: "InvalidHeaderValue",
			Message: "x-ms-blob-content-length must be a non-negative multiple of 512",
		}
	}

	contentType := octetStream
	if props != nil && props.ContentType != "" {
		contentType = props.ContentType
	}

	obj := &blobObject{
		Key: blob, Data: make([]byte, size), Size: size, ContentType: contentType,
		LastModified: m.opts.Clock.Now().UTC().Format(blobTimeFormat),
		Metadata:     maps.Clone(metadata), BlobType: blobTypePage,
		pages: make(map[int64]bool),
	}

	if props != nil {
		obj.ContentEncoding = props.ContentEncoding
		obj.ContentLanguage = props.ContentLanguage
		obj.ContentDisposition = props.ContentDisposition
		obj.CacheControl = props.CacheControl
	}

	obj.ETag = computeBlobETag(obj)

	m.carryOverLease(ctr, blob, obj)
	m.recordVersion(ctr, obj)
	ctr.objects.Set(blob, obj)

	info := objectInfo(obj)

	return &info, nil
}

// PutPage writes data over the inclusive byte range [start,end] of a page blob.
func (m *Mock) PutPage(
	_ context.Context, container, blob string, start, end int64, data []byte,
) (*driver.ObjectInfo, error) {
	ctr, obj, err := m.pageBlob(container, blob)
	if err != nil {
		return nil, err
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	// Immutable storage (WORM): writing pages over a protected blob is blocked.
	if berr := immutabilityBlock(obj, m.opts.Clock.Now().UTC()); berr != nil {
		return nil, berr
	}

	if err := validatePageRange(start, end, obj.Size); err != nil {
		return nil, err
	}

	if int64(len(data)) != end-start+1 {
		return nil, &driver.BlobOpError{
			Status: http.StatusBadRequest, Code: "InvalidHeaderValue",
			Message: "Content-Length does not match the x-ms-range span",
		}
	}

	copy(obj.Data[start:end+1], data)
	setPages(obj, start, end, true)

	m.finishPageWrite(ctr, container, obj, int64(len(data)))

	out := objectInfo(obj)

	return &out, nil
}

// ClearPage zeroes the inclusive byte range [start,end] of a page blob.
func (m *Mock) ClearPage(_ context.Context, container, blob string, start, end int64) (*driver.ObjectInfo, error) {
	ctr, obj, err := m.pageBlob(container, blob)
	if err != nil {
		return nil, err
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	// Immutable storage (WORM): clearing pages of a protected blob is blocked.
	if berr := immutabilityBlock(obj, m.opts.Clock.Now().UTC()); berr != nil {
		return nil, berr
	}

	if err := validatePageRange(start, end, obj.Size); err != nil {
		return nil, err
	}

	for i := start; i <= end; i++ {
		obj.Data[i] = 0
	}

	setPages(obj, start, end, false)

	m.finishPageWrite(ctr, container, obj, 0)

	out := objectInfo(obj)

	return &out, nil
}

// GetPageRanges returns the page blob's written ranges (ordered, coalesced) and
// its total size.
func (m *Mock) GetPageRanges(_ context.Context, container, blob string) ([]driver.PageRange, int64, error) {
	_, obj, err := m.pageBlob(container, blob)
	if err != nil {
		return nil, 0, err
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	return coalescePages(obj.pages), obj.Size, nil
}

// pageBlob fetches a container and one of its live page blobs, erroring if the
// container or blob is absent or the blob is not a page blob.
func (m *Mock) pageBlob(container, blob string) (*containerMeta, *blobObject, error) {
	ctr, obj, err := m.getContainerBlob(container, blob)
	if err != nil {
		return nil, nil, err
	}

	if obj.BlobType != blobTypePage {
		return nil, nil, &driver.BlobOpError{
			Status: http.StatusConflict, Code: "InvalidBlobType",
			Message: "The blob type is invalid for this operation.",
		}
	}

	return ctr, obj, nil
}

// finishPageWrite stamps the blob's last-modified/ETag, records a version, and
// emits a transaction metric after a Put Page / Clear Page mutation. The caller
// must hold obj.mu.
func (m *Mock) finishPageWrite(ctr *containerMeta, container string, obj *blobObject, ingress int64) {
	obj.LastModified = m.opts.Clock.Now().UTC().Format(blobTimeFormat)
	obj.ETag = computeBlobETag(obj)

	m.recordVersion(ctr, obj)
	m.emitMetric(container, map[string]float64{"Transactions": 1, "Ingress": float64(ingress)})
}

// validatePageRange enforces the Azure page-range rules: start/end aligned to
// the 512-byte page boundary and the whole range inside the blob.
func validatePageRange(start, end, size int64) error {
	if start < 0 || end < start || start%pageSize != 0 || (end+1)%pageSize != 0 || end >= size {
		return &driver.BlobOpError{
			Status: http.StatusRequestedRangeNotSatisfiable, Code: "InvalidPageRange",
			Message: "The page range specified is invalid.",
		}
	}

	return nil
}

// setPages marks (or clears) every page index covered by the inclusive byte
// range [start,end] in obj.pages. The caller must hold obj.mu.
func setPages(obj *blobObject, start, end int64, written bool) {
	for page := start / pageSize; page <= end/pageSize; page++ {
		if written {
			obj.pages[page] = true
		} else {
			delete(obj.pages, page)
		}
	}
}

// coalescePages turns the set of written page indices into contiguous byte
// ranges ordered by Start, merging adjacent pages into a single range.
func coalescePages(pages map[int64]bool) []driver.PageRange {
	if len(pages) == 0 {
		return nil
	}

	idx := make([]int64, 0, len(pages))
	for page := range pages {
		idx = append(idx, page)
	}

	sort.Slice(idx, func(i, j int) bool { return idx[i] < idx[j] })

	var ranges []driver.PageRange

	runStart, prev := idx[0], idx[0]
	for _, page := range idx[1:] {
		if page == prev+1 {
			prev = page
			continue
		}

		ranges = append(ranges, driver.PageRange{Start: runStart * pageSize, End: (prev+1)*pageSize - 1})
		runStart, prev = page, page
	}

	ranges = append(ranges, driver.PageRange{Start: runStart * pageSize, End: (prev+1)*pageSize - 1})

	return ranges
}
