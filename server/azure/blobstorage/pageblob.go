package blobstorage

import (
	"net/http"
	"strconv"
	"strings"

	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// pageWrite is the Azure page-write header name and its two actions.
const (
	pageWriteHeader = "X-Ms-Page-Write"
	pageWriteUpdate = "update"
	pageWriteClear  = "clear"
)

// createPageBlob handles PUT /{container}/{blob} with x-ms-blob-type: PageBlob,
// creating an empty page blob of the size named by x-ms-blob-content-length.
func (h *Handler) createPageBlob(
	w http.ResponseWriter, r *http.Request, page storagedriver.AzurePageBlob, container, blob string,
) {
	if h.checkLease(w, r, container, blob) {
		return
	}

	if h.conditionFailed(w, r, container, blob, true) {
		return
	}

	size, err := strconv.ParseInt(r.Header.Get("X-Ms-Blob-Content-Length"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidHeaderValue",
			"x-ms-blob-content-length must be a non-negative multiple of 512")
		return
	}

	props := &storagedriver.BlobProperties{ContentType: r.Header.Get("X-Ms-Blob-Content-Type")}
	if cp := blobContentProps(r); cp != nil {
		props.ContentEncoding = cp.ContentEncoding
		props.CacheControl = cp.CacheControl
		props.ContentLanguage = cp.ContentLanguage
		props.ContentDisposition = cp.ContentDisposition
	}

	info, err := page.CreatePageBlob(r.Context(), container, blob, size, props, extractMetadata(r.Header))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeWriteResult(w, info, http.StatusCreated)
}

// putPage handles PUT /{container}/{blob}?comp=page, dispatching on
// x-ms-page-write: update (write the request body over a range) or clear (zero a
// range).
func (h *Handler) putPage(
	w http.ResponseWriter, r *http.Request, page storagedriver.AzurePageBlob, container, blob string,
) {
	if h.checkLease(w, r, container, blob) {
		return
	}

	if h.conditionFailed(w, r, container, blob, false) {
		return
	}

	start, end, ok := parsePageRange(pageRangeHeader(r))
	if !ok {
		writeError(w, http.StatusRequestedRangeNotSatisfiable, "InvalidPageRange",
			"the x-ms-range header is missing or malformed")
		return
	}

	switch strings.ToLower(r.Header.Get(pageWriteHeader)) {
	case pageWriteClear:
		clearPage(w, r, page, container, blob, start, end)
	case pageWriteUpdate, "":
		updatePage(w, r, page, container, blob, start, end)
	default:
		writeError(w, http.StatusBadRequest, "InvalidHeaderValue",
			"x-ms-page-write must be 'update' or 'clear'")
	}
}

// updatePage writes the request body over the [start,end] page range.
func updatePage(
	w http.ResponseWriter, r *http.Request, page storagedriver.AzurePageBlob, container, blob string, start, end int64,
) {
	data, ok := readLimitedBody(w, r)
	if !ok {
		return
	}

	info, err := page.PutPage(r.Context(), container, blob, start, end, data)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeWriteResult(w, info, http.StatusCreated)
}

// clearPage zeroes the [start,end] page range.
func clearPage(
	w http.ResponseWriter, r *http.Request, page storagedriver.AzurePageBlob, container, blob string, start, end int64,
) {
	info, err := page.ClearPage(r.Context(), container, blob, start, end)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeWriteResult(w, info, http.StatusCreated)
}

// getPageRanges handles GET /{container}/{blob}?comp=pagelist (Get Page Ranges),
// returning the page blob's written byte ranges as a <PageList> document.
func (h *Handler) getPageRanges(w http.ResponseWriter, r *http.Request, container, blob string) {
	page, ok := h.bucket.(storagedriver.AzurePageBlob)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "page blobs are not supported")
		return
	}

	ranges, size, err := page.GetPageRanges(r.Context(), container, blob)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := pageListXML{}
	for _, pr := range ranges {
		out.PageRange = append(out.PageRange, pageRangeXML{Start: pr.Start, End: pr.End})
	}

	w.Header().Set("X-Ms-Blob-Content-Length", strconv.FormatInt(size, 10))
	writeXML(w, out)
}

// pageRangeHeader returns the range spec for a page op, preferring x-ms-range
// and falling back to the standard Range header.
func pageRangeHeader(r *http.Request) string {
	if h := r.Header.Get("X-Ms-Range"); h != "" {
		return h
	}

	return r.Header.Get("Range")
}

// parsePageRange parses an inclusive "bytes=start-end" page-range spec. Unlike a
// read range, both bounds are required (a page write always names a bounded
// range) and no clamping to a blob size happens here — bounds/alignment are the
// provider's to validate. ok is false for a missing or malformed spec.
func parsePageRange(header string) (start, end int64, ok bool) {
	const prefix = "bytes="

	if !strings.HasPrefix(header, prefix) {
		return 0, 0, false
	}

	spec := strings.TrimSpace(strings.TrimPrefix(header, prefix))

	startStr, endStr, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, false
	}

	start, err := strconv.ParseInt(strings.TrimSpace(startStr), 10, 64)
	if err != nil {
		return 0, 0, false
	}

	end, err = strconv.ParseInt(strings.TrimSpace(endStr), 10, 64)
	if err != nil {
		return 0, 0, false
	}

	return start, end, true
}
