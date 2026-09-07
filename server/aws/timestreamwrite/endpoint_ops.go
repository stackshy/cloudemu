package timestreamwrite

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

// endpointCachePeriodMinutes is the CachePeriodInMinutes reported for the
// discovered endpoint, matching real Timestream's 24-hour cache period.
const endpointCachePeriodMinutes = 1440

// registerEndpointRoutes wires the endpoint-discovery operation.
func (h *Handler) registerEndpointRoutes() {
	h.routes["DescribeEndpoints"] = h.describeEndpoints
}

type endpointJSON struct {
	Address              string `json:"Address"`
	CachePeriodInMinutes int64  `json:"CachePeriodInMinutes"`
}

type describeEndpointsResponse struct {
	Endpoints []endpointJSON `json:"Endpoints"`
}

// describeEndpoints answers the Timestream endpoint-discovery request. Real
// Timestream requires a client to resolve an ingest/query cell before calling
// the data operations; the emulator returns a single endpoint pointing at the
// request's own host, so a discovering client routes subsequent operations back
// here rather than at a real AWS cell. Clients configured with a custom
// endpoint (the emulator's endpoint override — how terraform-provider-aws and
// the aws-sdk-go-v2 client reach the emulator) skip discovery entirely and
// never call this; it is served for completeness and for discovering clients.
func (*Handler) describeEndpoints(w http.ResponseWriter, r *http.Request) {
	wire.WriteJSON(w, describeEndpointsResponse{
		Endpoints: []endpointJSON{
			{Address: r.Host, CachePeriodInMinutes: endpointCachePeriodMinutes},
		},
	})
}
