package apigateway_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/gcp/apigateway"
	agdriver "github.com/stackshy/cloudemu/v2/services/apigatewaygcp/driver"
)

const (
	project  = "demo"
	global   = "global"
	region   = "us-central1"
	apiID    = "my-api"
	configID = "my-config"
	gwID     = "my-gw"
)

func newMock() *apigateway.Mock { return apigateway.New(config.NewOptions()) }

func ctx() context.Context { return context.Background() }

func fields(kv map[string]string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range kv {
		out[k] = json.RawMessage(`"` + v + `"`)
	}

	return out
}

func mustAPI(t *testing.T, m *apigateway.Mock, id string) {
	t.Helper()

	_, op, err := m.CreateAPI(ctx(), &agdriver.Config{
		Project: project, Location: global, ID: id, Fields: fields(map[string]string{"displayName": id}),
	})
	if err != nil {
		t.Fatalf("CreateAPI: %v", err)
	}

	if op == nil || !op.Done {
		t.Fatalf("CreateAPI op not done: %+v", op)
	}
}

func mustConfig(t *testing.T, m *apigateway.Mock, api, id string) {
	t.Helper()

	_, _, err := m.CreateAPIConfig(ctx(), &agdriver.Config{
		Project: project, Location: global, API: api, ID: id,
		Fields: fields(map[string]string{"displayName": id}),
	})
	if err != nil {
		t.Fatalf("CreateAPIConfig: %v", err)
	}
}

func TestCreateGetAPI(t *testing.T) {
	m := newMock()
	mustAPI(t, m, apiID)

	got, err := m.GetAPI(ctx(), project, global, apiID)
	if err != nil {
		t.Fatalf("GetAPI: %v", err)
	}

	if got.ID != apiID {
		t.Fatalf("id = %q, want %q", got.ID, apiID)
	}

	if got.CreateTime.IsZero() {
		t.Fatal("createTime not minted")
	}
}

func TestCreateAPIDuplicate(t *testing.T) {
	m := newMock()
	mustAPI(t, m, apiID)

	_, _, err := m.CreateAPI(ctx(), &agdriver.Config{Project: project, Location: global, ID: apiID})
	if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate CreateAPI err = %v, want AlreadyExists", err)
	}
}

func TestConfigRequiresParentAPI(t *testing.T) {
	m := newMock()

	_, _, err := m.CreateAPIConfig(ctx(), &agdriver.Config{
		Project: project, Location: global, API: "missing", ID: configID,
	})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("config under missing api err = %v, want NotFound", err)
	}
}

func TestDeleteAPICascadesConfigs(t *testing.T) {
	m := newMock()
	mustAPI(t, m, "a1")
	mustAPI(t, m, "a10")
	mustConfig(t, m, "a1", "c1")
	mustConfig(t, m, "a10", "c10")

	if _, err := m.DeleteAPI(ctx(), project, global, "a1"); err != nil {
		t.Fatalf("DeleteAPI: %v", err)
	}

	// a1's config is gone.
	if _, err := m.GetAPIConfig(ctx(), project, global, "a1", "c1"); !cerrors.IsNotFound(err) {
		t.Fatalf("a1 config after cascade err = %v, want NotFound", err)
	}

	// a10's config must survive — the cascade prefix is trailing-slash-bounded.
	if _, err := m.GetAPIConfig(ctx(), project, global, "a10", "c10"); err != nil {
		t.Fatalf("a10 config wrongly cascaded: %v", err)
	}
}

func TestGatewayAPIConfigRefValidated(t *testing.T) {
	m := newMock()
	mustAPI(t, m, apiID)
	mustConfig(t, m, apiID, configID)

	ref := "projects/" + project + "/locations/" + global + "/apis/" + apiID + "/configs/" + configID

	// Valid reference succeeds.
	if _, _, err := m.CreateGateway(ctx(), &agdriver.Config{
		Project: project, Location: region, ID: gwID,
		Fields: map[string]json.RawMessage{"apiConfig": json.RawMessage(`"` + ref + `"`)},
	}); err != nil {
		t.Fatalf("CreateGateway with valid ref: %v", err)
	}

	// Dangling reference 404s.
	_, _, err := m.CreateGateway(ctx(), &agdriver.Config{
		Project: project, Location: region, ID: "gw2",
		Fields: map[string]json.RawMessage{"apiConfig": json.RawMessage(`"` + ref + "-missing" + `"`)},
	})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("CreateGateway dangling ref err = %v, want NotFound", err)
	}
}

func TestPatchAPIMaskReplacesOnlyMasked(t *testing.T) {
	m := newMock()
	_, _, err := m.CreateAPI(ctx(), &agdriver.Config{
		Project: project, Location: global, ID: apiID,
		Fields: fields(map[string]string{"displayName": "old", "managedService": "svc.example.com"}),
	})
	if err != nil {
		t.Fatalf("CreateAPI: %v", err)
	}

	_, _, err = m.PatchAPI(ctx(), &agdriver.Config{
		Project: project, Location: global, ID: apiID,
		Fields: fields(map[string]string{"displayName": "new"}),
	}, []string{"displayName"})
	if err != nil {
		t.Fatalf("PatchAPI: %v", err)
	}

	got, err := m.GetAPI(ctx(), project, global, apiID)
	if err != nil {
		t.Fatalf("GetAPI: %v", err)
	}

	if string(got.Fields["displayName"]) != `"new"` {
		t.Fatalf("displayName = %s, want \"new\"", got.Fields["displayName"])
	}

	// Unmasked field survives.
	if string(got.Fields["managedService"]) != `"svc.example.com"` {
		t.Fatalf("managedService dropped: %s", got.Fields["managedService"])
	}
}

func TestListScopedAndOrdered(t *testing.T) {
	m := newMock()
	mustAPI(t, m, "b")
	mustAPI(t, m, "a")

	all, err := m.ListAPIs(ctx(), project, global)
	if err != nil {
		t.Fatalf("ListAPIs: %v", err)
	}

	if len(all) != 2 || all[0].ID != "a" || all[1].ID != "b" {
		t.Fatalf("ListAPIs = %+v, want [a b]", all)
	}
}

func TestClonedReadsAreIsolated(t *testing.T) {
	m := newMock()
	mustAPI(t, m, apiID)

	got1, _ := m.GetAPI(ctx(), project, global, apiID)
	got1.Fields["displayName"] = json.RawMessage(`"mutated"`)

	got2, _ := m.GetAPI(ctx(), project, global, apiID)
	if string(got2.Fields["displayName"]) == `"mutated"` {
		t.Fatal("stored resource aliased a caller copy")
	}
}
