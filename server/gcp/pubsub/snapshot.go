package pubsub

import (
	"net/http"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
)

const snapshotTTL = 7 * 24 * time.Hour

// ---------- Snapshots ----------

func (h *Handler) serveSnapshot(w http.ResponseWriter, r *http.Request, project, name, _ string) {
	switch r.Method {
	case http.MethodPut:
		h.createSnapshot(w, r, project, name)
	case http.MethodGet:
		h.getSnapshot(w, project, name)
	case http.MethodDelete:
		h.deleteSnapshot(w, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, reasonMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) createSnapshot(w http.ResponseWriter, r *http.Request, project, name string) {
	var body createSnapshotRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	subShort := shortName(body.Subscription)

	h.mu.Lock()

	sub, ok := h.subs[subShort]
	if !ok {
		h.mu.Unlock()
		writeError(w, http.StatusNotFound, reasonNotFound, "subscription "+subShort+" not found")

		return
	}

	if _, exists := h.snapshots[name]; exists {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, reasonAlreadyExists, "snapshot "+name+" already exists")

		return
	}

	snap := &snapState{
		topic:      sub.topic,
		acked:      copyIntSet(sub.acked),
		labels:     body.Labels,
		createTime: time.Now().UTC(),
		expireTime: time.Now().UTC().Add(snapshotTTL),
	}
	h.snapshots[name] = snap
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, snapshotJSON(project, name, snap))
}

func (h *Handler) getSnapshot(w http.ResponseWriter, project, name string) {
	h.mu.RLock()
	snap, ok := h.snapshots[name]
	h.mu.RUnlock()

	if !ok {
		writeError(w, http.StatusNotFound, reasonNotFound, "snapshot "+name+" not found")
		return
	}

	writeJSON(w, http.StatusOK, snapshotJSON(project, name, snap))
}

func (h *Handler) deleteSnapshot(w http.ResponseWriter, name string) {
	h.mu.Lock()
	_, ok := h.snapshots[name]
	delete(h.snapshots, name)
	h.mu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, reasonNotFound, "snapshot "+name+" not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) listSnapshots(w http.ResponseWriter, r *http.Request, project string) {
	h.mu.RLock()
	items := make([]snapshot, 0, len(h.snapshots))

	for snapName, snap := range h.snapshots {
		items = append(items, snapshotJSON(project, snapName, snap))
	}
	h.mu.RUnlock()

	page, err := pagination.PaginateSorted(items, func(a, b snapshot) bool { return a.Name < b.Name },
		r.URL.Query().Get("pageToken"), pageSize(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, reasonInvalidArgument, "invalid pageToken")
		return
	}

	writeJSON(w, http.StatusOK, listSnapshotsResponse{Snapshots: page.Items, NextPageToken: page.NextPageToken})
}

func (h *Handler) listTopicSnapshots(w http.ResponseWriter, project, topicShort string) {
	h.mu.RLock()
	names := make([]string, 0)

	for snapName, snap := range h.snapshots {
		if snap.topic == topicShort {
			names = append(names, snapshotName(project, snapName))
		}
	}
	h.mu.RUnlock()

	sort.Strings(names)
	writeJSON(w, http.StatusOK, listTopicSubscriptionsResponse{Subscriptions: names})
}

// seek rewinds a subscription's ack cursor to a snapshot or a timestamp.
func (h *Handler) seek(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, reasonMethodNotAllowed, "method not allowed")
		return
	}

	var req seekRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	status, reason, msg := h.applySeek(name, req)
	if status != http.StatusOK {
		writeError(w, status, reason, msg)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

// applySeek mutates the subscription's ack cursor and reports the HTTP outcome.
func (h *Handler) applySeek(name string, req seekRequest) (status int, reason, msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	sub, ok := h.subs[name]
	if !ok {
		return http.StatusNotFound, reasonNotFound, "subscription " + name + " not found"
	}

	switch {
	case req.Snapshot != "":
		snap, sok := h.snapshots[shortName(req.Snapshot)]
		if !sok {
			return http.StatusNotFound, reasonNotFound, "snapshot not found"
		}

		sub.acked = copyIntSet(snap.acked)
	case req.Time != "":
		t, err := time.Parse(time.RFC3339Nano, req.Time)
		if err != nil {
			return http.StatusBadRequest, reasonInvalidArgument, "invalid time: " + err.Error()
		}

		sub.acked = h.ackedBefore(sub.topic, t)
	default:
		return http.StatusBadRequest, reasonInvalidArgument, "seek requires time or snapshot"
	}

	sub.outstanding = make(map[string]*lease)

	return http.StatusOK, "", ""
}

// ackedBefore returns the set of message indices published before t (marked
// acknowledged after a time-based seek). The caller holds h.mu.
func (h *Handler) ackedBefore(topicShort string, t time.Time) map[int]bool {
	acked := make(map[int]bool)

	if ts, ok := h.topics[topicShort]; ok {
		for i := range ts.messages {
			if ts.messages[i].publishTime.Before(t) {
				acked[i] = true
			}
		}
	}

	return acked
}

func snapshotJSON(project, name string, snap *snapState) snapshot {
	return snapshot{
		Name:       snapshotName(project, name),
		Topic:      topicName(project, snap.topic),
		ExpireTime: snap.expireTime.Format(time.RFC3339Nano),
		Labels:     snap.labels,
	}
}

func copyIntSet(src map[int]bool) map[int]bool {
	dst := make(map[int]bool, len(src))
	for k, v := range src {
		dst[k] = v
	}

	return dst
}
