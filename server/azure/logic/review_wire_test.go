package logic_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/logic/armlogic"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/logic"
	logicsrv "github.com/stackshy/cloudemu/v2/server/azure/logic"
)

// deleteMidPatchStore makes a DELETE land in the middle of a PATCH: right after
// the handler reads the workflow (a read-merge-write handler), or right before
// the store's own atomic merge (a decode-and-call handler). Either way the
// DELETE has completed before the PATCH writes anything, so the PATCH must
// answer 404 and must not bring the workflow back.
type deleteMidPatchStore struct {
	*logic.Mock

	armed bool
}

func (s *deleteMidPatchStore) Get(ctx context.Context, sub, rg, name string) (logic.Workflow, error) {
	wf, err := s.Mock.Get(ctx, sub, rg, name)
	s.fire(ctx, sub, rg, name)

	return wf, err
}

//nolint:gocritic // mirrors the Store signature.
func (s *deleteMidPatchStore) Update(ctx context.Context, sub, rg, name string, p logic.Patch) (logic.Workflow, error) {
	s.fire(ctx, sub, rg, name)
	return s.Mock.Update(ctx, sub, rg, name, p)
}

func (s *deleteMidPatchStore) fire(ctx context.Context, sub, rg, name string) {
	if s.armed {
		s.armed = false
		_, _ = s.Mock.Delete(ctx, sub, rg, name)
	}
}

func TestWirePatchRacingDeleteIs404AndStaysDeleted(t *testing.T) {
	store := &deleteMidPatchStore{Mock: logic.New(config.NewOptions())}
	srv := httptest.NewServer(logicsrv.New(store))
	t.Cleanup(srv.Close)

	path := basePath + "wf1" + apiVer
	if status, _, raw := do(t, srv, http.MethodPut, path, `{"location":"eastus"}`); status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, raw)
	}

	store.armed = true

	status, got, raw := do(t, srv, http.MethodPatch, path, `{"tags":{"a":"b"}}`)
	if status != http.StatusNotFound || got.Error == nil || got.Error.Code != "ResourceNotFound" {
		t.Errorf("PATCH racing DELETE: status=%d body=%s, want 404 ResourceNotFound", status, raw)
	}

	if status, _, raw := do(t, srv, http.MethodGet, path, ""); status != http.StatusNotFound {
		t.Errorf("workflow resurrected by PATCH: GET status=%d body=%s", status, raw)
	}
}

func TestWirePatchAfterDeleteIs404(t *testing.T) {
	srv, _ := newServer(t)
	path := basePath + "wf1" + apiVer

	do(t, srv, http.MethodPut, path, `{"location":"eastus"}`)
	do(t, srv, http.MethodDelete, path, "")

	for _, body := range []string{"", `{"tags":{"a":"b"}}`} {
		if status, _, raw := do(t, srv, http.MethodPatch, path, body); status != http.StatusNotFound {
			t.Errorf("PATCH %q after DELETE: status=%d body=%s", body, status, raw)
		}
	}

	if status, _, _ := do(t, srv, http.MethodGet, path, ""); status != http.StatusNotFound {
		t.Errorf("GET after PATCH-after-DELETE: status=%d, want 404", status)
	}
}

// readBarrierStore holds every Get until two of them are in flight, which forces
// two PATCHes that read before they write to both read the same version. A
// PATCH that merges inside the store never calls Get and is unaffected.
type readBarrierStore struct {
	*logic.Mock

	mu      sync.Mutex
	waiting int
	release chan struct{}
}

func (s *readBarrierStore) Get(ctx context.Context, sub, rg, name string) (logic.Workflow, error) {
	wf, err := s.Mock.Get(ctx, sub, rg, name)

	s.mu.Lock()
	s.waiting++
	if s.waiting == 2 { //nolint:mnd // two concurrent readers
		close(s.release)
	}
	s.mu.Unlock()

	select {
	case <-s.release:
	case <-time.After(2 * time.Second):
	}

	return wf, err
}

func TestWireConcurrentPatchesBothLand(t *testing.T) {
	store := &readBarrierStore{Mock: logic.New(config.NewOptions()), release: make(chan struct{})}
	srv := httptest.NewServer(logicsrv.New(store))
	t.Cleanup(srv.Close)

	path := basePath + "wf1" + apiVer
	do(t, srv, http.MethodPut, path, `{"location":"eastus","tags":{"k":"old"}}`)

	var wg sync.WaitGroup

	for _, body := range []string{`{"tags":{"k":"new"}}`, `{"properties":{"state":"Disabled"}}`} {
		wg.Go(func() {
			if status, _, raw := do(t, srv, http.MethodPatch, path, body); status != http.StatusOK {
				t.Errorf("PATCH %s: status=%d body=%s", body, status, raw)
			}
		})
	}

	wg.Wait()

	_, got, _ := do(t, srv, http.MethodGet, path, "")
	if got.Tags["k"] != "new" || got.Properties.State != "Disabled" || got.Properties.Version != "00000000000000000003" {
		t.Errorf("lost update: tags=%v state=%s version=%s, want k=new, Disabled, version 3",
			got.Tags, got.Properties.State, got.Properties.Version)
	}
}

func TestWireRejectsBadNameAndDefinition(t *testing.T) {
	srv, _ := newServer(t)

	cases := []struct {
		name, body, code string
	}{
		{"bad%20name!", `{"location":"eastus"}`, "InvalidParameter"},
		{strings.Repeat("a", 44), `{"location":"eastus"}`, "InvalidParameter"},
		{"wf1", `{"location":"eastus","properties":{"definition":"notanobject"}}`, "InvalidRequestContent"},
		{"wf1", `{"location":"eastus","properties":{"definition":[1]}}`, "InvalidRequestContent"},
	}

	for _, c := range cases {
		status, got, raw := do(t, srv, http.MethodPut, basePath+c.name+apiVer, c.body)
		if status != http.StatusBadRequest || got.Error == nil || got.Error.Code != c.code {
			t.Errorf("PUT %s %s: status=%d body=%s, want 400 %s", c.name, c.body, status, raw, c.code)
		}
	}

	do(t, srv, http.MethodPut, basePath+"ok"+apiVer, `{"location":"eastus"}`)

	status, got, raw := do(t, srv, http.MethodPatch, basePath+"ok"+apiVer, `{"properties":{"definition":"x"}}`)
	if status != http.StatusBadRequest || got.Error == nil || got.Error.Code != "InvalidRequestContent" {
		t.Errorf("PATCH non-object definition: status=%d body=%s", status, raw)
	}
}

func TestWireListTopAndNextLink(t *testing.T) {
	srv, _ := newServer(t)

	for i := range 5 {
		do(t, srv, http.MethodPut, fmt.Sprintf("%swf%d%s", basePath, i, apiVer), `{"location":"eastus"}`)
	}

	var names []string

	next := srv.URL + strings.TrimSuffix(basePath, "/") + apiVer + "&$top=2"
	pages := 0

	for next != "" {
		pages++

		resp, err := srv.Client().Get(next) //nolint:noctx // test helper
		if err != nil {
			t.Fatal(err)
		}

		var page struct {
			Value    []wireResp `json:"value"`
			NextLink string     `json:"nextLink"`
		}

		err = json.NewDecoder(resp.Body).Decode(&page)
		_ = resp.Body.Close()

		if err != nil || resp.StatusCode != http.StatusOK || len(page.Value) > 2 {
			t.Fatalf("page %d: status=%d err=%v len=%d", pages, resp.StatusCode, err, len(page.Value))
		}

		for _, v := range page.Value {
			names = append(names, v.Name)
		}

		next = page.NextLink
	}

	if pages != 3 || strings.Join(names, ",") != "wf0,wf1,wf2,wf3,wf4" {
		t.Errorf("pages=%d names=%v", pages, names)
	}

	status, got, _ := do(t, srv, http.MethodGet, strings.TrimSuffix(basePath, "/")+apiVer, "")
	if status != http.StatusOK || len(got.Value) != 5 {
		t.Errorf("no $top: status=%d len=%d, want all 5 in one page", status, len(got.Value))
	}

	for _, q := range []string{"&$top=0", "&$top=x", "&$skiptoken=%21%21"} {
		if status, _, raw := do(t, srv, http.MethodGet, strings.TrimSuffix(basePath, "/")+apiVer+q, ""); status != http.StatusBadRequest {
			t.Errorf("list %s: status=%d body=%s, want 400", q, status, raw)
		}
	}
}

func TestMatchesListCallbackURL(t *testing.T) {
	h := logicsrv.New(logic.New(config.NewOptions()))

	cases := map[string]bool{
		basePath + "wf1/triggers/manual/listCallbackUrl": true,
		basePath + "wf1/triggers/manual/listcallbackurl": true,
		basePath + "wf1/triggers/manual/run":             false,
		basePath + "wf1/triggers/manual":                 false,
		basePath + "wf1/triggers":                        false,
		basePath + "wf1/listCallbackUrl":                 false,
	}

	for path, want := range cases {
		req := httptest.NewRequest(http.MethodPost, path, http.NoBody)
		if got := h.Matches(req); got != want {
			t.Errorf("Matches(%s) = %v, want %v", path, got, want)
		}
	}
}

func TestSDKListCallbackURL(t *testing.T) {
	env := newSDKEnv(t)
	ctx := context.Background()
	wf := createWorkflow(t, env, "wf-cb")

	resp, err := env.triggers.ListCallbackURL(ctx, sdkRG, "wf-cb", "manual", nil)
	if err != nil {
		t.Fatalf("ListCallbackURL: %v", err)
	}

	base := *wf.Properties.AccessEndpoint + "/triggers/manual/paths/invoke"
	cb := resp.WorkflowTriggerCallbackURL

	if cb.BasePath == nil || *cb.BasePath != base || cb.Method == nil || *cb.Method != "POST" || cb.Queries == nil {
		t.Fatalf("callback = %+v", cb)
	}

	q := cb.Queries
	if *q.APIVersion != "2016-10-01" || *q.Sp != "/triggers/manual/run" || *q.Sv != "1.0" || len(*q.Sig) != 43 {
		t.Errorf("queries = %s %s %s %s", *q.APIVersion, *q.Sp, *q.Sv, *q.Sig)
	}

	want := base + "?api-version=2016-10-01&sp=%2Ftriggers%2Fmanual%2Frun&sv=1.0&sig=" + *q.Sig
	if cb.Value == nil || *cb.Value != want {
		t.Errorf("value = %v, want %s", cb.Value, want)
	}

	if _, err := env.triggers.ListCallbackURL(ctx, sdkRG, "wf-cb", "missing", nil); !isStatus(err, http.StatusNotFound) {
		t.Errorf("missing trigger: err = %v, want 404", err)
	}

	if _, err := env.triggers.ListCallbackURL(ctx, sdkRG, "nope", "manual", nil); !isStatus(err, http.StatusNotFound) {
		t.Errorf("missing workflow: err = %v, want 404", err)
	}
}

func TestSDKEndpointsSkuAndISERoundTrip(t *testing.T) {
	env := newSDKEnv(t)
	ctx := context.Background()
	ise := "/subscriptions/" + sdkSub + "/resourceGroups/" + sdkRG + "/providers/Microsoft.Logic/integrationServiceEnvironments/ise1"

	if _, err := env.workflows.CreateOrUpdate(ctx, sdkRG, "wf-ise", armlogic.Workflow{
		Location: to.Ptr("eastus"),
		Properties: &armlogic.WorkflowProperties{
			Definition:                    sdkDefinition(),
			IntegrationServiceEnvironment: &armlogic.ResourceReference{ID: to.Ptr(ise)},
			SKU:                           &armlogic.SKU{Name: to.Ptr(armlogic.SKUNameStandard)},
		},
	}, nil); err != nil {
		t.Fatal(err)
	}

	got, err := env.workflows.Get(ctx, sdkRG, "wf-ise", nil)
	if err != nil {
		t.Fatal(err)
	}

	p := got.Properties
	if p.IntegrationServiceEnvironment == nil || *p.IntegrationServiceEnvironment.ID != ise {
		t.Errorf("integrationServiceEnvironment = %+v", p.IntegrationServiceEnvironment)
	}

	if p.SKU == nil || p.SKU.Name == nil || *p.SKU.Name != armlogic.SKUNameStandard {
		t.Errorf("sku = %+v", p.SKU)
	}

	ec := p.EndpointsConfiguration
	if ec == nil || ec.Workflow == nil || ec.Connector == nil || len(ec.Workflow.OutgoingIPAddresses) != 4 ||
		len(ec.Workflow.AccessEndpointIPAddresses) != 4 || len(ec.Connector.OutgoingIPAddresses) != 2 ||
		*ec.Workflow.OutgoingIPAddresses[0].Address == "" {
		t.Fatalf("endpointsConfiguration = %+v", ec)
	}

	again, _ := env.workflows.Get(ctx, sdkRG, "wf-ise", nil)
	if *again.Properties.EndpointsConfiguration.Workflow.OutgoingIPAddresses[0].Address !=
		*ec.Workflow.OutgoingIPAddresses[0].Address {
		t.Error("endpointsConfiguration not stable across gets")
	}
}

func TestSDKListPagerHonoursTop(t *testing.T) {
	env := newSDKEnv(t)
	ctx := context.Background()

	for i := range 5 {
		createWorkflow(t, env, fmt.Sprintf("wf-%d", i))
	}

	pager := env.workflows.NewListByResourceGroupPager(sdkRG, &armlogic.WorkflowsClientListByResourceGroupOptions{
		Top: to.Ptr[int32](2),
	})

	var names []string

	pages := 0

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatal(err)
		}

		pages++

		for _, wf := range page.Value {
			names = append(names, *wf.Name)
		}
	}

	if pages != 3 || strings.Join(names, ",") != "wf-0,wf-1,wf-2,wf-3,wf-4" {
		t.Errorf("pages=%d names=%v", pages, names)
	}
}

func TestWireIdentityPatchAndErrorPaths(t *testing.T) {
	srv, _ := newServer(t)
	path := basePath + "wf1" + apiVer
	ua := "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id1"

	status, _, raw := do(t, srv, http.MethodPut, path, `{"location":"eastus","identity":{"type":"SystemAssigned, UserAssigned",`+
		`"userAssignedIdentities":{"`+ua+`":{}}},"properties":{"definition":`+wfDef+`}}`)
	if status != http.StatusCreated || !strings.Contains(raw, `"principalId"`) || !strings.Contains(raw, `"clientId"`) {
		t.Fatalf("create with identities: %d %s", status, raw)
	}

	status, _, raw = do(t, srv, http.MethodPatch, path, `{"identity":{"type":"None"}}`)
	if status != http.StatusOK || strings.Contains(raw, `"identity"`) {
		t.Errorf("PATCH identity None: %d %s", status, raw)
	}

	cases := []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPatch, path, `{"tags":`, http.StatusBadRequest},
		{http.MethodGet, basePath + "wf1/triggers/manual/listCallbackUrl" + apiVer, "", http.StatusMethodNotAllowed},
		{http.MethodPost, strings.TrimSuffix(basePath, "/") + apiVer, "", http.StatusMethodNotAllowed},
		{http.MethodPost, path, "", http.StatusMethodNotAllowed},
		{http.MethodPost, basePath + "nope/triggers/manual/listCallbackUrl" + apiVer, "", http.StatusNotFound},
		{http.MethodPut, "/subscriptions/sub1/providers/Microsoft.Logic/workflows/x" + apiVer, `{"location":"eastus"}`,
			http.StatusBadRequest},
	}

	for _, c := range cases {
		if status, _, raw := do(t, srv, c.method, c.path, c.body); status != c.want {
			t.Errorf("%s %s: status=%d body=%s, want %d", c.method, c.path, status, raw, c.want)
		}
	}
}
