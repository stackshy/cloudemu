package opensearch_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/opensearch"
	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

func newMock(t *testing.T) *opensearch.Mock {
	t.Helper()

	return opensearch.New(config.NewOptions())
}

func TestCreateDescribeDomain(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	out, err := m.CreateDomain(ctx, driver.CreateDomainInput{
		DomainName:    "my-domain",
		EngineVersion: "OpenSearch_2.11",
		ClusterConfig: driver.ClusterConfig{InstanceType: "t3.small.search", InstanceCount: 2},
		Tags:          map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	if !strings.Contains(out.ARN, ":es:") || !strings.HasSuffix(out.ARN, "domain/my-domain") {
		t.Fatalf("unexpected ARN: %s", out.ARN)
	}

	if out.Endpoint == "" || !out.Created {
		t.Fatalf("expected created domain with endpoint, got %+v", out)
	}

	desc, err := m.DescribeDomain(ctx, "my-domain")
	if err != nil {
		t.Fatalf("DescribeDomain: %v", err)
	}

	if desc.ClusterConfig.InstanceCount != 2 || desc.ClusterConfig.InstanceType != "t3.small.search" {
		t.Fatalf("cluster config not reflected: %+v", desc.ClusterConfig)
	}
}

func TestCreateDomainValidation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	tests := []struct {
		name   string
		domain string
	}{
		{name: "too short", domain: "ab"},
		{name: "uppercase", domain: "MyDomain"},
		{name: "leading digit", domain: "9domain"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.CreateDomain(ctx, driver.CreateDomainInput{DomainName: tc.domain})

			var apiErr *driver.APIError
			if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExValidation {
				t.Fatalf("want ValidationException, got %v", err)
			}
		})
	}
}

func TestCreateDomainDuplicate(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, err := m.CreateDomain(ctx, driver.CreateDomainInput{DomainName: "dup-domain"}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := m.CreateDomain(ctx, driver.CreateDomainInput{DomainName: "dup-domain"})

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExResourceAlreadyExists {
		t.Fatalf("want ResourceAlreadyExistsException, got %v", err)
	}
}

func TestDescribeMissingDomain(t *testing.T) {
	m := newMock(t)

	_, err := m.DescribeDomain(context.Background(), "no-such-domain")

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExResourceNotFound {
		t.Fatalf("want ResourceNotFoundException, got %v", err)
	}
}

func TestUpdateAndDeleteDomain(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, err := m.CreateDomain(ctx, driver.CreateDomainInput{DomainName: "upd-domain"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	policy := `{"Version":"2012-10-17"}`
	cfg, persisted, err := m.UpdateDomainConfig(ctx, driver.UpdateDomainConfigInput{
		DomainName:     "upd-domain",
		AccessPolicies: &policy,
		ClusterConfig:  &driver.ClusterConfig{InstanceType: "m6g.large.search", InstanceCount: 3},
	})
	if err != nil {
		t.Fatalf("UpdateDomainConfig: %v", err)
	}

	if !persisted || cfg.AccessPolicies != policy || cfg.ClusterConfig.InstanceCount != 3 {
		t.Fatalf("update not applied: persisted=%v cfg=%+v", persisted, cfg)
	}

	// Dry run must not persist.
	other := `{"other":true}`
	if _, persisted, _ := m.UpdateDomainConfig(ctx, driver.UpdateDomainConfigInput{
		DomainName: "upd-domain", AccessPolicies: &other, DryRun: true,
	}); persisted {
		t.Fatal("dry run should not persist")
	}

	desc, _ := m.DescribeDomainConfig(ctx, "upd-domain")
	if desc.AccessPolicies != policy {
		t.Fatalf("dry run leaked into stored config: %s", desc.AccessPolicies)
	}

	if _, err := m.DeleteDomain(ctx, "upd-domain"); err != nil {
		t.Fatalf("DeleteDomain: %v", err)
	}

	if _, err := m.DescribeDomain(ctx, "upd-domain"); err == nil {
		t.Fatal("expected domain gone after delete")
	}
}

func TestListDomainNames(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	for _, n := range []string{"alpha", "bravo", "charlie"} {
		if _, err := m.CreateDomain(ctx, driver.CreateDomainInput{DomainName: n}); err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
	}

	list, err := m.ListDomainNames(ctx, "")
	if err != nil {
		t.Fatalf("ListDomainNames: %v", err)
	}

	if len(list) != 3 || list[0].DomainName != "alpha" {
		t.Fatalf("unexpected list (want sorted 3): %+v", list)
	}

	if list[0].EngineType != "OpenSearch" {
		t.Fatalf("engine type = %s, want OpenSearch", list[0].EngineType)
	}
}

func TestTags(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	out, err := m.CreateDomain(ctx, driver.CreateDomainInput{DomainName: "tag-domain"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := m.AddTags(ctx, out.ARN, map[string]string{"team": "search"}); err != nil {
		t.Fatalf("AddTags: %v", err)
	}

	tags, err := m.ListTags(ctx, out.ARN)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}

	if tags["team"] != "search" || tags["env"] != "" {
		t.Fatalf("unexpected tags: %+v", tags)
	}

	if err := m.RemoveTags(ctx, out.ARN, []string{"team"}); err != nil {
		t.Fatalf("RemoveTags: %v", err)
	}

	tags, _ = m.ListTags(ctx, out.ARN)
	if len(tags) != 0 {
		t.Fatalf("expected no tags, got %+v", tags)
	}
}

// TestConcurrentCreate exercises the SetIfAbsent atomic claim under -race:
// exactly one creator wins, the rest see ResourceAlreadyExistsException.
func TestConcurrentCreate(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	const goroutines = 20

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		wins  int
		dupes int
	)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()

			_, err := m.CreateDomain(ctx, driver.CreateDomainInput{DomainName: "race-domain"})

			mu.Lock()
			defer mu.Unlock()

			switch {
			case err == nil:
				wins++
			default:
				var apiErr *driver.APIError
				if errors.As(err, &apiErr) && apiErr.Exception == driver.ExResourceAlreadyExists {
					dupes++
				} else {
					t.Errorf("unexpected error: %v", err)
				}
			}
		}()
	}

	wg.Wait()

	if wins != 1 || dupes != goroutines-1 {
		t.Fatalf("want 1 win and %d dupes, got wins=%d dupes=%d", goroutines-1, wins, dupes)
	}
}

// TestNoAliasOnRead asserts Describe returns a deep copy: mutating a returned
// map/slice must not affect subsequently returned values.
func TestNoAliasOnRead(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, err := m.CreateDomain(ctx, driver.CreateDomainInput{
		DomainName:      "alias-domain",
		AdvancedOptions: map[string]string{"rest.action.multi.allow_explicit_index": "true"},
		Tags:            map[string]string{"env": "prod"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	first, err := m.DescribeDomain(ctx, "alias-domain")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}

	// Mutate the returned map; the stored value must be untouched.
	first.AdvancedOptions["injected"] = "bad"

	second, _ := m.DescribeDomain(ctx, "alias-domain")
	if _, leaked := second.AdvancedOptions["injected"]; leaked {
		t.Fatal("Describe aliased stored AdvancedOptions map")
	}

	// Same for tags.
	tags, _ := m.ListTags(ctx, first.ARN)
	tags["injected"] = "bad"

	tags2, _ := m.ListTags(ctx, first.ARN)
	if _, leaked := tags2["injected"]; leaked {
		t.Fatal("ListTags aliased stored tags map")
	}
}

func TestPackagesLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, err := m.CreateDomain(ctx, driver.CreateDomainInput{DomainName: "pkg-domain"}); err != nil {
		t.Fatalf("create domain: %v", err)
	}

	pkg, err := m.CreatePackage(ctx, driver.CreatePackageInput{PackageName: "dict", PackageType: "TXT-DICTIONARY"})
	if err != nil {
		t.Fatalf("CreatePackage: %v", err)
	}

	assoc, err := m.AssociatePackage(ctx, pkg.PackageID, "pkg-domain")
	if err != nil {
		t.Fatalf("AssociatePackage: %v", err)
	}

	if assoc.DomainName != "pkg-domain" {
		t.Fatalf("unexpected association: %+v", assoc)
	}

	forDomain, _, err := m.ListPackagesForDomain(ctx, "pkg-domain", driver.Page{})
	if err != nil || len(forDomain) != 1 {
		t.Fatalf("ListPackagesForDomain: %v len=%d", err, len(forDomain))
	}

	// Deleting the domain must release the association.
	if _, err := m.DeleteDomain(ctx, "pkg-domain"); err != nil {
		t.Fatalf("delete domain: %v", err)
	}

	forPkg, _, _ := m.ListDomainsForPackage(ctx, pkg.PackageID, driver.Page{})
	if len(forPkg) != 0 {
		t.Fatalf("association not released on domain delete: %+v", forPkg)
	}
}

// TestInvalidPaginationToken asserts a corrupt NextToken is reported as an
// InvalidPaginationTokenException rather than silently restarting at page one.
func TestInvalidPaginationToken(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	_, _, err := m.ListVersions(ctx, driver.Page{NextToken: "not-a-number"})

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExInvalidPaginationToken {
		t.Fatalf("want InvalidPaginationTokenException, got %v", err)
	}

	// An out-of-range (too large) offset is equally invalid.
	if _, _, err := m.ListVersions(ctx, driver.Page{NextToken: "99999"}); err == nil {
		t.Fatal("want error for out-of-range token, got nil")
	} else if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExInvalidPaginationToken {
		t.Fatalf("want InvalidPaginationTokenException, got %v", err)
	}

	// A valid empty token still succeeds.
	if _, _, err := m.ListVersions(ctx, driver.Page{}); err != nil {
		t.Fatalf("empty token should succeed: %v", err)
	}
}

// TestAddTagsOverCap asserts an over-limit AddTags batch is rejected with
// LimitExceededException and does NOT persist any of the batch's tags.
func TestAddTagsOverCap(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	out, err := m.CreateDomain(ctx, driver.CreateDomainInput{DomainName: "cap-domain"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Seed one tag we can later confirm survived the rejected batch.
	if err := m.AddTags(ctx, out.ARN, map[string]string{"keep": "yes"}); err != nil {
		t.Fatalf("seed AddTags: %v", err)
	}

	// A batch that would push the total past the 50-tag cap must be rejected.
	over := make(map[string]string, 50)
	for i := 0; i < 50; i++ {
		over["k"+strconv.Itoa(i)] = "v"
	}

	err = m.AddTags(ctx, out.ARN, over)

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExLimitExceeded {
		t.Fatalf("want LimitExceededException, got %v", err)
	}

	// None of the rejected batch must have persisted; only the seed remains.
	tags, _ := m.ListTags(ctx, out.ARN)
	if len(tags) != 1 || tags["keep"] != "yes" {
		t.Fatalf("rejected batch leaked tags: %+v", tags)
	}
}

// TestDeleteDomainCascades asserts DeleteDomain also removes the domain's VPC
// endpoints and cross-cluster connections, so they stop listing.
func TestDeleteDomainCascades(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	out, err := m.CreateDomain(ctx, driver.CreateDomainInput{DomainName: "cascade-domain"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := m.CreateVpcEndpoint(ctx, out.ARN, driver.VpcOptions{SubnetIDs: []string{"subnet-1"}}, ""); err != nil {
		t.Fatalf("CreateVpcEndpoint: %v", err)
	}

	if _, err := m.CreateOutboundConnection(ctx, driver.CreateOutboundConnectionInput{
		ConnectionAlias: "x",
		LocalDomain:     driver.ConnectionEndpoint{DomainName: "cascade-domain"},
		RemoteDomain:    driver.ConnectionEndpoint{DomainName: "remote-domain"},
	}); err != nil {
		t.Fatalf("CreateOutboundConnection: %v", err)
	}

	eps, _, _ := m.ListVpcEndpoints(ctx, driver.Page{})
	conns, _, _ := m.DescribeOutboundConnections(ctx, driver.Page{})
	if len(eps) != 1 || len(conns) != 1 {
		t.Fatalf("precondition: eps=%d conns=%d", len(eps), len(conns))
	}

	if _, err := m.DeleteDomain(ctx, "cascade-domain"); err != nil {
		t.Fatalf("DeleteDomain: %v", err)
	}

	eps, _, _ = m.ListVpcEndpoints(ctx, driver.Page{})
	if len(eps) != 0 {
		t.Fatalf("VPC endpoint survived domain delete: %+v", eps)
	}

	conns, _, _ = m.DescribeOutboundConnections(ctx, driver.Page{})
	if len(conns) != 0 {
		t.Fatalf("outbound connection survived domain delete: %+v", conns)
	}

	inbound, _, _ := m.DescribeInboundConnections(ctx, driver.Page{})
	if len(inbound) != 0 {
		t.Fatalf("inbound connection survived domain delete: %+v", inbound)
	}
}

// TestDuplicatePackageName asserts package names are unique.
func TestDuplicatePackageName(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, err := m.CreatePackage(ctx, driver.CreatePackageInput{PackageName: "dict", PackageType: "TXT-DICTIONARY"}); err != nil {
		t.Fatalf("first CreatePackage: %v", err)
	}

	_, err := m.CreatePackage(ctx, driver.CreatePackageInput{PackageName: "dict", PackageType: "TXT-DICTIONARY"})

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExResourceAlreadyExists {
		t.Fatalf("want ResourceAlreadyExistsException for duplicate package name, got %v", err)
	}
}

// TestDuplicateApplicationName asserts application names are unique, and that a
// name is freed for reuse after the application is deleted.
func TestDuplicateApplicationName(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	app, err := m.CreateApplication(ctx, driver.CreateApplicationInput{Name: "search-app"})
	if err != nil {
		t.Fatalf("first CreateApplication: %v", err)
	}

	_, err = m.CreateApplication(ctx, driver.CreateApplicationInput{Name: "search-app"})

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExResourceAlreadyExists {
		t.Fatalf("want ResourceAlreadyExistsException for duplicate application name, got %v", err)
	}

	// Deleting frees the name for reuse.
	if err := m.DeleteApplication(ctx, app.ID); err != nil {
		t.Fatalf("DeleteApplication: %v", err)
	}

	if _, err := m.CreateApplication(ctx, driver.CreateApplicationInput{Name: "search-app"}); err != nil {
		t.Fatalf("name should be reusable after delete: %v", err)
	}
}

func TestVersionsAndCompatible(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	versions, _, err := m.ListVersions(ctx, driver.Page{MaxResults: 5})
	if err != nil || len(versions) != 5 {
		t.Fatalf("ListVersions page: %v len=%d", err, len(versions))
	}

	compat, err := m.GetCompatibleVersions(ctx, "")
	if err != nil {
		t.Fatalf("GetCompatibleVersions: %v", err)
	}

	// The newest version has no upgrade targets; an older one has some.
	if len(compat["OpenSearch_1.0"]) == 0 {
		t.Fatalf("expected upgrade targets for OpenSearch_1.0: %+v", compat["OpenSearch_1.0"])
	}
}
