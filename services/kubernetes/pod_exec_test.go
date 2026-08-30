// Tests for the WebSocket pods/exec and pods/attach subresources: kubectl exec
// runs a deterministic synthetic session (self-describing banner + command echo
// on stdout, a Success Status on the error channel = exit 0) instead of 501ing.

package kubernetes_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
	"k8s.io/apimachinery/pkg/util/remotecommand"
)

// dialExec opens a WebSocket exec/attach stream against the fixture and returns
// the per-channel bytes read until the server closes the connection.
func dialExec(t *testing.T, base, path, protocol string) map[byte][]byte {
	t.Helper()

	wsURL := strings.Replace(base+path, "http://", "ws://", 1)

	cfg, err := websocket.NewConfig(wsURL, "http://localhost")
	if err != nil {
		t.Fatalf("websocket config: %v", err)
	}
	cfg.Protocol = []string{protocol}

	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer ws.Close()

	if got := ws.Config().Protocol; len(got) != 1 || got[0] != protocol {
		t.Fatalf("negotiated subprotocol: got %v, want [%s]", got, protocol)
	}

	_ = ws.SetDeadline(time.Now().Add(5 * time.Second))

	frames := map[byte][]byte{}
	for {
		var data []byte
		if err := websocket.Message.Receive(ws, &data); err != nil {
			break // server closed the stream (deterministic session complete)
		}
		if len(data) == 0 {
			continue
		}
		frames[data[0]] = append(frames[data[0]], data[1:]...)
	}

	return frames
}

func TestPodExec_WebSocketSyntheticSession(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	createPodNamed(t, base, "wsexec")

	path := "/api/v1/namespaces/default/pods/wsexec/exec" +
		"?command=echo&command=hello&stdout=true&stderr=true&stdin=true"
	frames := dialExec(t, base, path, remotecommand.StreamProtocolV5Name)

	stdout := string(frames[remotecommand.StreamStdOut])
	if !strings.Contains(stdout, "cloudemu: synthetic exec session") {
		t.Fatalf("stdout missing self-describing banner: %q", stdout)
	}
	if !strings.Contains(stdout, "cloudemu: exec echo hello") {
		t.Fatalf("stdout missing echoed command: %q", stdout)
	}

	assertSuccessStatus(t, frames[remotecommand.StreamErr])
}

func TestPodAttach_WebSocketSyntheticSession(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	createPodNamed(t, base, "wsattach")

	path := "/api/v1/namespaces/default/pods/wsattach/attach?stdout=true&stderr=true"
	frames := dialExec(t, base, path, remotecommand.StreamProtocolV5Name)

	stdout := string(frames[remotecommand.StreamStdOut])
	if !strings.Contains(stdout, "cloudemu: synthetic attach session") {
		t.Fatalf("attach stdout missing self-describing banner: %q", stdout)
	}
	// attach has no command, so it must not echo an exec line.
	if strings.Contains(stdout, "cloudemu: exec") {
		t.Fatalf("attach stdout unexpectedly echoed a command: %q", stdout)
	}

	assertSuccessStatus(t, frames[remotecommand.StreamErr])
}

// The v4 subprotocol (kubectl < 1.29) must also be honored — v4/v5 are the ones
// wsstream.NewDefaultChannelProtocols omits and we register by hand.
func TestPodExec_WebSocketV4Protocol(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	createPodNamed(t, base, "wsexecv4")

	path := "/api/v1/namespaces/default/pods/wsexecv4/exec?command=ls&stdout=true"
	frames := dialExec(t, base, path, remotecommand.StreamProtocolV4Name)

	assertSuccessStatus(t, frames[remotecommand.StreamErr])
}

// Determinism: two identical exec requests produce byte-identical output.
func TestPodExec_Deterministic(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	createPodNamed(t, base, "wsdet")

	path := "/api/v1/namespaces/default/pods/wsdet/exec?command=whoami&stdout=true"
	first := dialExec(t, base, path, remotecommand.StreamProtocolV5Name)
	second := dialExec(t, base, path, remotecommand.StreamProtocolV5Name)

	for _, ch := range []byte{remotecommand.StreamStdOut, remotecommand.StreamErr} {
		if string(first[ch]) != string(second[ch]) {
			t.Fatalf("channel %d not deterministic:\n  %q\n  %q", ch, first[ch], second[ch])
		}
	}
}

func assertSuccessStatus(t *testing.T, errChannel []byte) {
	t.Helper()

	if len(errChannel) == 0 {
		t.Fatalf("error channel empty: want a Success Status")
	}

	var status struct {
		Kind   string `json:"kind"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(errChannel, &status); err != nil {
		t.Fatalf("error channel is not a JSON Status: %v (%q)", err, string(errChannel))
	}

	if status.Kind != "Status" || status.Status != "Success" {
		t.Fatalf("exec exit signal: got kind=%q status=%q, want Status/Success", status.Kind, status.Status)
	}
}
