package serverkit

import "net"

// loopbackIPv4 is advertised for the Kubernetes data plane when the server binds
// all interfaces (a published -p 4570:4570 maps to it from the host).
const loopbackIPv4 = "127.0.0.1"

// advertiseHostFor resolves the host the Kubernetes data plane advertises to
// clients: an explicit advertise host wins; otherwise the bind host, unless that
// binds all interfaces (0.0.0.0 / :: / empty — e.g. Docker), in which case
// loopback. The bind host isn't always reachable: under Docker the container
// binds 0.0.0.0, but 0.0.0.0 is not a connectable address — a kubeconfig
// advertising https://0.0.0.0:4570 with a 0.0.0.0-only cert SAN can't be dialed
// or TLS-verified from the host.
func advertiseHostFor(advertiseHost, bindHost string) string {
	if advertiseHost != "" {
		return advertiseHost
	}

	if isAllInterfaces(bindHost) {
		return loopbackIPv4
	}

	return bindHost
}

// isAllInterfaces reports whether h is a bind-all address (not connectable).
func isAllInterfaces(h string) bool {
	switch h {
	case "", "0.0.0.0", "::", "[::]":
		return true
	}

	if ip := net.ParseIP(h); ip != nil {
		return ip.IsUnspecified()
	}

	return false
}

// isLoopbackHost reports whether binding to h keeps the server local-only.
func isLoopbackHost(h string) bool {
	switch h {
	case "127.0.0.1", "::1", "localhost":
		return true
	}

	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}

	return false
}

// k8sCertHosts is the SAN set for the Kubernetes serving cert: the advertised
// host (what clients dial and verify), the loopback names, and any extra
// --tls-host entries, de-duplicated.
func k8sCertHosts(advertiseHost string, extra []string) []string {
	base := []string{advertiseHost, "localhost", loopbackIPv4}

	seen := map[string]bool{}
	out := make([]string, 0, len(base)+len(extra))

	for _, h := range append(base, extra...) {
		if h == "" || seen[h] {
			continue
		}

		seen[h] = true

		out = append(out, h)
	}

	return out
}
