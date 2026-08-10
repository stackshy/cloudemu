package opensearch

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// serveCrossCluster routes /opensearch/cc/* (cross-cluster connections).
func (h *Handler) serveCrossCluster(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch rest[0] {
	case "outboundConnection":
		h.serveOutbound(w, r, rest[1:])
	case "inboundConnection":
		h.serveInbound(w, r, rest[1:])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveOutbound routes /cc/outboundConnection/*.
func (h *Handler) serveOutbound(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		if r.Method == http.MethodPost {
			h.createOutboundConnection(w, r)

			return
		}

		methodNotAllowed(w)

		return
	}

	if len(rest) == 1 && rest[0] == segSearch && r.Method == http.MethodPost {
		h.describeOutboundConnections(w, r)

		return
	}

	if len(rest) == 1 && r.Method == http.MethodDelete {
		h.deleteOutboundConnection(w, r, rest[0])

		return
	}

	notFoundPath(w, r.URL.Path)
}

// serveInbound routes /cc/inboundConnection/*.
func (h *Handler) serveInbound(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 1 && rest[0] == segSearch && r.Method == http.MethodPost {
		h.describeInboundConnections(w, r)

		return
	}

	if len(rest) == 1 && r.Method == http.MethodDelete {
		h.deleteInboundConnection(w, r, rest[0])

		return
	}

	const wantSegs = 2
	if len(rest) == wantSegs && r.Method == http.MethodPut {
		switch rest[1] {
		case "accept":
			h.acceptInboundConnection(w, r, rest[0])

			return
		case "reject":
			h.rejectInboundConnection(w, r, rest[0])

			return
		}
	}

	notFoundPath(w, r.URL.Path)
}

func (h *Handler) createOutboundConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LocalDomainInfo  domainInfoWire `json:"LocalDomainInfo"`
		RemoteDomainInfo domainInfoWire `json:"RemoteDomainInfo"`
		ConnectionAlias  string         `json:"ConnectionAlias"`
		ConnectionMode   string         `json:"ConnectionMode"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	out, err := h.os.CreateOutboundConnection(r.Context(), driver.CreateOutboundConnectionInput{
		LocalDomain:     req.LocalDomainInfo.toDriver(),
		RemoteDomain:    req.RemoteDomainInfo.toDriver(),
		ConnectionAlias: req.ConnectionAlias,
		ConnectionMode:  req.ConnectionMode,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, outboundToWire(out))
}

func (h *Handler) deleteOutboundConnection(w http.ResponseWriter, r *http.Request, id string) {
	out, err := h.os.DeleteOutboundConnection(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"Connection": outboundToWire(out)})
}

//nolint:dupl // structurally mirrors describeInboundConnections but calls a distinct driver op + wire renderer.
func (h *Handler) describeOutboundConnections(w http.ResponseWriter, r *http.Request) {
	page := pageFromBody(w, r)
	if page == nil {
		return
	}

	list, next, err := h.os.DescribeOutboundConnections(r.Context(), *page)
	if err != nil {
		writeErr(w, err)

		return
	}

	conns := make([]map[string]any, 0, len(list))
	for i := range list {
		conns = append(conns, outboundToWire(&list[i]))
	}

	writeJSON(w, withNext(map[string]any{"Connections": conns}, next))
}

func (h *Handler) acceptInboundConnection(w http.ResponseWriter, r *http.Request, id string) {
	out, err := h.os.AcceptInboundConnection(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"Connection": inboundToWire(out)})
}

func (h *Handler) rejectInboundConnection(w http.ResponseWriter, r *http.Request, id string) {
	out, err := h.os.RejectInboundConnection(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"Connection": inboundToWire(out)})
}

func (h *Handler) deleteInboundConnection(w http.ResponseWriter, r *http.Request, id string) {
	out, err := h.os.DeleteInboundConnection(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"Connection": inboundToWire(out)})
}

//nolint:dupl // structurally mirrors describeOutboundConnections but calls a distinct driver op + wire renderer.
func (h *Handler) describeInboundConnections(w http.ResponseWriter, r *http.Request) {
	page := pageFromBody(w, r)
	if page == nil {
		return
	}

	list, next, err := h.os.DescribeInboundConnections(r.Context(), *page)
	if err != nil {
		writeErr(w, err)

		return
	}

	conns := make([]map[string]any, 0, len(list))
	for i := range list {
		conns = append(conns, inboundToWire(&list[i]))
	}

	writeJSON(w, withNext(map[string]any{"Connections": conns}, next))
}

// domainInfoWire is the AWSDomainInformation envelope on the wire.
type domainInfoWire struct {
	AWSDomainInformation struct {
		OwnerID    string `json:"OwnerId"`
		DomainName string `json:"DomainName"`
		Region     string `json:"Region"`
	} `json:"AWSDomainInformation"`
}

func (d domainInfoWire) toDriver() driver.ConnectionEndpoint {
	return driver.ConnectionEndpoint{
		OwnerID:    d.AWSDomainInformation.OwnerID,
		DomainName: d.AWSDomainInformation.DomainName,
		Region:     d.AWSDomainInformation.Region,
	}
}

// pageFromBody reads a {Filters, MaxResults, NextToken} search body and returns
// the page. It writes an error and returns nil on decode failure.
func pageFromBody(w http.ResponseWriter, r *http.Request) *driver.Page {
	var req struct {
		MaxResults int32  `json:"MaxResults"`
		NextToken  string `json:"NextToken"`
	}

	if !decodeJSON(w, r, &req) {
		return nil
	}

	return &driver.Page{NextToken: req.NextToken, MaxResults: req.MaxResults}
}
