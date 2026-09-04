package cloudfunctions

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// storageEventFilterAttrBucket is the eventTrigger.eventFilters attribute
// name a gen2 Cloud Storage trigger binds to a specific bucket, mirroring
// real GCP (google_cloudfunctions2_function.event_trigger.event_filters
// { attribute = "bucket", value = "<bucket>" }).
const storageEventFilterAttrBucket = "bucket"

// storageObjectResource mirrors the JSON shape the gcs handler marshals for
// an object-change event (server/gcp/gcs does not export its objectResource
// type, so the fields this package needs are redeclared here). The CloudEvent
// `data` payload a real Eventarc-backed Cloud Storage trigger delivers IS
// this resource verbatim — the object's metadata, never its bytes.
type storageObjectResource struct {
	Kind           string            `json:"kind"`
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Bucket         string            `json:"bucket"`
	Generation     string            `json:"generation"`
	Metageneration string            `json:"metageneration"`
	ContentType    string            `json:"contentType,omitempty"`
	Size           string            `json:"size"`
	MD5Hash        string            `json:"md5Hash,omitempty"`
	CRC32C         string            `json:"crc32c,omitempty"`
	ETag           string            `json:"etag,omitempty"`
	StorageClass   string            `json:"storageClass,omitempty"`
	TimeCreated    string            `json:"timeCreated,omitempty"`
	Updated        string            `json:"updated,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// gen2StorageEvent is the structured-mode CloudEvent envelope a real gen2
// Eventarc-backed Cloud Storage trigger delivers. Unlike Pub/Sub (whose data
// wraps {message, subscription}), a storage trigger's data IS the object
// resource directly. Real Eventarc delivers binary-mode CloudEvents (ce-* as
// HTTP headers, the bare data as the body), but driver.InvokeInput carries
// only a payload — no headers — so structured mode is the closest
// self-contained equivalent the emulator's invoke contract can deliver (same
// tradeoff as the Pub/Sub gen2 envelope in pubsubtrigger.go).
type gen2StorageEvent struct {
	SpecVersion     string                `json:"specversion"`
	Type            string                `json:"type"`
	Source          string                `json:"source"`
	Subject         string                `json:"subject"`
	ID              string                `json:"id"`
	Time            string                `json:"time,omitempty"`
	DataContentType string                `json:"datacontenttype"`
	Data            storageObjectResource `json:"data"`
}

// InvokeForObjectEvent delivers a GCS object-change event to every gen2 Cloud
// Function whose eventTrigger is a Cloud Storage trigger bound to (bucket,
// eventType) via an eventFilters "bucket" attribute. Only gen2 functions
// carry a direct storage eventTrigger in this shape; a gen1 storage-triggered
// function is delivered through the legacy GCS notificationConfig -> Pub/Sub
// -> function chain instead (server/gcp/gcs's TopicPublisher + this
// package's InvokeForTopic), so it is out of scope here. Best-effort — a
// missing or failing function is swallowed so an object write/delete never
// fails. It implements the gcs handler's FunctionInvoker.
//
// ctx carries the re-entrant delivery depth (internal/recursionguard): a
// function invoked from here that writes to its own trigger bucket would
// otherwise recurse synchronously and unbounded (PutObject ->
// InvokeForObjectEvent -> Invoke -> handler -> PutObject -> ...), so once the
// depth reaches recursionguard.MaxDepth this whole delivery hop is dropped.
func (h *Handler) InvokeForObjectEvent(ctx context.Context, bucket, eventType string, resource []byte) {
	depth := recursionguard.Depth(ctx)
	if depth >= recursionguard.MaxDepth {
		return
	}

	ctx = recursionguard.WithDepth(ctx, depth+1)

	targets := h.storageTargetsLocked(bucket, eventType)
	if len(targets) == 0 {
		return
	}

	var res storageObjectResource
	if err := json.Unmarshal(resource, &res); err != nil {
		return
	}

	for _, name := range targets {
		payload, err := json.Marshal(buildGen2StorageEvent(bucket, eventType, &res))
		if err != nil {
			continue
		}

		_, _ = h.fn.Invoke(ctx, sdrv.InvokeInput{FunctionName: name, Payload: payload, InvokeType: "Event"})
	}
}

// storageTargetsLocked snapshots the gen2 function (short) names whose
// eventTrigger is a Cloud Storage trigger bound to bucket and eventType.
func (h *Handler) storageTargetsLocked(bucket, eventType string) (names []string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, fn := range h.gen2 {
		if gen2StorageTriggerMatches(fn.EventTrigger, bucket, eventType) {
			names = append(names, lastSegment(fn.Name))
		}
	}

	return names
}

// gen2StorageTriggerMatches reports whether et is a Cloud Storage trigger for
// eventType bound to bucket via an eventFilters "bucket" attribute.
func gen2StorageTriggerMatches(et *gen2EventTrigger, bucket, eventType string) bool {
	if et == nil || et.EventType != eventType {
		return false
	}

	for _, f := range et.EventFilters {
		if f.Attribute == storageEventFilterAttrBucket {
			return f.Value == bucket
		}
	}

	return false
}

// buildGen2StorageEvent wraps an object resource into the CloudEvent shape a
// gen2 function's Cloud Storage eventTrigger delivers: source names the
// bucket (real GCP always uses the "_" wildcard project segment here, never
// the actual project id), subject names the object.
func buildGen2StorageEvent(bucket, eventType string, res *storageObjectResource) gen2StorageEvent {
	return gen2StorageEvent{
		SpecVersion:     "1.0",
		Type:            eventType,
		Source:          "//storage.googleapis.com/projects/_/buckets/" + bucket,
		Subject:         "objects/" + res.Name,
		ID:              bucket + "/" + res.Name + "/" + res.Generation,
		Time:            res.Updated,
		DataContentType: "application/json",
		Data:            *res,
	}
}
