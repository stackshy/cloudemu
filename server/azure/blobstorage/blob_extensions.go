package blobstorage

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// blobExt is the Azure-specific blob capability, aliased for brevity.
type blobExt = storagedriver.AzureBlobExtensions

// stageBlock handles PUT /{container}/{blob}?comp=block&blockid=….
func (h *Handler) stageBlock(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	if h.checkLease(w, r, container, blob) {
		return
	}

	blockID := r.URL.Query().Get("blockid")
	if blockID == "" {
		writeError(w, http.StatusBadRequest, "InvalidQueryParameterValue", "missing blockid")
		return
	}

	data, ok := readLimitedBody(w, r)
	if !ok {
		return
	}

	if err := ext.StageBlock(r.Context(), container, blob, blockID, data); err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("X-Ms-Request-Server-Encrypted", "true")
	w.WriteHeader(http.StatusCreated)
}

// commitBlockList handles PUT /{container}/{blob}?comp=blocklist.
func (h *Handler) commitBlockList(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	if h.checkLease(w, r, container, blob) {
		return
	}

	if h.conditionFailed(w, r, container, blob, false) {
		return
	}

	body, ok := readLimitedBody(w, r)
	if !ok {
		return
	}

	blocks, err := parseBlockListXML(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidBlockList", err.Error())
		return
	}

	info, err := ext.CommitBlockList(
		r.Context(), container, blob, blocks, blobContentType(r), blobContentProps(r), extractMetadata(r.Header),
	)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeWriteResult(w, info, http.StatusCreated)
}

// setBlobMetadata handles PUT /{container}/{blob}?comp=metadata.
func (h *Handler) setBlobMetadata(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	if h.checkLease(w, r, container, blob) {
		return
	}

	if h.conditionFailed(w, r, container, blob, false) {
		return
	}

	info, err := ext.SetBlobMetadata(r.Context(), container, blob, extractMetadata(r.Header))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeWriteResult(w, info, http.StatusOK)
}

// setBlobProperties handles PUT /{container}/{blob}?comp=properties.
func (h *Handler) setBlobProperties(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	if h.checkLease(w, r, container, blob) {
		return
	}

	if h.conditionFailed(w, r, container, blob, false) {
		return
	}

	props := &storagedriver.BlobProperties{
		ContentType:        r.Header.Get("x-ms-blob-content-type"),
		ContentEncoding:    r.Header.Get("x-ms-blob-content-encoding"),
		ContentLanguage:    r.Header.Get("x-ms-blob-content-language"),
		ContentDisposition: r.Header.Get("x-ms-blob-content-disposition"),
		CacheControl:       r.Header.Get("x-ms-blob-cache-control"),
	}

	info, err := ext.SetBlobProperties(r.Context(), container, blob, props)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeWriteResult(w, info, http.StatusOK)
}

// setBlobTier handles PUT /{container}/{blob}?comp=tier.
func (h *Handler) setBlobTier(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	tier := r.Header.Get("x-ms-access-tier")
	if tier == "" {
		writeError(w, http.StatusBadRequest, "MissingRequiredHeader", "x-ms-access-tier is required")
		return
	}

	if h.conditionFailed(w, r, container, blob, false) {
		return
	}

	status, err := ext.SetBlobTier(r.Context(), container, blob, tier)
	if err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(status)
}

// snapshotBlob handles PUT /{container}/{blob}?comp=snapshot.
func (h *Handler) snapshotBlob(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	if h.conditionFailed(w, r, container, blob, false) {
		return
	}

	snapshot, info, err := ext.CreateBlobSnapshot(r.Context(), container, blob)
	if err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("X-Ms-Snapshot", snapshot)
	writeWriteResult(w, info, http.StatusCreated)
}

// createAppendBlob handles PUT /{container}/{blob} with x-ms-blob-type: AppendBlob.
func (h *Handler) createAppendBlob(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	if h.checkLease(w, r, container, blob) {
		return
	}

	if h.conditionFailed(w, r, container, blob, true) {
		return
	}

	info, err := ext.CreateAppendBlob(r.Context(), container, blob, blobContentType(r), extractMetadata(r.Header))
	if err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("X-Ms-Request-Server-Encrypted", "true")
	writeWriteResult(w, info, http.StatusCreated)
}

// appendBlock handles PUT /{container}/{blob}?comp=appendblock.
func (h *Handler) appendBlock(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	if h.checkLease(w, r, container, blob) {
		return
	}

	if h.conditionFailed(w, r, container, blob, false) {
		return
	}

	data, ok := readLimitedBody(w, r)
	if !ok {
		return
	}

	offset, committed, info, err := ext.AppendBlock(r.Context(), container, blob, data)
	if err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("X-Ms-Blob-Append-Offset", strconv.FormatInt(offset, 10))
	w.Header().Set("X-Ms-Blob-Committed-Block-Count", strconv.Itoa(committed))
	writeWriteResult(w, info, http.StatusCreated)
}

// setContainerMetadata handles PUT /{container}?restype=container&comp=metadata.
func (h *Handler) setContainerMetadata(w http.ResponseWriter, r *http.Request, container string) {
	ext, ok := h.bucket.(blobExt)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "container metadata not supported")
		return
	}

	if err := ext.SetContainerMetadata(r.Context(), container, extractMetadata(r.Header)); err != nil {
		writeErr(w, err)
		return
	}

	created, _ := h.containerCreatedAt(r, container)
	w.Header().Set("ETag", containerETag(container, created))
	w.Header().Set("Last-Modified", httpDate(created))
	w.WriteHeader(http.StatusOK)
}

// writeWriteResult writes the ETag/Last-Modified headers common to blob write
// responses and the status code.
func writeWriteResult(w http.ResponseWriter, info *storagedriver.ObjectInfo, status int) {
	if info != nil {
		w.Header().Set("ETag", fmt.Sprintf("%q", info.ETag))
		w.Header().Set("Last-Modified", httpDate(info.LastModified))
	}

	w.Header().Set("X-Ms-Request-Server-Encrypted", "true")
	w.WriteHeader(status)
}

// blobContentType resolves a blob's content type from x-ms-blob-content-type,
// falling back to the request Content-Type.
func blobContentType(r *http.Request) string {
	if ct := r.Header.Get("x-ms-blob-content-type"); ct != "" {
		return ct
	}

	return r.Header.Get("Content-Type")
}

// readLimitedBody reads the (capped) request body, writing a 400 on error.
func readLimitedBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPutBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidInput", "could not read body")
		return nil, false
	}

	return data, true
}

// parseBlockListXML extracts the ordered block entries from a Put Block List
// body, preserving both document order and each block's source list
// (Latest/Committed/Uncommitted) so the commit can resolve a block against the
// correct source rather than collapsing all three into one list.
func parseBlockListXML(body []byte) ([]storagedriver.BlockListEntry, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))

	var (
		entries []storagedriver.BlockListEntry
		list    string
	)

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			list = blockListSource(t.Name.Local)
		case xml.CharData:
			if list != "" {
				if id := string(bytes.TrimSpace([]byte(t))); id != "" {
					entries = append(entries, storagedriver.BlockListEntry{ID: id, List: list})
				}
			}
		case xml.EndElement:
			list = ""
		}
	}

	if len(entries) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "block list is empty")
	}

	return entries, nil
}

// blockListSource maps a Put Block List element name to its source-list value,
// returning "" for any non-block element.
func blockListSource(name string) string {
	switch name {
	case "Latest":
		return storagedriver.BlockListLatest
	case "Committed":
		return storagedriver.BlockListCommitted
	case "Uncommitted":
		return storagedriver.BlockListUncommitted
	default:
		return ""
	}
}
