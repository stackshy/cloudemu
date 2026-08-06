package kubernetes

import "net/http"

// dryRunQueryValue is the value kubectl / client-go send for a server-side
// dry-run (`?dryRun=All`, from `kubectl apply|create|delete --dry-run=server`).
const dryRunQueryValue = "All"

// isDryRun reports whether the request is a server-side dry-run. A dry-run write
// runs the same name/namespace/conflict validation and defaulting as a real
// write, then echoes the object the server would have stored WITHOUT persisting
// it, bumping any resourceVersion counter, running reconcile, or emitting watch
// events. Controllers do not run during a real apiserver dry-run either, so
// skipping reconcile keeps the echoed object faithful (no synthetic status).
func isDryRun(r *http.Request) bool {
	return r.URL.Query().Get("dryRun") == dryRunQueryValue
}
