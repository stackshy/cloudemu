package apprunner_test

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

func TestDeleteAllRevisionsAndLatestPromotion(t *testing.T) {
	m := newMock()

	mk := func() *driver.AutoScalingConfiguration {
		t.Helper()

		c, err := m.CreateAutoScalingConfiguration(bg, &driver.CreateAutoScalingConfigurationInput{AutoScalingConfigurationName: "revtest"})
		requireNoError(t, err)

		return c
	}

	r1, r2 := mk(), mk()
	assertStr(t, itoa(r2.AutoScalingConfigurationRevision), "2")

	base := strings.Join(strings.Split(r2.AutoScalingConfigurationArn, "/")[:2], "/")

	// Deleting the latest revision makes the highest remaining one latest again, so
	// the name-only ARN and the default list still find it.
	_, err := m.DeleteAutoScalingConfiguration(bg, r2.AutoScalingConfigurationArn, false)
	requireNoError(t, err)

	got, err := m.DescribeAutoScalingConfiguration(bg, base)
	requireNoError(t, err)
	assertStr(t, got.AutoScalingConfigurationArn, r1.AutoScalingConfigurationArn)

	list, _, err := m.ListAutoScalingConfigurations(bg, "revtest", true, driver.Page{})
	requireNoError(t, err)
	assertStr(t, itoa(int32(len(list))), "1")

	// The deleted revision number is never handed out again.
	r3 := mk()
	assertStr(t, itoa(r3.AutoScalingConfigurationRevision), "3")

	// DeleteAllRevisions needs the name-only ARN and removes every revision.
	_, err = m.DeleteAutoScalingConfiguration(bg, r3.AutoScalingConfigurationArn, true)
	requireInvalidRequest(t, err)

	_, err = m.DeleteAutoScalingConfiguration(bg, base, true)
	requireNoError(t, err)

	all, _, _ := m.ListAutoScalingConfigurations(bg, "revtest", false, driver.Page{})
	assertStr(t, itoa(int32(len(all))), "0")
}

func itoa(n int32) string { return strconv.Itoa(int(n)) }

func TestDeleteAllRevisionsIsAllOrNothing(t *testing.T) {
	m := newMock()

	first, _ := m.CreateAutoScalingConfiguration(bg, &driver.CreateAutoScalingConfigurationInput{AutoScalingConfigurationName: "shared"})
	_, _ = m.CreateAutoScalingConfiguration(bg, &driver.CreateAutoScalingConfigurationInput{AutoScalingConfigurationName: "shared"})

	in := namedService("uses-rev1")
	in.AutoScalingConfigurationArn = first.AutoScalingConfigurationArn
	_, err := m.CreateService(bg, in)
	requireNoError(t, err)

	base := strings.Join(strings.Split(first.AutoScalingConfigurationArn, "/")[:2], "/")

	_, err = m.DeleteAutoScalingConfiguration(bg, base, true)
	requireInvalidRequest(t, err) // revision 1 is in use

	all, _, _ := m.ListAutoScalingConfigurations(bg, "shared", false, driver.Page{})
	assertStr(t, itoa(int32(len(all))), "2") // nothing was deleted
}

func TestObservabilityPartialArnsRevisionsAndPromotion(t *testing.T) {
	m := newMock()

	mk := func() *driver.ObservabilityConfiguration {
		c, err := m.CreateObservabilityConfiguration(bg, &driver.CreateObservabilityConfigurationInput{
			ObservabilityConfigurationName: "obs-cfg", TraceConfiguration: &driver.TraceConfiguration{Vendor: "AWSXRAY"},
		})
		requireNoError(t, err)

		return c
	}

	r1, r2 := mk(), mk()
	base := strings.Join(strings.Split(r2.ObservabilityConfigurationArn, "/")[:2], "/")

	got, err := m.DescribeObservabilityConfiguration(bg, base)
	requireNoError(t, err)
	assertStr(t, got.ObservabilityConfigurationArn, r2.ObservabilityConfigurationArn)

	pinned, err := m.DescribeObservabilityConfiguration(bg, base+"/1")
	requireNoError(t, err)
	assertStr(t, pinned.ObservabilityConfigurationArn, r1.ObservabilityConfigurationArn)

	_, err = m.DeleteObservabilityConfiguration(bg, base) // latest by name
	requireNoError(t, err)

	got, err = m.DescribeObservabilityConfiguration(bg, base)
	requireNoError(t, err)
	assertStr(t, got.ObservabilityConfigurationArn, r1.ObservabilityConfigurationArn)

	r3 := mk()
	assertStr(t, itoa(r3.ObservabilityConfigurationRevision), "3")
}

func TestVpcConnectorRevisionsAreNotReused(t *testing.T) {
	m := newMock()

	mk := func() *driver.VpcConnector {
		c, err := m.CreateVpcConnector(bg, &driver.CreateVpcConnectorInput{VpcConnectorName: "conn", Subnets: []string{"subnet-1"}})
		requireNoError(t, err)

		return c
	}

	c1 := mk()
	_, err := m.DeleteVpcConnector(bg, c1.VpcConnectorArn)
	requireNoError(t, err)

	assertStr(t, itoa(mk().VpcConnectorRevision), "2")
}

func TestRevisionMarksSurviveSnapshotRestore(t *testing.T) {
	m := newMock()

	c, _ := m.CreateAutoScalingConfiguration(bg, &driver.CreateAutoScalingConfigurationInput{AutoScalingConfigurationName: "marked"})
	_, err := m.DeleteAutoScalingConfiguration(bg, c.AutoScalingConfigurationArn, false)
	requireNoError(t, err)

	data, err := m.Snapshot(bg, false)
	requireNoError(t, err)

	m2 := newMock()
	requireNoError(t, m2.Restore(bg, data))

	again, err := m2.CreateAutoScalingConfiguration(bg, &driver.CreateAutoScalingConfigurationInput{AutoScalingConfigurationName: "marked"})
	requireNoError(t, err)
	assertStr(t, itoa(again.AutoScalingConfigurationRevision), "2")
}

func TestConcurrentTagsAreNotLostToServiceUpdates(t *testing.T) {
	m := newMock()
	svc := mustNamed(t, m, "tag-race")

	const n = 20

	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()

			_ = m.TagResource(bg, svc.ServiceArn, []driver.Tag{{Key: "k" + string(rune('a'+i)), Value: "v"}})
		}()

		go func() {
			defer wg.Done()

			_, _ = m.UpdateService(bg, &driver.UpdateServiceInput{
				ServiceArn: svc.ServiceArn, HealthCheckConfiguration: &driver.HealthCheckConfiguration{Protocol: "TCP"},
			})
		}()
	}

	wg.Wait()

	tags, err := m.ListTagsForResource(bg, svc.ServiceArn)
	requireNoError(t, err)

	if len(tags) < n {
		t.Fatalf("%d of %d tags survived concurrent service updates", len(tags), n)
	}
}

func TestInstanceCpuMemoryPairs(t *testing.T) {
	m := newMock()

	for _, ok := range [][2]string{{"256", "512"}, {"0.5 vCPU", "1 GB"}, {"1024", "3072"}, {"2 vCPU", "6 GB"}, {"4096", "12288"}} {
		in := namedService("pair-ok")
		in.InstanceConfiguration = &driver.InstanceConfiguration{CPU: ok[0], Memory: ok[1]}

		res, err := m.CreateService(bg, in)
		requireNoError(t, err)

		_, err = m.DeleteService(bg, res.Service.ServiceArn)
		requireNoError(t, err)
	}

	for _, bad := range [][2]string{{"256", "12 GB"}, {"4096", "512"}, {"1024", "6144"}, {"0.25 vCPU", "1 GB"}} {
		in := namedService("pair-bad")
		in.InstanceConfiguration = &driver.InstanceConfiguration{CPU: bad[0], Memory: bad[1]}

		_, err := m.CreateService(bg, in)
		requireInvalidRequest(t, err)
	}
}

func TestOperationInProgressHasNoEndedAt(t *testing.T) {
	m, clk := newAsyncMock()
	res, err := m.CreateService(bg, namedService("op-ended"))
	requireNoError(t, err)

	clk.Advance(time.Minute)

	_, err = m.PauseService(bg, res.Service.ServiceArn)
	requireNoError(t, err)

	ops, _, err := m.ListOperations(bg, res.Service.ServiceArn, driver.Page{})
	requireNoError(t, err)
	assertStr(t, ops[0].Status, driver.OpStatusInProgress)

	if !ops[0].EndedAt.IsZero() {
		t.Fatalf("an operation in progress must have no EndedAt, got %v", ops[0].EndedAt)
	}

	clk.Advance(time.Minute)

	ops, _, _ = m.ListOperations(bg, res.Service.ServiceArn, driver.Page{})
	if ops[0].EndedAt.IsZero() {
		t.Fatal("a finished operation has an EndedAt")
	}
}
