package kubernetes

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/httpstream/wsstream"
	"k8s.io/apimachinery/pkg/util/remotecommand"
)

// execStreamIdleTimeout bounds an idle synthetic exec/attach session so a stuck
// client can't hold the hijacked connection open forever.
const execStreamIdleTimeout = 30 * time.Second

// serveExecAttach handles pods/exec and pods/attach. cloudemu has no container
// runtime, so the session is a deterministic fake: it completes the WebSocket
// upgrade, writes a self-describing banner (and, for exec, echoes the command)
// on stdout, then reports exit 0 via a Success Status on the error channel.
//
// Only the WebSocket streaming protocol is served. A non-WebSocket (SPDY-only)
// client gets a clean typed Status rather than a hijack panic or a raw 501.
func serveExecAttach(w http.ResponseWriter, r *http.Request, route *Route) {
	if !wsstream.IsWebSocketRequest(r) {
		writeStatus(w, http.StatusBadRequest, metav1.StatusReasonBadRequest,
			"k8s api: pods/"+route.Subresource+
				" requires a WebSocket streaming upgrade (v5.channel.k8s.io); the SPDY protocol is not supported")

		return
	}

	runSyntheticExecSession(w, r, route)
}

// execChannelProtocols builds the remotecommand channel protocol map: stdin=0,
// stdout=1, stderr=2, error=3, resize=4. It adds v4/v5.channel.k8s.io, which
// wsstream.NewDefaultChannelProtocols omits (it registers only ""/channel.k8s.io/
// base64.channel.k8s.io), so kubectl >=1.29 and Helm v4 — which negotiate the
// WebSocket v5 CLOSE protocol — are honored.
func execChannelProtocols() map[string]wsstream.ChannelProtocolConfig {
	channels := []wsstream.ChannelType{
		remotecommand.StreamStdIn:  wsstream.ReadChannel,
		remotecommand.StreamStdOut: wsstream.WriteChannel,
		remotecommand.StreamStdErr: wsstream.WriteChannel,
		remotecommand.StreamErr:    wsstream.WriteChannel,
		remotecommand.StreamResize: wsstream.ReadChannel,
	}

	protocols := wsstream.NewDefaultChannelProtocols(channels)
	binary := wsstream.ChannelProtocolConfig{Binary: true, Channels: channels}
	protocols[remotecommand.StreamProtocolV4Name] = binary
	protocols[remotecommand.StreamProtocolV5Name] = binary

	return protocols
}

// runSyntheticExecSession completes the WebSocket handshake and runs the
// deterministic fake session. The bytes written are identical on every run.
func runSyntheticExecSession(w http.ResponseWriter, r *http.Request, route *Route) {
	conn := wsstream.NewConn(execChannelProtocols())
	conn.SetIdleTimeout(execStreamIdleTimeout)
	defer conn.Close()

	_, streams, err := conn.Open(w, r)
	if err != nil {
		// The upgrade failed (e.g. an unsupported subprotocol). The ws server
		// already wrote the rejection, so there is nothing typed to add.
		return
	}

	// Drain stdin and resize so an interactive client isn't blocked — there is no
	// container to receive them. Both unblock when conn.Close closes the streams.
	go func() { _, _ = io.Copy(io.Discard, streams[remotecommand.StreamStdIn]) }()
	go func() { _, _ = io.Copy(io.Discard, streams[remotecommand.StreamResize]) }()

	writeExecBanner(streams[remotecommand.StreamStdOut], r, route)

	// A Success Status on the error channel is the exit-0 signal remotecommand
	// clients wait for, so `kubectl exec` returns 0.
	writeExecStatus(streams[remotecommand.StreamErr])
}

// writeExecBanner writes the self-describing banner (mirroring the pods/log
// synthetic marker) and, for exec, echoes the requested command so the session
// is visibly emulated rather than passing as a real container exec.
func writeExecBanner(stdout io.Writer, r *http.Request, route *Route) {
	_, _ = fmt.Fprintf(stdout,
		"cloudemu: synthetic %s session for pod %s/%s — no real container runtime; command echoed, no on-disk state\n",
		route.Subresource, route.Namespace, route.Name)

	if route.Subresource != subresourcePodExec {
		return
	}

	if cmd := r.URL.Query()["command"]; len(cmd) > 0 {
		_, _ = fmt.Fprintf(stdout, "cloudemu: exec %s\n", strings.Join(cmd, " "))
	}
}

// writeExecStatus writes a Success metav1.Status to the error channel — the
// remotecommand exit-0 signal.
func writeExecStatus(errStream io.Writer) {
	data, err := json.Marshal(&metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusSuccess,
	})
	if err != nil {
		return
	}

	_, _ = errStream.Write(data)
}
