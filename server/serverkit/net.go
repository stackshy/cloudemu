package serverkit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/features/topology"
)

// serveCanConnect answers GET /_cloudemu/net/can-connect?from&to&port&protocol
// with the engine's ConnectivityResult as JSON.
func serveCanConnect(w http.ResponseWriter, r *http.Request, eng *topology.Engine) {
	q := r.URL.Query()

	from, to := q.Get("from"), q.Get("to")
	if from == "" || to == "" {
		writeNetErr(w, http.StatusBadRequest, "from and to instance IDs are required")

		return
	}

	port, err := netPort(q.Get("port"))
	if err != nil {
		writeNetErr(w, http.StatusBadRequest, err.Error())

		return
	}

	proto := q.Get("protocol")
	if proto == "" {
		proto = "tcp"
	}

	res, err := eng.CanConnect(r.Context(), topology.ConnectivityQuery{
		SrcInstanceID: from, DstInstanceID: to, Port: port, Protocol: proto,
	})
	if err != nil {
		writeNetErr(w, http.StatusBadRequest, err.Error())

		return
	}

	writeNetJSON(w, res)
}

// serveTrace answers GET /_cloudemu/net/trace?from&to (to is a destination IP)
// with the route hops as JSON.
func serveTrace(w http.ResponseWriter, r *http.Request, eng *topology.Engine) {
	q := r.URL.Query()

	from, dest := q.Get("from"), q.Get("to")
	if from == "" || dest == "" {
		writeNetErr(w, http.StatusBadRequest, "from instance ID and to IP are required")

		return
	}

	hops, err := eng.TraceRoute(r.Context(), from, dest)
	if err != nil {
		writeNetErr(w, http.StatusBadRequest, err.Error())

		return
	}

	writeNetJSON(w, map[string]any{"hops": hops})
}

// netPort parses an optional port query value (empty → 0 = any).
func netPort(s string) (int, error) {
	if s == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q: %w", s, err)
	}

	return n, nil
}

func writeNetJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")

	b, err := json.Marshal(v)
	if err != nil {
		writeNetErr(w, http.StatusInternalServerError, err.Error())

		return
	}

	_, _ = w.Write(b)
}

func writeNetErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
