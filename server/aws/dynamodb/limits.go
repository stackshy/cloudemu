package dynamodb

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

// DynamoDB's default provisioned-capacity ceilings, reported by DescribeLimits.
// These are the real per-account and per-table defaults an unmodified account
// carries, so an SDK caller reading them sees the documented values.
const (
	accountMaxCapacityUnits = 80000
	tableMaxCapacityUnits   = 40000
)

// endpointCachePeriodMinutes is the CachePeriodInMinutes DynamoDB advertises for
// its endpoint-discovery entry.
const endpointCachePeriodMinutes = 1440

// routeLimits dispatches the account-level DescribeLimits and DescribeEndpoints
// operations, which carry no per-table state.
func (*Handler) routeLimits(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "DescribeLimits":
		describeLimits(w)
	case "DescribeEndpoints":
		describeEndpoints(w, r)
	default:
		return false
	}

	return true
}

// describeLimits reports the account's provisioned-capacity ceilings.
func describeLimits(w http.ResponseWriter) {
	wire.WriteJSON(w, map[string]any{
		"AccountMaxReadCapacityUnits":  accountMaxCapacityUnits,
		"AccountMaxWriteCapacityUnits": accountMaxCapacityUnits,
		"TableMaxReadCapacityUnits":    tableMaxCapacityUnits,
		"TableMaxWriteCapacityUnits":   tableMaxCapacityUnits,
	})
}

// describeEndpoints reports the request's own host as the DynamoDB endpoint, so
// an endpoint-discovery-enabled client keeps talking to the emulator.
func describeEndpoints(w http.ResponseWriter, r *http.Request) {
	wire.WriteJSON(w, map[string]any{
		"Endpoints": []map[string]any{{
			"Address":              r.Host,
			"CachePeriodInMinutes": endpointCachePeriodMinutes,
		}},
	})
}
