package logic_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/logic"
)

const reqDef = `{"triggers":{"manual":{"type":"Request","kind":"Http","inputs":{"method":"put","relativePath":"orders/{id}"}},` +
	`"every":{"type":"Recurrence","recurrence":{"frequency":"Hour","interval":1}}},"actions":{}}`

func mustCreate(t *testing.T, m *logic.Mock, rg, name string, in *logic.Input) logic.Workflow {
	t.Helper()

	if in == nil {
		in = &logic.Input{Location: "eastus", Definition: json.RawMessage(def)}
	}

	wf, _, err := m.CreateOrUpdate(context.Background(), "sub", rg, name, in)
	if err != nil {
		t.Fatalf("create %s/%s: %v", rg, name, err)
	}

	return wf
}

func TestUpdateAfterDeleteIsNotFoundAndDoesNotRecreate(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()
	mustCreate(t, m, "rg", "wf1", nil)

	if _, err := m.Delete(ctx, "sub", "rg", "wf1"); err != nil {
		t.Fatal(err)
	}

	_, err := m.Update(ctx, "sub", "rg", "wf1", logic.Patch{Tags: map[string]string{"a": "b"}})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("patch after delete: err = %v, want NotFound", err)
	}

	if !strings.Contains(err.Error(), "under resource group 'rg'") {
		t.Errorf("message = %q, want ARM's resource-group clause", err.Error())
	}

	if _, err := m.Get(ctx, "sub", "rg", "wf1"); !cerrors.IsNotFound(err) {
		t.Fatalf("workflow came back after PATCH: err = %v", err)
	}
}

func TestUpdateMergesOnlySuppliedFields(t *testing.T) {
	ctx := context.Background()
	m, fc := newMock()
	orig := mustCreate(t, m, "rg", "wf1", &logic.Input{
		Location:   "eastus",
		Tags:       map[string]string{"env": "dev"},
		Definition: json.RawMessage(def),
		Parameters: json.RawMessage(`{"p":{"value":1}}`),
		Identity:   &logic.Identity{Type: "SystemAssigned"},
		Sku:        json.RawMessage(`{"name":"Standard"}`),
	})

	fc.Advance(1)

	got, err := m.Update(ctx, "sub", "rg", "wf1", logic.Patch{State: "disabled"})
	if err != nil {
		t.Fatal(err)
	}

	if got.State != logic.StateDisabled || got.Tags["env"] != "dev" || string(got.Parameters) != `{"p":{"value":1}}` ||
		got.Identity == nil || got.Identity.PrincipalID != orig.Identity.PrincipalID || string(got.Sku) != `{"name":"Standard"}` {
		t.Fatalf("patch clobbered unsupplied fields: %+v", got)
	}

	if got.Version() != "00000000000000000002" || !got.ChangedTime.After(orig.ChangedTime) ||
		!got.CreatedTime.Equal(orig.CreatedTime) || got.AccessEndpoint != orig.AccessEndpoint {
		t.Errorf("version/times/endpoint = %s %v %v %s", got.Version(), got.ChangedTime, got.CreatedTime, got.AccessEndpoint)
	}

	got, err = m.Update(ctx, "sub", "rg", "wf1", logic.Patch{
		Tags:                          map[string]string{},
		Identity:                      &logic.Identity{Type: "None"},
		Definition:                    json.RawMessage(reqDef),
		IntegrationServiceEnvironment: json.RawMessage(`{"id":"/ise/1"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Tags) != 0 || got.Identity != nil || string(got.Definition) != reqDef ||
		string(got.IntegrationServiceEnvironment) != `{"id":"/ise/1"}` {
		t.Errorf("second patch = %+v", got)
	}

	for _, p := range []logic.Patch{{State: "Deleted"}, {Definition: json.RawMessage(`"x"`)}} {
		if _, err := m.Update(ctx, "sub", "rg", "wf1", p); !cerrors.IsInvalidArgument(err) {
			t.Errorf("patch %+v: err = %v, want InvalidArgument", p, err)
		}
	}
}

// TestConcurrentUpdatesAllLand runs PATCHes of disjoint fields in parallel; every
// one must survive, which a read-then-write outside the lock would not
// guarantee.
func TestConcurrentUpdatesAllLand(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()
	mustCreate(t, m, "rg", "wf1", nil)

	const n = 50

	var wg sync.WaitGroup

	for i := range n {
		wg.Go(func() {
			var err error
			if i%2 == 0 {
				_, err = m.Update(ctx, "sub", "rg", "wf1", logic.Patch{State: logic.StateDisabled})
			} else {
				_, err = m.Update(ctx, "sub", "rg", "wf1", logic.Patch{Tags: map[string]string{"k": "v"}})
			}

			if err != nil {
				t.Error(err)
			}
		})
	}

	wg.Wait()

	got, err := m.Get(ctx, "sub", "rg", "wf1")
	if err != nil {
		t.Fatal(err)
	}

	if got.State != logic.StateDisabled || got.Tags["k"] != "v" || got.Revision != n+1 {
		t.Errorf("state=%s tags=%v revision=%d, want Disabled, k=v, %d", got.State, got.Tags, got.Revision, n+1)
	}
}

func TestNameAndDefinitionValidation(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	for _, name := range []string{"bad name!", "a/b", "é", strings.Repeat("a", 44)} {
		_, _, err := m.CreateOrUpdate(ctx, "sub", "rg", name, &logic.Input{Location: "eastus"})
		if !cerrors.IsInvalidArgument(err) {
			t.Errorf("name %q: err = %v, want InvalidArgument", name, err)
		}
	}

	for _, name := range []string{"a", "My_wf-1.(v2)", strings.Repeat("a", 43)} {
		if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", name, &logic.Input{Location: "eastus"}); err != nil {
			t.Errorf("name %q rejected: %v", name, err)
		}
	}

	for _, d := range []string{`"notanobject"`, `[]`, `42`, `{"a":`} {
		_, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "wf", &logic.Input{Location: "eastus", Definition: json.RawMessage(d)})
		if !cerrors.IsInvalidArgument(err) {
			t.Errorf("definition %s: err = %v, want InvalidArgument", d, err)
		}
	}

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "wf", &logic.Input{
		Location: "eastus", Definition: json.RawMessage(" null "),
	}); err != nil {
		t.Errorf("null definition rejected: %v", err)
	}
}

func TestListOrderIsByARMIDAcrossResourceGroups(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	for _, rg := range []string{"rg-c", "RG-a", "rg-b"} {
		mustCreate(t, m, rg, "same", nil)
	}

	mustCreate(t, m, "rg-b", "Alpha", nil)

	want := []string{"RG-a/same", "rg-b/Alpha", "rg-b/same", "rg-c/same"}

	for range 20 {
		got, err := m.ListBySubscription(ctx, "sub")
		if err != nil {
			t.Fatal(err)
		}

		var ids []string
		for _, wf := range got {
			ids = append(ids, wf.ResourceGroup+"/"+wf.Name)
		}

		if strings.Join(ids, ",") != strings.Join(want, ",") {
			t.Fatalf("order = %v, want %v", ids, want)
		}
	}
}

func TestEndpointsAreDeterministicPerRegion(t *testing.T) {
	m, _ := newMock()
	a := mustCreate(t, m, "rg", "a", &logic.Input{Location: "West Europe"})
	b := mustCreate(t, m, "rg2", "b", &logic.Input{Location: "westeurope"})
	c := mustCreate(t, m, "rg", "c", &logic.Input{Location: "eastus"})

	ea, eb, ec := a.Endpoints(), b.Endpoints(), c.Endpoints()

	if len(ea.Workflow.OutgoingIPAddresses) != 4 || len(ea.Workflow.AccessEndpointIPAddresses) != 4 ||
		len(ea.Connector.OutgoingIPAddresses) != 2 || len(ea.Connector.AccessEndpointIPAddresses) != 2 {
		t.Fatalf("endpoints shape = %+v", ea)
	}

	if strings.Join(ea.Workflow.OutgoingIPAddresses, ",") != strings.Join(eb.Workflow.OutgoingIPAddresses, ",") {
		t.Errorf("same region differs: %v vs %v", ea.Workflow.OutgoingIPAddresses, eb.Workflow.OutgoingIPAddresses)
	}

	if strings.Join(ea.Workflow.OutgoingIPAddresses, ",") == strings.Join(ec.Workflow.OutgoingIPAddresses, ",") {
		t.Errorf("different regions share addresses: %v", ec.Workflow.OutgoingIPAddresses)
	}
}

func TestListCallbackURL(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()
	wf := mustCreate(t, m, "rg", "wf1", &logic.Input{Location: "eastus", Definition: json.RawMessage(reqDef)})

	cb, err := m.ListCallbackURL(ctx, "sub", "rg", "wf1", "MANUAL")
	if err != nil {
		t.Fatal(err)
	}

	base := wf.AccessEndpoint + "/triggers/manual/paths/invoke"
	if cb.BasePath != base || cb.Method != "PUT" || cb.RelativePath != "orders/{id}" {
		t.Errorf("callback = %+v", cb)
	}

	want := base + "?api-version=2016-10-01&sp=%2Ftriggers%2Fmanual%2Frun&sv=1.0&sig=" + cb.Queries.Sig
	if cb.Value != want || len(cb.Queries.Sig) != 43 || cb.Queries.Sp != "/triggers/manual/run" || cb.Queries.Sv != "1.0" {
		t.Errorf("value = %q\nwant    %q (queries %+v)", cb.Value, want, cb.Queries)
	}

	again, _ := m.ListCallbackURL(ctx, "sub", "rg", "wf1", "manual")
	if again.Value != cb.Value {
		t.Errorf("callback URL not stable: %q vs %q", again.Value, cb.Value)
	}

	rec, err := m.ListCallbackURL(ctx, "sub", "rg", "wf1", "every")
	if err != nil || rec.Method != "POST" {
		t.Errorf("recurrence trigger: %+v, %v", rec, err)
	}

	if _, err := m.ListCallbackURL(ctx, "sub", "rg", "wf1", "nope"); !cerrors.IsNotFound(err) {
		t.Errorf("missing trigger: err = %v, want NotFound", err)
	}

	if _, err := m.ListCallbackURL(ctx, "sub", "rg", "gone", "manual"); !cerrors.IsNotFound(err) {
		t.Errorf("missing workflow: err = %v, want NotFound", err)
	}

	mustCreate(t, m, "rg", "nodef", &logic.Input{Location: "eastus"})

	if _, err := m.ListCallbackURL(ctx, "sub", "rg", "nodef", "manual"); !cerrors.IsNotFound(err) {
		t.Errorf("no definition: err = %v, want NotFound", err)
	}
}
