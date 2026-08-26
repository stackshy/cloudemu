package servicebus

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

func (h *Handler) serveQueue(w http.ResponseWriter, r *http.Request, sp sbPath) {
	name := ""
	if len(sp.segs) >= namePairLen {
		name = sp.segs[1]
	}

	if name == "" {
		h.listQueues(w, r, sp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createQueue(w, r, sp, name)
	case http.MethodGet:
		h.getQueue(w, sp, name)
	case http.MethodDelete:
		h.deleteQueue(w, sp, name)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createQueue(w http.ResponseWriter, r *http.Request, sp sbPath, name string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req createQueueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}

	h.mu.Lock()

	ns, ok := h.getNS(sp)
	if !ok {
		h.mu.Unlock()
		writeNSNotFound(w, sp.namespace)

		return
	}

	now := time.Now().UTC()
	lockSeconds := lockDurationSeconds(req.Properties.LockDuration)

	rec, existed := ns.Queues[name]
	if !existed {
		dlqURL, err := h.provisionDLQ(r, sp.namespace+"/"+name)
		if err != nil {
			h.mu.Unlock()
			azurearm.WriteCErr(w, err)

			return
		}

		info, err := h.mq.CreateQueue(r.Context(), mqdriver.QueueConfig{
			Name:              sp.namespace + "/" + name,
			VisibilityTimeout: lockSeconds,
			DeadLetterQueue: &mqdriver.DeadLetterConfig{
				TargetQueueURL:  dlqURL,
				MaxReceiveCount: effectiveMaxDeliveryCount(&req.Properties),
			},
			DeadLetterOnExpiration: req.Properties.DeadLetteringOnExpiration,
		})
		if err != nil && !cerrors.IsAlreadyExists(err) {
			h.mu.Unlock()
			azurearm.WriteCErr(w, err)

			return
		}

		url := ""
		if info != nil {
			url = info.URL
		}

		rec = &queueRecord{Name: name, DriverURL: url, DLQURL: dlqURL, CreatedAt: now}
		ns.Queues[name] = rec
	} else if rec.DriverURL != "" {
		// PUT is create-or-update: propagate a LockDuration, MaxDeliveryCount or
		// deadLetteringOnMessageExpiration change onto the backing store so, e.g.,
		// lowering maxDeliveryCount dead-letters at the new threshold.
		_ = h.mq.SetQueueAttributes(r.Context(), rec.DriverURL, map[string]int{
			"VisibilityTimeout":      lockSeconds,
			"MaxDeliveryCount":       effectiveMaxDeliveryCount(&req.Properties),
			"DeadLetterOnExpiration": boolToInt(req.Properties.DeadLetteringOnExpiration),
		})
	}

	rec.Props = buildQueueProps(&req.Properties, rec.CreatedAt, now)
	rec.UpdatedAt = now

	resource := h.toQueueResource(sp, rec)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) getQueue(w http.ResponseWriter, sp sbPath, name string) {
	h.mu.RLock()

	ns, ok := h.getNS(sp)
	if !ok {
		h.mu.RUnlock()
		writeNSNotFound(w, sp.namespace)

		return
	}

	rec, ok := ns.Queues[name]
	if !ok {
		h.mu.RUnlock()
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "queue not found: "+name)

		return
	}

	resource := h.toQueueResource(sp, rec)
	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) listQueues(w http.ResponseWriter, r *http.Request, sp sbPath) {
	h.listChildren(w, r, sp, func(ns *namespaceState) []any {
		out := make([]any, 0, len(ns.Queues))
		for _, n := range sortedKeys(ns.Queues) {
			out = append(out, h.toQueueResource(sp, ns.Queues[n]))
		}

		return out
	})
}

func (h *Handler) deleteQueue(w http.ResponseWriter, sp sbPath, name string) {
	h.mu.Lock()

	ns, ok := h.getNS(sp)
	if !ok {
		h.mu.Unlock()
		writeNSNotFound(w, sp.namespace)

		return
	}

	rec, ok := ns.Queues[name]
	if !ok {
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

		return
	}

	url := rec.DriverURL
	dlqURL := rec.DLQURL

	delete(ns.Queues, name)
	h.mu.Unlock()

	if url != "" {
		_ = h.mq.DeleteQueue(context.Background(), url)
	}

	h.deleteBackingQueue(dlqURL)

	w.WriteHeader(http.StatusOK)
}

// provisionDLQ creates the backing store for an entity's $DeadLetterQueue
// sub-queue and returns its driver URL. The DLQ store carries no onward
// dead-lettering of its own. Callers hold h.mu.
func (h *Handler) provisionDLQ(r *http.Request, entityName string) (string, error) {
	info, err := h.mq.CreateQueue(r.Context(), mqdriver.QueueConfig{Name: entityName + "/" + dlqSuffix})
	if err != nil && !cerrors.IsAlreadyExists(err) {
		return "", err
	}

	if info != nil {
		return info.URL, nil
	}

	return "", nil
}

// effectiveMaxDeliveryCount is the delivery-attempt ceiling after which a
// message dead-letters, defaulting to Service Bus' documented 10.
func effectiveMaxDeliveryCount(p *queueProperties) int {
	if p.MaxDeliveryCount > 0 {
		return int(p.MaxDeliveryCount)
	}

	return defaultMaxDeliveryCount
}

// buildQueueProps synthesizes the server-computed fields and defaults a real
// Service Bus queue reports when the client omits them.
func buildQueueProps(in *queueProperties, created, updated time.Time) queueProperties {
	out := *in
	out.Status = statusActive

	if out.LockDuration == "" {
		out.LockDuration = defaultLockDuration
	}

	if out.DefaultMessageTimeToLive == "" {
		out.DefaultMessageTimeToLive = maxTimeToLive
	}

	if out.MaxDeliveryCount == 0 {
		out.MaxDeliveryCount = defaultMaxDeliveryCount
	}

	if out.MaxSizeInMegabytes == 0 {
		out.MaxSizeInMegabytes = defaultMaxSizeMB
	}

	out.CountDetails = &countDetails{}
	out.CreatedAt = &created
	out.UpdatedAt = &updated
	out.AccessedAt = &updated

	return out
}

func (h *Handler) toQueueResource(sp sbPath, rec *queueRecord) queueResource {
	props := rec.Props

	if info, err := h.mq.GetQueueInfo(context.Background(), rec.DriverURL); err == nil && info != nil {
		props.MessageCount = int64(info.ApproxMessageCount)
		props.CountDetails = &countDetails{ActiveMessageCount: int64(info.ApproxMessageCount)}
	}

	return queueResource{
		ID: azurearm.BuildResourceID(sp.sub, sp.rg, providerName, resourceType, sp.namespace) +
			"/queues/" + rec.Name,
		Name:       rec.Name,
		Type:       providerName + "/Namespaces/Queues",
		Properties: props,
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}
