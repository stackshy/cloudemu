package cloudfunctions_test

import (
	"context"
	"strconv"
	"sync"
	"testing"

	cloudfunctions2 "google.golang.org/api/cloudfunctions/v2"
)

// TestSDKGen2ConcurrentGetPatch drives Get and Patch against the SAME gen2
// function from many goroutines at once. patchV2 mutates the stored
// serviceConfig in place and reassigns nested pointers under the write lock; a
// reader that marshals the stored object (or a shallow copy of it) after
// releasing the lock aliases those pointers and races the writer. This test
// fails under -race on the pre-fix code (read at getV2->writeJSON vs write at
// mergeServiceConfig) and passes once readers deep-copy under the lock.
func TestSDKGen2ConcurrentGetPatch(t *testing.T) {
	svc := newGCPV2Service(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"
	name := parent + "/functions/g2"

	if _, err := svc.Projects.Locations.Functions.Create(parent, &cloudfunctions2.Function{
		BuildConfig: &cloudfunctions2.BuildConfig{
			Runtime:    "go121",
			EntryPoint: "Hello",
			Source: &cloudfunctions2.Source{
				StorageSource: &cloudfunctions2.StorageSource{Bucket: "b", Object: "o.zip"},
			},
			EnvironmentVariables: map[string]string{"K": "V"},
		},
		ServiceConfig: &cloudfunctions2.ServiceConfig{
			AvailableMemory:      "256M",
			EnvironmentVariables: map[string]string{"FOO": "bar"},
		},
	}).FunctionId("g2").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	const iterations = 40

	var wg sync.WaitGroup

	// Writers: patch serviceConfig in place repeatedly.
	wg.Add(1)

	go func() {
		defer wg.Done()

		for i := 0; i < iterations; i++ {
			_, err := svc.Projects.Locations.Functions.Patch(name, &cloudfunctions2.Function{
				ServiceConfig: &cloudfunctions2.ServiceConfig{
					AvailableMemory:      strconv.Itoa(i) + "M",
					EnvironmentVariables: map[string]string{"FOO": strconv.Itoa(i)},
				},
			}).UpdateMask("serviceConfig").Context(ctx).Do()
			if err != nil {
				t.Errorf("Patch: %v", err)
				return
			}
		}
	}()

	// Readers: concurrent Get and List on the same function while it is patched.
	for r := 0; r < 4; r++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := 0; i < iterations; i++ {
				if _, err := svc.Projects.Locations.Functions.Get(name).Context(ctx).Do(); err != nil {
					t.Errorf("Get: %v", err)
					return
				}

				if _, err := svc.Projects.Locations.Functions.List(parent).Context(ctx).Do(); err != nil {
					t.Errorf("List: %v", err)
					return
				}
			}
		}()
	}

	wg.Wait()
}
