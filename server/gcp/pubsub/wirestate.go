package pubsub

import (
	"encoding/json"
	"time"

	gcppubsub "github.com/stackshy/cloudemu/v2/providers/gcp/pubsub"
)

var _ gcppubsub.WireState = (*Handler)(nil)

// wireAttacher is the provider Pub/Sub mock: it carries this handler's native
// state in its snapshot so serve --persist keeps it across a restart.
type wireAttacher interface {
	AttachWireState(ws gcppubsub.WireState) error
}

// wireState is the persisted form of the handler maps. Outstanding leases are
// not kept: after a restart an unacked message is redelivered, the same
// at-least-once outcome as a lapsed ack deadline.
type wireState struct {
	Topics    map[string]*wireTopic `json:"topics,omitempty"`
	Subs      map[string]*wireSub   `json:"subscriptions,omitempty"`
	Snapshots map[string]*wireSnap  `json:"snapshots,omitempty"`
	Counter   uint64                `json:"counter,omitempty"`
}

type wireMessage struct {
	ID          string            `json:"id"`
	Body        string            `json:"body"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	OrderingKey string            `json:"orderingKey,omitempty"`
	PublishTime time.Time         `json:"publishTime"`
}

type wireTopic struct {
	Messages             []wireMessage     `json:"messages,omitempty"`
	IAM                  *iamPolicy        `json:"iam,omitempty"`
	Labels               map[string]string `json:"labels,omitempty"`
	LabelsSet            bool              `json:"labelsSet,omitempty"`
	MsgRetentionDuration string            `json:"messageRetentionDuration,omitempty"`
	SchemaSettings       json.RawMessage   `json:"schemaSettings,omitempty"`
	KmsKeyName           string            `json:"kmsKeyName,omitempty"`
	MessageStoragePolicy json.RawMessage   `json:"messageStoragePolicy,omitempty"`
	SatisfiesPzs         bool              `json:"satisfiesPzs,omitempty"`
}

type wireSub struct {
	Config           subscription `json:"config"`
	Topic            string       `json:"topic"`
	CreateTime       time.Time    `json:"createTime"`
	Acked            map[int]bool `json:"acked,omitempty"`
	DeliveryAttempts map[int]int  `json:"deliveryAttempts,omitempty"`
	IAM              *iamPolicy   `json:"iam,omitempty"`
}

type wireSnap struct {
	Topic      string            `json:"topic"`
	Acked      map[int]bool      `json:"acked,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	CreateTime time.Time         `json:"createTime"`
	ExpireTime time.Time         `json:"expireTime"`
}

// ExportWire serializes the topics, subscriptions and snapshots this handler
// holds.
func (h *Handler) ExportWire() (json.RawMessage, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	ws := wireState{
		Topics:    make(map[string]*wireTopic, len(h.topics)),
		Subs:      make(map[string]*wireSub, len(h.subs)),
		Snapshots: make(map[string]*wireSnap, len(h.snapshots)),
		Counter:   h.ackCounter.Load(),
	}

	for k, ts := range h.topics {
		wt := &wireTopic{
			IAM: ts.iam, Labels: ts.labels, LabelsSet: ts.labelsSet,
			MsgRetentionDuration: ts.msgRetentionDuration, SchemaSettings: ts.schemaSettings,
			KmsKeyName: ts.kmsKeyName, MessageStoragePolicy: ts.messageStoragePolicy, SatisfiesPzs: ts.satisfiesPzs,
		}

		for i := range ts.messages {
			m := &ts.messages[i]
			wt.Messages = append(wt.Messages, wireMessage{
				ID: m.id, Body: m.body, Attributes: m.attributes, OrderingKey: m.orderingKey, PublishTime: m.publishTime,
			})
		}

		ws.Topics[k] = wt
	}

	for k, s := range h.subs {
		ws.Subs[k] = &wireSub{
			Config: s.cfg, Topic: s.topic, CreateTime: s.createTime,
			Acked: s.acked, DeliveryAttempts: s.deliveryAttempts, IAM: s.iam,
		}
	}

	for k, s := range h.snapshots {
		ws.Snapshots[k] = &wireSnap{
			Topic: s.topic, Acked: s.acked, Labels: s.labels, CreateTime: s.createTime, ExpireTime: s.expireTime,
		}
	}

	return json.Marshal(ws)
}

// ImportWire replaces the handler maps with a previously exported state.
func (h *Handler) ImportWire(data json.RawMessage) error {
	var ws wireState
	if err := json.Unmarshal(data, &ws); err != nil {
		return err
	}

	topics := make(map[string]*topicState, len(ws.Topics))

	for k, wt := range ws.Topics {
		ts := &topicState{
			iam: wt.IAM, labels: wt.Labels, labelsSet: wt.LabelsSet,
			msgRetentionDuration: wt.MsgRetentionDuration, schemaSettings: wt.SchemaSettings,
			kmsKeyName: wt.KmsKeyName, messageStoragePolicy: wt.MessageStoragePolicy, satisfiesPzs: wt.SatisfiesPzs,
		}

		for _, m := range wt.Messages {
			ts.messages = append(ts.messages, storedMessage{
				id: m.ID, body: m.Body, attributes: m.Attributes, orderingKey: m.OrderingKey, publishTime: m.PublishTime,
			})
		}

		topics[k] = ts
	}

	subs := make(map[string]*subState, len(ws.Subs))

	for k, s := range ws.Subs {
		// The filter was validated at create, so a parse error cannot occur for
		// state this handler exported.
		filter, _ := parseFilter(s.Config.Filter)

		subs[k] = &subState{
			cfg: s.Config, topic: s.Topic, createTime: s.CreateTime, iam: s.IAM, filter: filter,
			acked: orEmpty(s.Acked), deliveryAttempts: orEmpty(s.DeliveryAttempts),
			outstanding: make(map[string]*lease),
		}
	}

	snaps := make(map[string]*snapState, len(ws.Snapshots))

	for k, s := range ws.Snapshots {
		snaps[k] = &snapState{
			topic: s.Topic, acked: orEmpty(s.Acked), labels: s.Labels, createTime: s.CreateTime, expireTime: s.ExpireTime,
		}
	}

	h.mu.Lock()
	h.topics, h.subs, h.snapshots = topics, subs, snaps
	h.ackCounter.Store(ws.Counter)
	h.mu.Unlock()

	return nil
}

func orEmpty[V any](m map[int]V) map[int]V {
	if m == nil {
		return make(map[int]V)
	}

	return m
}
