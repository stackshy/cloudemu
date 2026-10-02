package managedkafka

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	mkdriver "github.com/stackshy/cloudemu/v2/services/managedkafka/driver"
)

const (
	clusterIDParam = "clusterId"
	topicIDParam   = "topicId"

	nextPageTokenKey = "nextPageToken"

	defaultPageSize = 500
	maxPageSize     = 500
)

// createCluster handles POST .../clusters?clusterId=. Validation (clusterId
// format, capacity, network configs) lives in the driver; a failure is 400. In
// an assembled server an id already used on the shared clusters path by the
// other service is 409 ALREADY_EXISTS, in either direction: a Kafka create
// naming a GKE/AlloyDB cluster's id, or a GKE/AlloyDB create (claimed only for
// this) naming a Kafka cluster's id.
func (h *Handler) createCluster(w http.ResponseWriter, r *http.Request, rt *route) {
	id := r.URL.Query().Get(clusterIDParam)

	if h.poller == nil {
		if probe := probeBody(r); !isKafkaBody(probe) {
			writeIDTaken(w, rt, foreignCreateID(r, probe), "a Managed Kafka")
			return
		}

		if h.siblingOwns(r.Context(), rt, id) {
			writeIDTaken(w, rt, id, "another service's (GKE or AlloyDB)")
			return
		}
	}

	var body clusterJSON
	if !gcprest.DecodeOptionalJSON(w, r, &body) {
		return
	}

	c, op, err := h.db.CreateCluster(r.Context(), toDriverCluster(&body, rt, id))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeOperation(w, op, fromDriverCluster(c), clusterTypeURL)
}

func (h *Handler) getCluster(w http.ResponseWriter, r *http.Request, rt *route) {
	c, err := h.db.GetCluster(r.Context(), rt.project, rt.location, rt.cluster)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, fromDriverCluster(c))
}

// listClusters handles GET .../clusters with pageToken/pageSize.
func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request, rt *route) {
	all, err := h.db.ListClusters(r.Context(), rt.project, rt.location)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, err := pagination.PaginateSorted(all,
		func(a, b mkdriver.Cluster) bool { return a.ID < b.ID },
		r.URL.Query().Get("pageToken"), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	items := make([]clusterJSON, 0, len(page.Items))
	for i := range page.Items {
		items = append(items, fromDriverCluster(&page.Items[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{
		"clusters":       items,
		nextPageTokenKey: page.NextPageToken,
	})
}

// updateCluster handles PATCH .../clusters/{c}?updateMask=. Only masked fields
// change; unknown, immutable and output-only paths are 400.
func (h *Handler) updateCluster(w http.ResponseWriter, r *http.Request, rt *route) {
	var body clusterJSON
	if !gcprest.DecodeOptionalJSON(w, r, &body) {
		return
	}

	c, op, err := h.db.UpdateCluster(r.Context(), toDriverCluster(&body, rt, rt.cluster),
		parseMask(r.URL.Query().Get("updateMask")))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeOperation(w, op, fromDriverCluster(c), clusterTypeURL)
}

// deleteCluster handles DELETE .../clusters/{c}; the LRO response is Empty.
func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request, rt *route) {
	op, err := h.db.DeleteCluster(r.Context(), rt.project, rt.location, rt.cluster)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeOperation(w, op, struct{}{}, emptyTypeURL)
}

// createTopic handles POST .../topics?topicId=; synchronous, returns the Topic.
func (h *Handler) createTopic(w http.ResponseWriter, r *http.Request, rt *route) {
	var body topicJSON
	if !gcprest.DecodeOptionalJSON(w, r, &body) {
		return
	}

	t, err := h.db.CreateTopic(r.Context(), toDriverTopic(&body, rt, r.URL.Query().Get(topicIDParam)))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, fromDriverTopic(t))
}

func (h *Handler) getTopic(w http.ResponseWriter, r *http.Request, rt *route) {
	t, err := h.db.GetTopic(r.Context(), rt.project, rt.location, rt.cluster, rt.topic)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, fromDriverTopic(t))
}

// listTopics handles GET .../topics with pageToken/pageSize.
func (h *Handler) listTopics(w http.ResponseWriter, r *http.Request, rt *route) {
	all, err := h.db.ListTopics(r.Context(), rt.project, rt.location, rt.cluster)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, err := pagination.PaginateSorted(all,
		func(a, b mkdriver.Topic) bool { return a.ID < b.ID },
		r.URL.Query().Get("pageToken"), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	items := make([]topicJSON, 0, len(page.Items))
	for i := range page.Items {
		items = append(items, fromDriverTopic(&page.Items[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{
		"topics":         items,
		nextPageTokenKey: page.NextPageToken,
	})
}

// updateTopic handles PATCH .../topics/{t}?updateMask=; synchronous.
func (h *Handler) updateTopic(w http.ResponseWriter, r *http.Request, rt *route) {
	var body topicJSON
	if !gcprest.DecodeOptionalJSON(w, r, &body) {
		return
	}

	t, err := h.db.UpdateTopic(r.Context(), toDriverTopic(&body, rt, rt.topic),
		parseMask(r.URL.Query().Get("updateMask")))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, fromDriverTopic(t))
}

// deleteTopic handles DELETE .../topics/{t}; synchronous, returns Empty.
func (h *Handler) deleteTopic(w http.ResponseWriter, r *http.Request, rt *route) {
	if err := h.db.DeleteTopic(r.Context(), rt.project, rt.location, rt.cluster, rt.topic); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{})
}

// writeIDTaken writes the 409 for a cluster id another service already uses
// on the shared clusters path. Real GCP keeps GKE, AlloyDB and Managed Kafka
// apart by hostname; the emulator serves them on one path, so it refuses the
// second cluster rather than making one of the two unreachable.
func writeIDTaken(w http.ResponseWriter, rt *route, id, owner string) {
	gcprest.WriteError(w, http.StatusConflict, "alreadyExists",
		"cluster "+clusterName(rt.project, rt.location, id)+" already exists: the id is used by "+owner+
			" cluster on this emulator, which serves GKE, AlloyDB and Managed Kafka clusters on one path")
}

// parseMask splits a comma-separated updateMask query param into field paths.
func parseMask(raw string) []string {
	var out []string

	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}

// pageSize reads ?pageSize, clamping to a sane default and ceiling.
func pageSize(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if err != nil || n <= 0 {
		return defaultPageSize
	}

	return min(n, maxPageSize)
}
