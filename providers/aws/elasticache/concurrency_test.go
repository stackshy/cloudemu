package elasticache

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	driver "github.com/stackshy/cloudemu/v2/services/cache/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const raceGoroutines = 50

// gatedCacheEngine is a real-engine stand-in whose Provision and Deprovision
// block until released, so a test can act while a create or delete is mid-way
// through its engine call.
type gatedCacheEngine struct {
	provisionEntered   chan struct{}
	provisionRelease   chan struct{}
	deprovisionEntered chan struct{}
	deprovisionRelease chan struct{}
}

func newGatedCacheEngine() *gatedCacheEngine {
	return &gatedCacheEngine{
		provisionEntered:   make(chan struct{}, 1),
		provisionRelease:   make(chan struct{}),
		deprovisionEntered: make(chan struct{}, 1),
		deprovisionRelease: make(chan struct{}),
	}
}

func (e *gatedCacheEngine) Provision(_ context.Context, _ config.CacheProvisionRequest) (config.ProvisionResult, error) {
	e.provisionEntered <- struct{}{}
	<-e.provisionRelease

	return config.ProvisionResult{Host: "127.0.0.1", Port: 6379}, nil
}

func (e *gatedCacheEngine) Deprovision(_ context.Context, _ string) error {
	e.deprovisionEntered <- struct{}{}
	<-e.deprovisionRelease

	return nil
}

func newGatedMock() (*Mock, *gatedCacheEngine) {
	eng := newGatedCacheEngine()
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	return New(config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"), config.WithCacheEngine(eng))), eng
}

// faultCode returns the wire fault code carried by err, or "".
func faultCode(err error) string {
	var f *Fault
	if errors.As(err, &f) {
		return f.AWSFault()
	}

	return ""
}

// runConcurrently runs fn on n goroutines started together and returns how
// many calls succeeded and the fault codes of the ones that failed.
func runConcurrently(n int, fn func() error) (successes int, codes []string) {
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		start = make(chan struct{})
	)

	for range n {
		wg.Add(1)

		go func() {
			defer wg.Done()

			<-start

			err := fn()

			mu.Lock()
			defer mu.Unlock()

			if err == nil {
				successes++
			} else {
				codes = append(codes, faultCode(err))
			}
		}()
	}

	close(start)
	wg.Wait()

	return successes, codes
}

func assertOneWinner(t *testing.T, successes int, codes []string, wantCode string) {
	t.Helper()

	if successes != 1 {
		t.Fatalf("successes = %d, want exactly 1", successes)
	}

	for _, c := range codes {
		if c != wantCode {
			t.Fatalf("losing create got code %q, want %q", c, wantCode)
		}
	}
}

func TestCreateCacheConcurrentDuplicate(t *testing.T) {
	m, _ := newTestMock()

	successes, codes := runConcurrently(raceGoroutines, func() error {
		_, err := m.CreateCache(context.Background(), driver.CacheConfig{Name: "dup"})
		return err
	})

	assertOneWinner(t, successes, codes, FaultCacheClusterAlreadyExists)
}

func TestCreateReplicationGroupConcurrentDuplicate(t *testing.T) {
	m, _ := newTestMock()

	successes, codes := runConcurrently(raceGoroutines, func() error {
		_, err := m.CreateReplicationGroup(context.Background(), driver.ReplicationGroupConfig{ID: "dup"})
		return err
	})

	assertOneWinner(t, successes, codes, FaultReplicationGroupAlreadyExists)
}

func TestCreateCacheSubnetGroupConcurrentDuplicate(t *testing.T) {
	m, _ := newTestMock()

	successes, codes := runConcurrently(raceGoroutines, func() error {
		_, err := m.CreateCacheSubnetGroup(context.Background(), driver.SubnetGroupConfig{
			Name: "dup", SubnetIDs: []string{"subnet-1"},
		})

		return err
	})

	assertOneWinner(t, successes, codes, FaultCacheSubnetGroupAlreadyExists)
}

// TestDeleteSubnetGroupVsCreateCacheRace races cluster creates against deletes
// of the subnet group they are placed in. Either the delete loses with
// CacheSubnetGroupInUse or the create loses with CacheSubnetGroupNotFoundFault;
// a cluster must never end up pointing at a deleted group.
func TestDeleteSubnetGroupVsCreateCacheRace(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	for i := range raceGoroutines {
		sg := fmt.Sprintf("sg-%d", i)
		if _, err := m.CreateCacheSubnetGroup(ctx, driver.SubnetGroupConfig{Name: sg, SubnetIDs: []string{"subnet-1"}}); err != nil {
			t.Fatalf("CreateCacheSubnetGroup: %v", err)
		}

		var wg sync.WaitGroup

		wg.Add(2)

		go func() {
			defer wg.Done()

			_, _ = m.CreateCache(ctx, driver.CacheConfig{Name: "c-" + sg, SubnetGroupName: sg})
		}()

		go func() {
			defer wg.Done()

			_ = m.DeleteCacheSubnetGroup(ctx, sg)
		}()

		wg.Wait()

		_, cacheErr := m.GetCache(ctx, "c-"+sg)
		_, sgErr := m.DescribeCacheSubnetGroups(ctx, []string{sg})

		if cacheErr == nil && sgErr != nil {
			t.Fatalf("cluster c-%s exists but its subnet group was deleted", sg)
		}
	}
}

// TestDeleteSubnetGroupWhileCacheCreating proves a cluster still being
// provisioned counts as a user of its subnet group.
func TestDeleteSubnetGroupWhileCacheCreating(t *testing.T) {
	ctx := context.Background()
	m, eng := newGatedMock()

	if _, err := m.CreateCacheSubnetGroup(ctx, driver.SubnetGroupConfig{Name: "sg", SubnetIDs: []string{"subnet-1"}}); err != nil {
		t.Fatalf("CreateCacheSubnetGroup: %v", err)
	}

	done := make(chan error, 1)

	go func() {
		_, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c1", SubnetGroupName: "sg"})
		done <- err
	}()

	<-eng.provisionEntered

	if code := faultCode(m.DeleteCacheSubnetGroup(ctx, "sg")); code != FaultCacheSubnetGroupInUse {
		t.Fatalf("DeleteCacheSubnetGroup during create: code %q, want %q", code, FaultCacheSubnetGroupInUse)
	}

	close(eng.provisionRelease)

	if err := <-done; err != nil {
		t.Fatalf("CreateCache: %v", err)
	}
}

// TestDeleteParameterGroupWhileCacheCreating proves a create that has
// validated a parameter group keeps it from being deleted until it commits.
func TestDeleteParameterGroupWhileCacheCreating(t *testing.T) {
	ctx := context.Background()
	m, eng := newGatedMock()

	if _, err := m.CreateCacheParameterGroup(ctx, "pg", "redis7", "d"); err != nil {
		t.Fatalf("CreateCacheParameterGroup: %v", err)
	}

	done := make(chan error, 1)

	go func() {
		_, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c1", ParameterGroupName: "pg"})
		done <- err
	}()

	<-eng.provisionEntered

	if code := faultCode(m.DeleteCacheParameterGroup(ctx, "pg")); code != FaultInvalidCacheParameterGroupState {
		t.Fatalf("DeleteCacheParameterGroup during create: code %q, want %q", code, FaultInvalidCacheParameterGroupState)
	}

	close(eng.provisionRelease)

	if err := <-done; err != nil {
		t.Fatalf("CreateCache: %v", err)
	}
}

// TestDeleteParameterGroupVsCreateRace races creates that name a parameter
// group against deletes of that group: a committed cluster must never name a
// deleted group.
func TestDeleteParameterGroupVsCreateRace(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	for i := range raceGoroutines {
		pg := fmt.Sprintf("pg-%d", i)
		if _, err := m.CreateCacheParameterGroup(ctx, pg, "redis7", "d"); err != nil {
			t.Fatalf("CreateCacheParameterGroup: %v", err)
		}

		var wg sync.WaitGroup

		wg.Add(2)

		go func() {
			defer wg.Done()

			_, _ = m.CreateCache(ctx, driver.CacheConfig{Name: "c-" + pg, ParameterGroupName: pg})
		}()

		go func() {
			defer wg.Done()

			_ = m.DeleteCacheParameterGroup(ctx, pg)
		}()

		wg.Wait()

		_, cacheErr := m.GetCache(ctx, "c-"+pg)
		_, pgErr := m.DescribeCacheParameterGroups(ctx, []string{pg})

		if cacheErr == nil && pgErr != nil {
			t.Fatalf("cluster c-%s exists but its parameter group was deleted", pg)
		}
	}
}

// TestDescribeReservedCreatingReturnsCreating proves a cluster whose engine is
// still being provisioned is describable with status creating.
func TestDescribeReservedCreatingReturnsCreating(t *testing.T) {
	ctx := context.Background()
	m, eng := newGatedMock()

	done := make(chan error, 1)

	go func() {
		_, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c1", Engine: "redis"})
		done <- err
	}()

	<-eng.provisionEntered

	info, err := m.GetCache(ctx, "c1")
	if err != nil || info.Status != statusCreating {
		t.Fatalf("GetCache during create = %+v, %v; want status %q", info, err, statusCreating)
	}

	list, err := m.ListCaches(ctx, info.Scope)
	if err != nil || len(list) != 1 || list[0].Status != statusCreating {
		t.Fatalf("ListCaches during create = %+v, %v; want one creating cluster", list, err)
	}

	close(eng.provisionRelease)

	if err := <-done; err != nil {
		t.Fatalf("CreateCache: %v", err)
	}

	if info, _ = m.GetCache(ctx, "c1"); info.Status != statusAvailable {
		t.Fatalf("status after create = %q, want %q", info.Status, statusAvailable)
	}
}

// TestModifyReservedCreatingIsInvalidState proves mutating calls on a cluster
// or replication group that is still being created report the invalid-state
// fault, not not-found.
func TestModifyReservedCreatingIsInvalidState(t *testing.T) {
	ctx := context.Background()
	m, eng := newGatedMock()

	cacheDone := make(chan error, 1)

	go func() {
		_, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c1", Engine: "redis"})
		cacheDone <- err
	}()

	<-eng.provisionEntered

	_, err := m.ModifyCache(ctx, driver.ModifyCacheConfig{Name: "c1", NodeType: "cache.m5.large"})
	if code := faultCode(err); code != FaultInvalidCacheClusterState {
		t.Fatalf("ModifyCache during create: code %q, want %q", code, FaultInvalidCacheClusterState)
	}

	if code := faultCode(m.DeleteCache(ctx, "c1")); code != FaultInvalidCacheClusterState {
		t.Fatalf("DeleteCache during create: code %q, want %q", code, FaultInvalidCacheClusterState)
	}

	close(eng.provisionRelease)

	if err := <-cacheDone; err != nil {
		t.Fatalf("CreateCache: %v", err)
	}

	eng.provisionRelease = make(chan struct{})
	rgDone := make(chan error, 1)

	go func() {
		_, err := m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{ID: "rg1", Engine: "redis"})
		rgDone <- err
	}()

	<-eng.provisionEntered

	_, err = m.ModifyReplicationGroup(ctx, "rg1", 2)
	if code := faultCode(err); code != FaultInvalidReplicationGroupState {
		t.Fatalf("ModifyReplicationGroup during create: code %q, want %q", code, FaultInvalidReplicationGroupState)
	}

	groups, err := m.DescribeReplicationGroups(ctx, []string{"rg1"})
	if err != nil || groups[0].Status != statusCreating {
		t.Fatalf("DescribeReplicationGroups during create = %+v, %v; want creating", groups, err)
	}

	close(eng.provisionRelease)

	if err := <-rgDone; err != nil {
		t.Fatalf("CreateReplicationGroup: %v", err)
	}
}

// TestRecreateWhileDeprovisioning proves an id stays taken until the real
// engine has been torn down, so the same engine slot is never provisioned
// twice, and that Describe reports the cluster as deleting meanwhile.
func TestRecreateWhileDeprovisioning(t *testing.T) {
	ctx := context.Background()
	m, eng := newGatedMock()

	close(eng.provisionRelease)

	if _, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c1", Engine: "redis"}); err != nil {
		t.Fatalf("CreateCache: %v", err)
	}

	<-eng.provisionEntered

	done := make(chan error, 1)

	go func() { done <- m.DeleteCache(ctx, "c1") }()

	<-eng.deprovisionEntered

	info, err := m.GetCache(ctx, "c1")
	if err != nil || info.Status != statusDeleting {
		t.Fatalf("GetCache during delete = %+v, %v; want status %q", info, err, statusDeleting)
	}

	_, err = m.CreateCache(ctx, driver.CacheConfig{Name: "c1", Engine: "redis"})
	if code := faultCode(err); code != FaultCacheClusterAlreadyExists {
		t.Fatalf("CreateCache during delete: code %q, want %q", code, FaultCacheClusterAlreadyExists)
	}

	if code := faultCode(m.DeleteCache(ctx, "c1")); code != FaultInvalidCacheClusterState {
		t.Fatalf("DeleteCache during delete: code %q, want %q", code, FaultInvalidCacheClusterState)
	}

	close(eng.deprovisionRelease)

	if err := <-done; err != nil {
		t.Fatalf("DeleteCache: %v", err)
	}

	if _, err := m.GetCache(ctx, "c1"); !cerrors.IsNotFound(err) {
		t.Fatalf("GetCache after delete: %v, want not found", err)
	}

	if _, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c1", Engine: "redis"}); err != nil {
		t.Fatalf("CreateCache after delete: %v", err)
	}
}

// TestDescribeDuringDeprovisionReturnsDeleting covers the replication-group
// side of the deleting reservation.
func TestDescribeDuringDeprovisionReturnsDeleting(t *testing.T) {
	ctx := context.Background()
	m, eng := newGatedMock()

	close(eng.provisionRelease)

	if _, err := m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{ID: "rg1", Engine: "redis"}); err != nil {
		t.Fatalf("CreateReplicationGroup: %v", err)
	}

	<-eng.provisionEntered

	done := make(chan error, 1)

	go func() { done <- m.DeleteReplicationGroup(ctx, "rg1", driver.DeleteReplicationGroupOptions{}) }()

	<-eng.deprovisionEntered

	groups, err := m.DescribeReplicationGroups(ctx, nil)
	if err != nil || len(groups) != 1 || groups[0].Status != statusDeleting {
		t.Fatalf("DescribeReplicationGroups during delete = %+v, %v; want one deleting group", groups, err)
	}

	_, err = m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{ID: "rg1"})
	if code := faultCode(err); code != FaultReplicationGroupAlreadyExists {
		t.Fatalf("CreateReplicationGroup during delete: code %q, want %q", code, FaultReplicationGroupAlreadyExists)
	}

	close(eng.deprovisionRelease)

	if err := <-done; err != nil {
		t.Fatalf("DeleteReplicationGroup: %v", err)
	}

	if _, err := m.DescribeReplicationGroups(ctx, []string{"rg1"}); faultCode(err) != FaultReplicationGroupNotFound {
		t.Fatalf("DescribeReplicationGroups after delete: %v, want ReplicationGroupNotFoundFault", err)
	}
}

// TestSnapshotRestoreSerialisedWithCreate runs persist Snapshot and Restore
// alongside creates and tag writes; under -race it proves they share mu.
func TestSnapshotRestoreSerialisedWithCreate(t *testing.T) {
	ctx := context.Background()
	src, _ := newTestMock()
	dst, _ := newTestMock()

	var wg sync.WaitGroup

	for i := range raceGoroutines {
		wg.Add(1)

		go func() {
			defer wg.Done()

			name := fmt.Sprintf("c%d", i)
			if _, err := src.CreateCache(ctx, driver.CacheConfig{Name: name, Tags: map[string]string{"k": "v"}}); err != nil {
				t.Errorf("CreateCache: %v", err)
				return
			}

			_ = src.AddTags(ctx, src.cacheARN("us-east-1", name), map[string]string{"i": name})

			raw, err := src.Snapshot(ctx, true)
			if err != nil {
				t.Errorf("Snapshot: %v", err)
				return
			}

			if err := dst.Restore(ctx, raw); err != nil {
				t.Errorf("Restore: %v", err)
			}
		}()
	}

	wg.Wait()

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	list, err := dst.ListCaches(ctx, scope.Scope{})
	if err != nil || len(list) != raceGoroutines {
		t.Fatalf("restored %d clusters (%v), want %d", len(list), err, raceGoroutines)
	}
}

func TestCreateCacheParameterGroupConcurrentDuplicate(t *testing.T) {
	m, _ := newTestMock()

	successes, codes := runConcurrently(raceGoroutines, func() error {
		_, err := m.CreateCacheParameterGroup(context.Background(), "dup", "redis7", "d")
		return err
	})

	assertOneWinner(t, successes, codes, FaultCacheParameterGroupAlreadyExists)
}

// TestModifyVsResetParameterGroupRace interleaves modifies, resets and reads of
// one parameter group. Under -race it proves the overrides map is never
// written while a reader holds it.
func TestModifyVsResetParameterGroupRace(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	if _, err := m.CreateCacheParameterGroup(ctx, "pg", "redis7", "d"); err != nil {
		t.Fatalf("CreateCacheParameterGroup: %v", err)
	}

	var wg sync.WaitGroup

	for i := range raceGoroutines {
		wg.Add(3)

		go func() {
			defer wg.Done()

			if err := m.ModifyCacheParameterGroup(ctx, "pg", []ParameterUpdate{
				{Name: "maxmemory-policy", Value: "allkeys-lru"}, {Name: "timeout", Value: strconv.Itoa(i + 20)},
			}); err != nil {
				t.Errorf("ModifyCacheParameterGroup: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if err := m.ResetCacheParameterGroup(ctx, "pg", i%2 == 0, []string{"timeout"}); err != nil {
				t.Errorf("ResetCacheParameterGroup: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			readParameterGroup(t, m, "pg")
		}()
	}

	wg.Wait()
}

// readParameterGroup reads a group's overrides and parameters the way a
// Describe caller would.
func readParameterGroup(t *testing.T, m *Mock, name string) {
	t.Helper()

	groups, err := m.DescribeCacheParameterGroups(context.Background(), []string{name})
	if err != nil {
		t.Errorf("DescribeCacheParameterGroups: %v", err)
		return
	}

	seen := 0
	for range groups[0].Overrides {
		seen++
	}

	if seen > 2 {
		t.Errorf("overrides = %d entries, want at most 2", seen)
	}

	if _, err := m.DescribeCacheParameters(context.Background(), name, ""); err != nil {
		t.Errorf("DescribeCacheParameters: %v", err)
	}
}

// snapshotRaceResult holds the outcome of one round of
// TestSnapshotVsDeleteReplicationGroupRace.
type snapshotRaceResult struct {
	manual, final, copied, deleted error
}

// TestSnapshotVsDeleteReplicationGroupRace races snapshot create, copy and
// delete against deleting the replication group they back up. The manual
// snapshot and the group's final snapshot share a name, so exactly one of them
// wins; a snapshot of the group never outlives a failed lookup; and copy and
// delete of one snapshot are serialized.
func TestSnapshotVsDeleteReplicationGroupRace(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	for i := range raceGoroutines {
		rg := fmt.Sprintf("rg%d", i)
		if _, err := m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{ID: rg, Engine: "redis"}); err != nil {
			t.Fatalf("CreateReplicationGroup: %v", err)
		}

		if _, err := m.CreateSnapshot(ctx, driver.SnapshotConfig{SnapshotName: "base-" + rg, ReplicationGroupID: rg}); err != nil {
			t.Fatalf("CreateSnapshot: %v", err)
		}

		var (
			wg  sync.WaitGroup
			res snapshotRaceResult
		)

		wg.Add(4)

		go func() {
			defer wg.Done()

			_, res.manual = m.CreateSnapshot(ctx, driver.SnapshotConfig{SnapshotName: "snap-" + rg, ReplicationGroupID: rg})
		}()

		go func() {
			defer wg.Done()

			res.final = m.DeleteReplicationGroup(ctx, rg, driver.DeleteReplicationGroupOptions{FinalSnapshotIdentifier: "snap-" + rg})
		}()

		go func() {
			defer wg.Done()

			_, res.copied = m.CopySnapshot(ctx, driver.CopySnapshotConfig{SourceSnapshotName: "base-" + rg, TargetSnapshotName: "copy-" + rg})
		}()

		go func() {
			defer wg.Done()

			_, res.deleted = m.DeleteSnapshot(ctx, "base-"+rg)
		}()

		wg.Wait()

		checkSnapshotRaceOutcome(t, m, rg, &res)
	}
}

// checkSnapshotRaceOutcome asserts the invariants of one round.
func checkSnapshotRaceOutcome(t *testing.T, m *Mock, rg string, res *snapshotRaceResult) {
	t.Helper()

	ctx := context.Background()

	// Manual first: it wins and the final snapshot then collides, so the group
	// survives. Delete first: the final snapshot wins and the manual one finds
	// no group. Never both, never neither.
	if (res.manual == nil) == (res.final == nil) {
		t.Fatalf("%s: manual snapshot err %v, delete err %v; want exactly one to succeed", rg, res.manual, res.final)
	}

	// Losing to the delete, the manual snapshot finds the final snapshot's name
	// already taken (the name is checked first) or, failing that, no group.
	if code := faultCode(res.manual); res.manual != nil &&
		code != FaultSnapshotAlreadyExists && code != FaultReplicationGroupNotFound {
		t.Fatalf("%s: manual snapshot error %v, want SnapshotAlreadyExistsFault or ReplicationGroupNotFoundFault", rg, res.manual)
	}

	if res.final != nil && faultCode(res.final) != FaultSnapshotAlreadyExists {
		t.Fatalf("%s: delete error %v, want SnapshotAlreadyExistsFault", rg, res.final)
	}

	_, describeErr := m.DescribeReplicationGroups(ctx, []string{rg})
	if (res.final == nil) != (faultCode(describeErr) == FaultReplicationGroupNotFound) {
		t.Fatalf("%s: delete err %v but describe err %v", rg, res.final, describeErr)
	}

	if res.deleted != nil {
		t.Fatalf("%s: DeleteSnapshot of base: %v", rg, res.deleted)
	}

	if res.copied != nil && faultCode(res.copied) != FaultSnapshotNotFound {
		t.Fatalf("%s: CopySnapshot error %v, want nil or SnapshotNotFoundFault", rg, res.copied)
	}

	if _, err := m.DescribeSnapshots(ctx, driver.SnapshotFilter{SnapshotName: "base-" + rg}); faultCode(err) != FaultSnapshotNotFound {
		t.Fatalf("%s: base snapshot survived its delete: %v", rg, err)
	}
}

// TestCreateCacheCollidingWithReservedGroupMember proves a replication group
// still being created owns its member ids, so a cluster named "<rg>-002" is
// refused during the window, and that a group whose member id is already a
// cluster is refused too.
func TestCreateCacheCollidingWithReservedGroupMember(t *testing.T) {
	ctx := context.Background()
	m, eng := newGatedMock()

	done := make(chan error, 1)

	go func() {
		_, err := m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{ID: "rg", Engine: "redis", NumCacheNodes: 2})
		done <- err
	}()

	<-eng.provisionEntered

	_, err := m.CreateCache(ctx, driver.CacheConfig{Name: "rg-002", Engine: "memcached"})
	if code := faultCode(err); code != FaultCacheClusterAlreadyExists {
		t.Fatalf("CreateCache(rg-002) during group create: code %q, want %q", code, FaultCacheClusterAlreadyExists)
	}

	close(eng.provisionRelease)

	if err := <-done; err != nil {
		t.Fatalf("CreateReplicationGroup: %v", err)
	}

	plain, _ := newTestMock()

	if _, err := plain.CreateCache(ctx, driver.CacheConfig{Name: "g2-001", Engine: "memcached"}); err != nil {
		t.Fatalf("CreateCache: %v", err)
	}

	_, err = plain.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{ID: "g2"})
	if code := faultCode(err); code != FaultCacheClusterAlreadyExists {
		t.Fatalf("CreateReplicationGroup over a cluster's id: code %q, want %q", code, FaultCacheClusterAlreadyExists)
	}
}
