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
func (*Handler) stageBlock(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
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
func (*Handler) commitBlockList(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	body, ok := readLimitedBody(w, r)
	if !ok {
		return
	}

	blockIDs, err := parseBlockListXML(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidBlockList", err.Error())
		return
	}

	info, err := ext.CommitBlockList(r.Context(), container, blob, blockIDs, blobContentType(r), extractMetadata(r.Header))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeWriteResult(w, info, http.StatusCreated)
}

// setBlobMetadata handles PUT /{container}/{blob}?comp=metadata.
func (*Handler) setBlobMetadata(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	info, err := ext.SetBlobMetadata(r.Context(), container, blob, extractMetadata(r.Header))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeWriteResult(w, info, http.StatusOK)
}

// setBlobProperties handles PUT /{container}/{blob}?comp=properties.
func (*Handler) setBlobProperties(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
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
func (*Handler) setBlobTier(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	tier := r.Header.Get("x-ms-access-tier")
	if tier == "" {
		writeError(w, http.StatusBadRequest, "MissingRequiredHeader", "x-ms-access-tier is required")
		return
	}

	if err := ext.SetBlobTier(r.Context(), container, blob, tier); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// snapshotBlob handles PUT /{container}/{blob}?comp=snapshot.
func (*Handler) snapshotBlob(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	snapshot, info, err := ext.CreateBlobSnapshot(r.Context(), container, blob)
	if err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("X-Ms-Snapshot", snapshot)
	writeWriteResult(w, info, http.StatusCreated)
}

// createAppendBlob handles PUT /{container}/{blob} with x-ms-blob-type: AppendBlob.
func (*Handler) createAppendBlob(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	info, err := ext.CreateAppendBlob(r.Context(), container, blob, blobContentType(r), extractMetadata(r.Header))
	if err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("X-Ms-Request-Server-Encrypted", "true")
	writeWriteResult(w, info, http.StatusCreated)
}

// appendBlock handles PUT /{container}/{blob}?comp=appendblock.
func (*Handler) appendBlock(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
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

// parseBlockListXML extracts the ordered block IDs from a Put Block List body,
// preserving document order across Latest/Committed/Uncommitted elements.
func parseBlockListXML(body []byte) ([]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))

	var (
		ids     []string
		capture bool
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
			capture = isBlockElement(t.Name.Local)
		case xml.CharData:
			if capture {
				if id := string(bytes.TrimSpace([]byte(t))); id != "" {
					ids = append(ids, id)
				}
			}
		case xml.EndElement:
			capture = false
		}
	}

	if len(ids) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "block list is empty")
	}

	return ids, nil
}

func isBlockElement(name string) bool {
	return name == "Latest" || name == "Committed" || name == "Uncommitted"
}
