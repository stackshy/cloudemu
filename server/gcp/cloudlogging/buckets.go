package cloudlogging

import (
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// logBucketJSON is the Cloud Logging LogBucket resource shape.
type logBucketJSON struct {
	Name           string `json:"name,omitempty"`
	Description    string `json:"description,omitempty"`
	RetentionDays  int32  `json:"retentionDays,omitempty"`
	Locked         bool   `json:"locked,omitempty"`
	LifecycleState string `json:"lifecycleState,omitempty"`
	CreateTime     string `json:"createTime,omitempty"`
	UpdateTime     string `json:"updateTime,omitempty"`
}

type listBucketsResponse struct {
	Buckets       []logBucketJSON `json:"buckets"`
	NextPageToken string          `json:"nextPageToken,omitempty"`
}

// bucketNameFor builds the fully-qualified bucket resource name:
// "projects/{project}/locations/{location}/buckets/{name}".
func bucketNameFor(project, location, name string) string {
	return "projects/" + project + "/locations/" + location + "/buckets/" + name
}

func toBucketJSON(project, location string, b *logdriver.LogBucket) logBucketJSON {
	out := logBucketJSON{
		Name:           bucketNameFor(project, location, b.Name),
		Description:    b.Description,
		RetentionDays:  b.RetentionDays,
		Locked:         b.Locked,
		LifecycleState: b.LifecycleState,
	}

	if !b.CreatedAt.IsZero() {
		out.CreateTime = b.CreatedAt.UTC().Format(time.RFC3339Nano)
	}

	if !b.UpdatedAt.IsZero() {
		out.UpdateTime = b.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}

	return out
}

// writeBucket writes a single bucket or the driver error that produced it.
func writeBucket(w http.ResponseWriter, project, location string, b *logdriver.LogBucket, err error) {
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toBucketJSON(project, location, b))
}

// routeBuckets serves the projects.locations.buckets collection:
// GET/POST .../locations/{l}/buckets and GET/PATCH/DELETE .../buckets/{id}.
func (h *Handler) routeBuckets(w http.ResponseWriter, r *http.Request, project, location, tail string) {
	gcp, ok := h.gcpBackend(w)
	if !ok {
		return
	}

	if tail == "/" {
		switch r.Method {
		case http.MethodPost:
			createBucket(w, r, gcp, project, location)
		case http.MethodGet:
			listBuckets(w, r, gcp, project, location)
		default:
			writeMethodNotAllowed(w)
		}

		return
	}

	bucketID := strings.TrimPrefix(tail, "/")

	switch r.Method {
	case http.MethodGet:
		b, err := gcp.GetBucket(r.Context(), project, location, bucketID)
		writeBucket(w, project, location, b, err)
	case http.MethodPatch, http.MethodPut:
		updateBucket(w, r, gcp, project, location, bucketID)
	case http.MethodDelete:
		deleteResource(w, gcp.DeleteBucket(r.Context(), project, location, bucketID))
	default:
		writeMethodNotAllowed(w)
	}
}

// createBucket maps CreateBucket. The bucket id is a required "bucketId" query
// param (the body's own name field is empty on create, matching the real API).
func createBucket(w http.ResponseWriter, r *http.Request, gcp logdriver.GCPLogging, project, location string) {
	var body logBucketJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	bucketID := r.URL.Query().Get("bucketId")
	if bucketID == "" {
		bucketID = body.Name
	}

	b, err := gcp.CreateBucket(r.Context(), project, location, &logdriver.LogBucket{
		Name:          bucketID,
		Description:   body.Description,
		RetentionDays: body.RetentionDays,
	})
	writeBucket(w, project, location, b, err)
}

func listBuckets(w http.ResponseWriter, r *http.Request, gcp logdriver.GCPLogging, project, location string) {
	buckets, err := gcp.ListBuckets(r.Context(), project, location)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	out := make([]logBucketJSON, 0, len(buckets))
	for i := range buckets {
		out = append(out, toBucketJSON(project, buckets[i].Location, &buckets[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listBucketsResponse{Buckets: out})
}

// updateBucket maps UpdateBucket (PATCH). When the caller sends an
// updateMask, only the named fields are applied. A caller with no mask falls
// back to a presence heuristic (retentionDays/locked apply only when
// non-zero/true), so a plain full-body PUT-style call cannot silently zero
// out retention or unlock a bucket it never meant to touch.
func updateBucket(w http.ResponseWriter, r *http.Request, gcp logdriver.GCPLogging, project, location, bucketID string) {
	var body logBucketJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	mask := r.URL.Query().Get("updateMask")

	update := logdriver.BucketUpdate{
		Description:   body.Description,
		RetentionDays: body.RetentionDays,
		Locked:        body.Locked,
	}

	if mask == "" {
		update.SetDescription = true
		update.SetRetentionDays = body.RetentionDays != 0
		update.SetLocked = body.Locked
	} else {
		update.SetDescription = maskHas(mask, "description")
		update.SetRetentionDays = maskHas(mask, "retentionDays") || maskHas(mask, "retention_days")
		update.SetLocked = maskHas(mask, "locked")
	}

	b, err := gcp.UpdateBucket(r.Context(), project, location, bucketID, update)
	writeBucket(w, project, location, b, err)
}

// maskHas reports whether the comma-separated updateMask names field exactly.
func maskHas(mask, field string) bool {
	for _, p := range strings.Split(mask, ",") {
		if strings.TrimSpace(p) == field {
			return true
		}
	}

	return false
}
