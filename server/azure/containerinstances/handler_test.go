package containerinstances_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	subID  = "00000000-0000-0000-0000-000000000000"
	rgName = "demo"
	apiVer = "?api-version=2023-05-01"
)

// recordingEngine is a config.ContainerEngine that records calls and returns
// canned per-container statuses, so the wire+provider plumbing is exercised
// end-to-end without Docker.
type recordingEngine struct {
	ran     []config.ContainerRunSpec
	logs    string
	stopped []string
}

func (e *recordingEngine) Run(_ context.Context, spec config.ContainerRunSpec) (string, error) {
	e.ran = append(e.ran, spec)

	return "handle-1", nil
}

func (e *recordingEngine) Status(_ context.Context, _ string) ([]config.ContainerStatus, error) {
	return []config.ContainerStatus{{Name: "app", State: "exited", ExitCode: 42}}, nil
}

func (e *recordingEngine) Logs(_ context.Context, _, _ string, _ int) (string, error) {
	return e.logs, nil
}

func (e *recordingEngine) Exec(_ context.Context, _, _ string, _ []string) (config.ExecResult, error) {
	return config.ExecResult{}, nil
}

func (e *recordingEngine) Stop(_ context.Context, handle string) error {
	e.stopped = append(e.stopped, handle)

	return nil
}

func groupURL(name string) string {
	return groupURLInRG(rgName, name)
}

func groupURLInRG(rg, name string) string {
	return "/subscriptions/" + subID + "/resourceGroups/" + rg +
		"/providers/Microsoft.ContainerInstance/containerGroups/" + name
}

const createBody = `{
  "location": "eastus",
  "properties": {
    "osType": "Linux",
    "restartPolicy": "Never",
    "containers": [
      {
        "name": "app",
        "properties": {
          "image": "busybox:latest",
          "command": ["echo", "hi"],
          "environmentVariables": [{"name": "FOO", "value": "bar"}],
          "resources": {"requests": {"cpu": 1, "memoryInGB": 1.5}}
        }
      }
    ]
  }
}`

func TestContainerGroupLifecycleDrivesEngine(t *testing.T) {
	eng := &recordingEngine{logs: "hello from container"}
	cloud := cloudemu.NewAzure(config.WithContainerEngine(eng))
	srv := httptest.NewServer(azureserver.New(azureserver.DriversFrom(cloud)))
	t.Cleanup(srv.Close)

	// 1. PUT create — the engine runs the container.
	body := doReq(t, srv.URL, http.MethodPut, groupURL("cg1")+apiVer,
		strings.NewReader(createBody), http.StatusCreated)

	if len(eng.ran) != 1 {
		t.Fatalf("engine should run once on create, got %d", len(eng.ran))
	}

	if !eng.ran[0].RunToCompletion {
		t.Fatalf("Never restart policy should run to completion")
	}

	// The create response reflects the real exited state and exit code.
	created := decodeGroup(t, body)
	state := created.Properties.Containers[0].Properties.InstanceView.CurrentState
	if state.State != "Terminated" || state.ExitCode == nil || *state.ExitCode != 42 {
		t.Fatalf("create response state = %+v, want Terminated exit 42", state)
	}

	if created.Properties.InstanceView.State != "Succeeded" {
		t.Fatalf("group instanceView.state = %q, want Succeeded", created.Properties.InstanceView.State)
	}

	// 2. GET surfaces the same real state.
	got := decodeGroup(t, doReq(t, srv.URL, http.MethodGet, groupURL("cg1")+apiVer, nil, http.StatusOK))
	if got.Properties.Containers[0].Properties.InstanceView.CurrentState.State != "Terminated" {
		t.Fatalf("GET did not surface terminated state: %+v", got.Properties.Containers[0])
	}

	// 3. Logs sub-path returns the engine output.
	logsURL := groupURL("cg1") + "/containers/app/logs" + apiVer + "&tail=10"
	logsBody := doReq(t, srv.URL, http.MethodGet, logsURL, nil, http.StatusOK)

	var logs struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(logsBody, &logs); err != nil {
		t.Fatalf("decode logs: %v", err)
	}

	if logs.Content != "hello from container" {
		t.Fatalf("logs content = %q, want engine output", logs.Content)
	}

	// 4. DELETE tears down the engine workload.
	doReq(t, srv.URL, http.MethodDelete, groupURL("cg1")+apiVer, nil, http.StatusOK)

	if len(eng.stopped) != 1 || eng.stopped[0] != "handle-1" {
		t.Fatalf("engine workload not stopped on delete: %v", eng.stopped)
	}

	// 5. GET after delete → 404.
	doReq(t, srv.URL, http.MethodGet, groupURL("cg1")+apiVer, nil, http.StatusNotFound)
}

// TestContainerGroupsIsolatedAcrossResourceGroups is a regression test for the
// cross-RG collision fix: a container group's ARM identity is
// {subscriptionId, resourceGroupName, containerGroupName} — see
// https://learn.microsoft.com/en-us/rest/api/container-instances/container-groups/create-or-update
// — so two resource groups can each hold a same-named group without one
// aliasing or leaking into the other.
func TestContainerGroupsIsolatedAcrossResourceGroups(t *testing.T) {
	const otherRG = "other-rg"

	cloud := cloudemu.NewAzure()
	srv := httptest.NewServer(azureserver.New(azureserver.DriversFrom(cloud)))
	t.Cleanup(srv.Close)

	// Same group name ("shared") created in two different resource groups.
	doReq(t, srv.URL, http.MethodPut, groupURLInRG(rgName, "shared")+apiVer,
		strings.NewReader(createBody), http.StatusCreated)
	doReq(t, srv.URL, http.MethodPut, groupURLInRG(otherRG, "shared")+apiVer,
		strings.NewReader(strings.Replace(createBody, `"location": "eastus"`, `"location": "westus2"`, 1)),
		http.StatusCreated)

	// GET in each RG returns that RG's own group, not the other's.
	inFirst := decodeGroup(t, doReq(t, srv.URL, http.MethodGet, groupURLInRG(rgName, "shared")+apiVer, nil, http.StatusOK))
	inOther := decodeGroup(t, doReq(t, srv.URL, http.MethodGet, groupURLInRG(otherRG, "shared")+apiVer, nil, http.StatusOK))

	if inFirst.Properties.Containers[0].Properties.Image == "" || inOther.Properties.Containers[0].Properties.Image == "" {
		t.Fatalf("expected both groups populated, got %+v / %+v", inFirst, inOther)
	}

	// Deleting the group in one RG must not remove (or affect) the same-named
	// group in the other RG.
	doReq(t, srv.URL, http.MethodDelete, groupURLInRG(rgName, "shared")+apiVer, nil, http.StatusOK)

	doReq(t, srv.URL, http.MethodGet, groupURLInRG(rgName, "shared")+apiVer, nil, http.StatusNotFound)
	doReq(t, srv.URL, http.MethodGet, groupURLInRG(otherRG, "shared")+apiVer, nil, http.StatusOK)
}

func TestNilEngineStaysSynthetic(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := httptest.NewServer(azureserver.New(azureserver.DriversFrom(cloud)))
	t.Cleanup(srv.Close)

	body := doReq(t, srv.URL, http.MethodPut, groupURL("cg2")+apiVer,
		strings.NewReader(createBody), http.StatusCreated)

	created := decodeGroup(t, body)
	state := created.Properties.Containers[0].Properties.InstanceView.CurrentState
	if state.State != "Running" || state.ExitCode != nil {
		t.Fatalf("synthetic state = %+v, want Running with no exit code", state)
	}

	if created.Properties.InstanceView.State != "Running" {
		t.Fatalf("synthetic group state = %q, want Running", created.Properties.InstanceView.State)
	}

	// Synthetic logs are empty.
	logsBody := doReq(t, srv.URL, http.MethodGet,
		groupURL("cg2")+"/containers/app/logs"+apiVer, nil, http.StatusOK)

	var logs struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(logsBody, &logs); err != nil {
		t.Fatalf("decode logs: %v", err)
	}

	if logs.Content != "" {
		t.Fatalf("synthetic logs = %q, want empty", logs.Content)
	}
}

// wireGroup is the subset of the ARM container-group response the tests assert
// on.
type wireGroup struct {
	Name       string `json:"name"`
	Properties struct {
		ProvisioningState string `json:"provisioningState"`
		InstanceView      struct {
			State string `json:"state"`
		} `json:"instanceView"`
		Containers []struct {
			Name       string `json:"name"`
			Properties struct {
				Image        string `json:"image"`
				InstanceView struct {
					CurrentState struct {
						State    string `json:"state"`
						ExitCode *int   `json:"exitCode"`
					} `json:"currentState"`
				} `json:"instanceView"`
			} `json:"properties"`
		} `json:"containers"`
	} `json:"properties"`
}

func decodeGroup(t *testing.T, body []byte) wireGroup {
	t.Helper()

	var g wireGroup
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatalf("decode container group: %v (body: %s)", err, body)
	}

	return g
}

func doReq(t *testing.T, base, method, path string, body io.Reader, wantStatus int) []byte {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, base+path, body)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	out, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d (body: %s)", method, path, resp.StatusCode, wantStatus, out)
	}

	return out
}
