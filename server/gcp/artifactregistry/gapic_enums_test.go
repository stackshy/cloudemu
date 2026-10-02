package artifactregistry_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	"cloud.google.com/go/artifactregistry/apiv1/artifactregistrypb"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// newEnumGAPIC serves the full GCP server and returns the real apiv1 REST
// client, which sends every enum as a JSON number.
func newEnumGAPIC(t *testing.T) (*artifactregistry.Client, *httptest.Server, crdriver.ContainerRegistry) {
	t.Helper()

	cloud := cloudemu.NewGCP(
		config.WithClock(config.NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))),
		config.WithProjectID("demo"),
	)
	ts := httptest.NewServer(gcpserver.New(gcpserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	c, err := artifactregistry.NewRESTClient(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c, ts, cloud.ArtifactRegistry
}

func cleanupPolicy(action artifactregistrypb.CleanupPolicy_Action) map[string]*artifactregistrypb.CleanupPolicy {
	return map[string]*artifactregistrypb.CleanupPolicy{
		"p1": {
			Id:     "p1",
			Action: action,
			ConditionType: &artifactregistrypb.CleanupPolicy_Condition{
				Condition: &artifactregistrypb.CleanupPolicyCondition{
					TagState: artifactregistrypb.CleanupPolicyCondition_UNTAGGED.Enum(),
				},
			},
		},
	}
}

func assertRepoEnums(t *testing.T, step string, got *artifactregistrypb.Repository,
	action artifactregistrypb.CleanupPolicy_Action,
) {
	t.Helper()

	if got.GetFormat() != artifactregistrypb.Repository_DOCKER {
		t.Errorf("%s: format=%v want DOCKER", step, got.GetFormat())
	}

	if got.GetMode() != artifactregistrypb.Repository_STANDARD_REPOSITORY {
		t.Errorf("%s: mode=%v want STANDARD_REPOSITORY", step, got.GetMode())
	}

	p := got.GetCleanupPolicies()["p1"]
	if p.GetAction() != action {
		t.Errorf("%s: cleanupPolicies.p1.action=%v want %v", step, p.GetAction(), action)
	}

	if p.GetCondition().GetTagState() != artifactregistrypb.CleanupPolicyCondition_UNTAGGED {
		t.Errorf("%s: cleanupPolicies.p1.condition.tagState=%v want UNTAGGED", step, p.GetCondition().GetTagState())
	}
}

// rawRepo GETs a repository over plain REST and returns the decoded JSON.
func rawRepo(t *testing.T, ts *httptest.Server, name string) map[string]any {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/v1/"+name, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode raw GET: %v", err)
	}

	return out
}

func rawPolicyField(t *testing.T, repo map[string]any, field string) any {
	t.Helper()

	policies, _ := repo["cleanupPolicies"].(map[string]any)
	p1, _ := policies["p1"].(map[string]any)

	if field == "tagState" {
		cond, _ := p1["condition"].(map[string]any)
		return cond["tagState"]
	}

	return p1[field]
}

// TestGAPICRepositoryNumericEnumsLifecycle is GAR-01: the apiv1 REST client
// sends mode and the cleanup policy enums as numbers. Create used to fail with
// "cannot unmarshal number into Go struct field repositoryJSON.mode of type
// string", and cleanupPolicies numbers were stored and re-emitted raw.
func TestGAPICRepositoryNumericEnumsLifecycle(t *testing.T) {
	c, ts, _ := newEnumGAPIC(t)
	ctx := context.Background()
	parent := "projects/demo/locations/us-central1"
	name := parent + "/repositories/enums"

	op, err := c.CreateRepository(ctx, &artifactregistrypb.CreateRepositoryRequest{
		Parent:       parent,
		RepositoryId: "enums",
		Repository: &artifactregistrypb.Repository{
			Format:          artifactregistrypb.Repository_DOCKER,
			Mode:            artifactregistrypb.Repository_STANDARD_REPOSITORY,
			Description:     "v1",
			CleanupPolicies: cleanupPolicy(artifactregistrypb.CleanupPolicy_DELETE),
		},
	})
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	created, err := op.Wait(ctx)
	if err != nil {
		t.Fatalf("CreateRepository Wait: %v", err)
	}

	assertRepoEnums(t, "create", created, artifactregistrypb.CleanupPolicy_DELETE)

	got, err := c.GetRepository(ctx, &artifactregistrypb.GetRepositoryRequest{Name: name})
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}

	assertRepoEnums(t, "get", got, artifactregistrypb.CleanupPolicy_DELETE)

	raw := rawRepo(t, ts, name)
	if a := rawPolicyField(t, raw, "action"); a != "DELETE" {
		t.Errorf("raw GET cleanupPolicies.p1.action=%v want \"DELETE\"", a)
	}

	if s := rawPolicyField(t, raw, "tagState"); s != "UNTAGGED" {
		t.Errorf("raw GET cleanupPolicies.p1.condition.tagState=%v want \"UNTAGGED\"", s)
	}

	assertListed(t, c, parent, name)

	// Update the fetched object, so its echoed enums go back as numbers too.
	got.Description = "v2"
	got.CleanupPolicies = cleanupPolicy(artifactregistrypb.CleanupPolicy_KEEP)

	updated, err := c.UpdateRepository(ctx, &artifactregistrypb.UpdateRepositoryRequest{
		Repository: got,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"description", "cleanup_policies"}},
	})
	if err != nil {
		t.Fatalf("UpdateRepository: %v", err)
	}

	if updated.GetDescription() != "v2" {
		t.Errorf("update: description=%q want v2", updated.GetDescription())
	}

	assertRepoEnums(t, "update", updated, artifactregistrypb.CleanupPolicy_KEEP)

	assertDeletedRepo(t, c, name)
}

func assertListed(t *testing.T, c *artifactregistry.Client, parent, name string) {
	t.Helper()

	it := c.ListRepositories(context.Background(), &artifactregistrypb.ListRepositoriesRequest{Parent: parent})

	for {
		r, err := it.Next()
		if errors.Is(err, iterator.Done) {
			t.Fatalf("ListRepositories: %s not listed", name)
		}

		if err != nil {
			t.Fatalf("ListRepositories: %v", err)
		}

		if r.GetName() == name {
			assertRepoEnums(t, "list", r, artifactregistrypb.CleanupPolicy_DELETE)
			return
		}
	}
}

func assertDeletedRepo(t *testing.T, c *artifactregistry.Client, name string) {
	t.Helper()

	ctx := context.Background()

	// The delete operation is not Waited on: its missing Empty response is
	// GAR-02, tracked separately.
	if _, err := c.DeleteRepository(ctx, &artifactregistrypb.DeleteRepositoryRequest{Name: name}); err != nil {
		t.Fatalf("DeleteRepository: %v", err)
	}

	_, err := c.GetRepository(ctx, &artifactregistrypb.GetRepositoryRequest{Name: name})

	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) || apiErr.Code != http.StatusNotFound {
		t.Fatalf("GetRepository after delete: err=%v want 404", err)
	}
}

// TestRepositoryUnknownNumericModeRejected checks that a number with no Mode
// value is a 400 INVALID_ARGUMENT naming the field.
func TestRepositoryUnknownNumericModeRejected(t *testing.T) {
	_, ts, _ := newEnumGAPIC(t)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		ts.URL+"/v1/projects/demo/locations/us-central1/repositories?repositoryId=bad",
		strings.NewReader(`{"format":"DOCKER","mode":99}`))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", resp.StatusCode, body)
	}

	var env struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}

	if env.Error.Status != "INVALID_ARGUMENT" || !strings.Contains(env.Error.Message, "mode") {
		t.Fatalf("error=%+v want INVALID_ARGUMENT naming mode", env.Error)
	}
}

// TestRepositoryStoredNumericCleanupPolicyRendersNames covers a repository
// whose cleanupPolicies were stored with numeric enums before create bodies
// were normalized: reads must render the names.
func TestRepositoryStoredNumericCleanupPolicyRendersNames(t *testing.T) {
	c, ts, reg := newEnumGAPIC(t)
	ctx := context.Background()

	stored := `{"p1":{"id":"p1","action":1,"condition":{"tagState":2}}}`

	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{
		Name: "legacy",
		Tags: map[string]string{
			"cloudemu:gcpArFormat":          "DOCKER",
			"cloudemu:gcpArCleanupPolicies": stored,
		},
	}); err != nil {
		t.Fatalf("seed repository: %v", err)
	}

	name := "projects/demo/locations/us-central1/repositories/legacy"

	raw := rawRepo(t, ts, name)
	if a := rawPolicyField(t, raw, "action"); a != "DELETE" {
		t.Errorf("raw GET cleanupPolicies.p1.action=%v want \"DELETE\"", a)
	}

	if s := rawPolicyField(t, raw, "tagState"); s != "UNTAGGED" {
		t.Errorf("raw GET cleanupPolicies.p1.condition.tagState=%v want \"UNTAGGED\"", s)
	}

	got, err := c.GetRepository(ctx, &artifactregistrypb.GetRepositoryRequest{Name: name})
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}

	assertRepoEnums(t, "get", got, artifactregistrypb.CleanupPolicy_DELETE)

	repo, err := reg.GetRepository(ctx, "legacy")
	if err != nil {
		t.Fatalf("driver GetRepository: %v", err)
	}

	if repo.Tags["cloudemu:gcpArCleanupPolicies"] != stored {
		t.Fatalf("stored tag rewritten: %s", repo.Tags["cloudemu:gcpArCleanupPolicies"])
	}
}
