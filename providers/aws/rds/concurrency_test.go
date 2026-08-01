package rds

import (
	"context"
	"sync"
	"testing"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// TestConcurrentDescribeAndMutate runs Describe reads (and iteration of the
// returned map/slice fields, as a real caller does) concurrently with tag and
// parameter mutations. Before the copy-on-read + replace-on-write fix this
// panicked with "concurrent map read and map write" under `go test -race`.
func TestConcurrentDescribeAndMutate(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	inst, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "db", Engine: "mysql", Tags: map[string]string{"seed": "1"},
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := m.CreateDBParameterGroup(ctx, rdsdriver.ParameterGroupConfig{Name: "pg", Family: "mysql8.0"}); err != nil {
		t.Fatalf("CreateDBParameterGroup: %v", err)
	}

	stop := make(chan struct{})

	var readers sync.WaitGroup

	for range 4 {
		readers.Add(1)

		go func() {
			defer readers.Done()

			for {
				select {
				case <-stop:
					return
				default:
				}

				instances, _ := m.DescribeInstances(ctx, nil)
				for i := range instances {
					for k := range instances[i].Tags { // caller iterates the returned map
						_ = k
					}
				}

				groups, _ := m.DescribeDBParameterGroups(ctx, nil)
				for i := range groups {
					for k := range groups[i].Parameters {
						_ = k
					}
				}

				_, _ = m.ListTagsForResource(ctx, inst.ARN)
			}
		}()
	}

	var writers sync.WaitGroup

	for range 4 {
		writers.Add(1)

		go func() {
			defer writers.Done()

			for range 300 {
				_ = m.AddTagsToResource(ctx, inst.ARN, map[string]string{"k": "v"})
				_ = m.RemoveTagsFromResource(ctx, inst.ARN, []string{"k"})
				_, _ = m.ModifyDBParameterGroup(ctx, "pg", []rdsdriver.Parameter{{Name: "max_connections", Value: "100"}})
				_, _ = m.ResetDBParameterGroup(ctx, "pg", nil, true)
			}
		}()
	}

	writers.Wait()
	close(stop)
	readers.Wait()
}
