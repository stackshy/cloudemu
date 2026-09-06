package servicedirectory

import sddriver "github.com/stackshy/cloudemu/v2/services/servicedirectory/driver"

// cloneStrMap deep-copies a string label/annotation map so a stored value is
// never aliased by one handed back to a caller. An empty map clones to nil so
// round-tripped resources compare cleanly.
func cloneStrMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

// cloneNamespace returns a deep copy of ns.
func cloneNamespace(ns *sddriver.Namespace) sddriver.Namespace {
	out := *ns
	out.Labels = cloneStrMap(ns.Labels)

	return out
}

// cloneService returns a deep copy of svc.
func cloneService(svc *sddriver.Service) sddriver.Service {
	out := *svc
	out.Annotations = cloneStrMap(svc.Annotations)

	return out
}

// cloneEndpoint returns a deep copy of ep.
func cloneEndpoint(ep *sddriver.Endpoint) sddriver.Endpoint {
	out := *ep
	out.Annotations = cloneStrMap(ep.Annotations)

	return out
}
