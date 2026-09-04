package compute

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// TestGCPMutatorsConcurrentWithReads hammers the write-locked GCP mutators
// (disk setLabels, MutateInstanceGCP, ResizeVolumeGCP, AttachVolume/
// DetachVolume) concurrently with Describe reads that RANGE the tag maps. It
// must stay clean under `go test -race`: the mutators are copy-on-write inside
// memstore.Store.Update, so a reader holding a previous record pointer never
// observes an in-place map mutation. The pre-fix Get-then-naked-mutate tripped
// Go's concurrent map access — an unrecoverable process crash.
func TestGCPMutatorsConcurrentWithReads(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	vol, err := m.CreateVolume(ctx, driver.VolumeConfig{
		Size: 10, AvailabilityZone: "us-central1-a",
		Tags: map[string]string{"cloudemu:gcpDiskName": "d1", "seed": "x"},
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	insts, err := m.RunInstances(ctx, driver.InstanceConfig{
		ImageID: "img-1", InstanceType: "n1-standard-1", Tags: map[string]string{"seed": "y"},
	}, 1)
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	instID := insts[0].ID

	const (
		workers = 8
		iters   = 200
	)

	var wg sync.WaitGroup

	spawn := func(fn func(w int)) {
		for w := range workers {
			wg.Add(1)

			go func(w int) {
				defer wg.Done()
				fn(w)
			}(w)
		}
	}

	// Disk label writers (copy-on-write replacement of the tag map).
	spawn(func(w int) {
		for i := range iters {
			_ = m.SetVolumeLabelsGCP(vol.ID, map[string]string{"env": strconv.Itoa(w), "k": strconv.Itoa(i)}, nil)
		}
	})

	// Instance tag writers.
	spawn(func(_ int) {
		for i := range iters {
			_ = m.MutateInstanceGCP(instID, map[string]string{"lbl": strconv.Itoa(i)}, nil, "")
		}
	})

	// Resize + attach/detach churn on the same disk.
	spawn(func(_ int) {
		for i := range iters {
			_ = m.ResizeVolumeGCP(vol.ID, 10+i)
			_ = m.AttachVolume(ctx, vol.ID, instID, "dev")
			_ = m.DetachVolume(ctx, vol.ID, "", "")
		}
	})

	// Readers that iterate the tag maps and read scalar fields.
	spawn(func(_ int) {
		for range iters {
			vols, _ := m.DescribeVolumes(ctx, nil)
			for j := range vols {
				for range vols[j].Tags {
				}

				_ = vols[j].Size
				_ = vols[j].State
			}

			got, _ := m.DescribeInstances(ctx, nil, nil)
			for j := range got {
				for range got[j].Tags {
				}
			}
		}
	})

	wg.Wait()

	// Final state is consistent: the disk survives with its seed label intact
	// (setLabels only ever added keys here) and a grown size; the instance
	// remains present.
	vols, err := m.DescribeVolumes(ctx, []string{vol.ID})
	if err != nil || len(vols) != 1 {
		t.Fatalf("DescribeVolumes final: err=%v n=%d", err, len(vols))
	}

	if vols[0].Tags["seed"] != "x" {
		t.Errorf("seed label lost: %v", vols[0].Tags)
	}

	if vols[0].Size < 10 {
		t.Errorf("final size=%d want >=10", vols[0].Size)
	}

	got, err := m.DescribeInstances(ctx, []string{instID}, nil)
	if err != nil || len(got) != 1 {
		t.Fatalf("DescribeInstances final: err=%v n=%d", err, len(got))
	}
}
