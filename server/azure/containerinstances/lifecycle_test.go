package containerinstances_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	"net/http/httptest"
)

func TestContainerGroupUpdateReturns200(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := httptest.NewServer(azureserver.New(azureserver.DriversFrom(cloud)))
	t.Cleanup(srv.Close)

	// First PUT creates → 201.
	doReq(t, srv.URL, http.MethodPut, groupURL("cg2")+apiVer, strings.NewReader(createBody), http.StatusCreated)
	// Second PUT is an in-place update → 200.
	doReq(t, srv.URL, http.MethodPut, groupURL("cg2")+apiVer, strings.NewReader(createBody), http.StatusOK)
}

func TestContainerGroupLifecycleVerbs(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := httptest.NewServer(azureserver.New(azureserver.DriversFrom(cloud)))
	t.Cleanup(srv.Close)

	doReq(t, srv.URL, http.MethodPut, groupURL("cg1")+apiVer, strings.NewReader(createBody), http.StatusCreated)

	// Stop → 204, group reports Stopped.
	doReq(t, srv.URL, http.MethodPost, groupURL("cg1")+"/stop"+apiVer, nil, http.StatusNoContent)

	got := decodeGroup(t, doReq(t, srv.URL, http.MethodGet, groupURL("cg1")+apiVer, nil, http.StatusOK))
	if got.Properties.InstanceView.State != "Stopped" {
		t.Fatalf("after stop, group state = %q, want Stopped", got.Properties.InstanceView.State)
	}

	// Start → 204, group reports Running again.
	doReq(t, srv.URL, http.MethodPost, groupURL("cg1")+"/start"+apiVer, nil, http.StatusNoContent)

	got = decodeGroup(t, doReq(t, srv.URL, http.MethodGet, groupURL("cg1")+apiVer, nil, http.StatusOK))
	if got.Properties.InstanceView.State != "Running" {
		t.Fatalf("after start, group state = %q, want Running", got.Properties.InstanceView.State)
	}

	// Restart → 204.
	doReq(t, srv.URL, http.MethodPost, groupURL("cg1")+"/restart"+apiVer, nil, http.StatusNoContent)

	// A lifecycle verb on a missing group → 404.
	doReq(t, srv.URL, http.MethodPost, groupURL("missing")+"/stop"+apiVer, nil, http.StatusNotFound)
}

func TestContainerExecReturnsSession(t *testing.T) {
	eng := &recordingEngine{logs: "out"}
	cloud := cloudemu.NewAzure(config.WithContainerEngine(eng))
	srv := httptest.NewServer(azureserver.New(azureserver.DriversFrom(cloud)))
	t.Cleanup(srv.Close)

	doReq(t, srv.URL, http.MethodPut, groupURL("cg1")+apiVer, strings.NewReader(createBody), http.StatusCreated)

	execBody := `{"command":"/bin/sh -c ls","terminalSize":{"rows":24,"cols":80}}`
	body := doReq(t, srv.URL, http.MethodPost,
		groupURL("cg1")+"/containers/app/exec"+apiVer, strings.NewReader(execBody), http.StatusOK)

	var got struct {
		WebSocketURI string `json:"webSocketUri"`
		Password     string `json:"password"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode exec response: %v", err)
	}

	if got.WebSocketURI == "" || !strings.HasPrefix(got.WebSocketURI, "wss://") {
		t.Fatalf("webSocketUri = %q, want a wss:// uri", got.WebSocketURI)
	}

	if got.Password == "" {
		t.Fatalf("password is empty")
	}

	// Exec on a container that isn't in the group → 404.
	doReq(t, srv.URL, http.MethodPost,
		groupURL("cg1")+"/containers/ghost/exec"+apiVer, strings.NewReader(execBody), http.StatusNotFound)
}

func TestPublicIPAddressAssigned(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := httptest.NewServer(azureserver.New(azureserver.DriversFrom(cloud)))
	t.Cleanup(srv.Close)

	body := `{
      "location": "westus2",
      "properties": {
        "osType": "Linux",
        "ipAddress": {"type": "Public", "dnsNameLabel": "myapp", "ports": [{"port": 80, "protocol": "TCP"}]},
        "containers": [{"name": "app", "properties": {"image": "nginx"}}]
      }
    }`

	raw := doReq(t, srv.URL, http.MethodPut, groupURL("pub")+apiVer, strings.NewReader(body), http.StatusCreated)

	var got struct {
		Properties struct {
			IPAddress struct {
				Type         string `json:"type"`
				IP           string `json:"ip"`
				FQDN         string `json:"fqdn"`
				DNSNameLabel string `json:"dnsNameLabel"`
			} `json:"ipAddress"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.Properties.IPAddress.IP == "" {
		t.Fatalf("public group got no assigned ip: %s", raw)
	}

	if got.Properties.IPAddress.FQDN != "myapp.westus2.azurecontainer.io" {
		t.Fatalf("fqdn = %q, want myapp.westus2.azurecontainer.io", got.Properties.IPAddress.FQDN)
	}
}
