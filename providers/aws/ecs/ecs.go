// Package ecs provides an in-memory mock implementation of Amazon ECS
// (Elastic Container Service). It satisfies services/ecs/driver.ECS so the
// real aws-sdk-go-v2/service/ecs client works against it via the AWS server.
package ecs

import (
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// Compile-time check that Mock implements driver.ECS.
var _ driver.ECS = (*Mock)(nil)

const (
	statusActive   = "ACTIVE"
	statusInactive = "INACTIVE"
	statusRunning  = "RUNNING"
	statusStopped  = "STOPPED"
	defaultCluster = "default"
)

// Mock is an in-memory mock implementation of Amazon ECS.
type Mock struct {
	clusters  *memstore.Store[*driver.Cluster]
	taskDefs  *memstore.Store[*driver.TaskDefinition] // keyed by "family:revision"
	tasks     *memstore.Store[*driver.Task]           // keyed by task ARN
	services  *memstore.Store[*driver.Service]        // keyed by "cluster/name"
	instances *memstore.Store[*driver.ContainerInstance]
	opts      *config.Options
	regMu     sync.Mutex // serializes task-definition revision allocation
}

// New creates a new ECS mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:  memstore.New[*driver.Cluster](),
		taskDefs:  memstore.New[*driver.TaskDefinition](),
		tasks:     memstore.New[*driver.Task](),
		services:  memstore.New[*driver.Service](),
		instances: memstore.New[*driver.ContainerInstance](),
		opts:      opts,
	}
}

func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(time.RFC3339)
}

func (m *Mock) arn(resource string) string {
	return idgen.AWSARN("ecs", m.opts.Region, m.opts.AccountID, resource)
}

// hexID returns a 32-character hex id with no dashes, matching the ECS
// resource-id shape used in task and container-instance ARNs. idgen.GenerateID
// emits 8 hex digits per call, so four calls yield the 32-char id.
func (*Mock) hexID() string {
	return idgen.GenerateID("") + idgen.GenerateID("") + idgen.GenerateID("") + idgen.GenerateID("")
}

// resolveClusterName accepts a cluster name or ARN and returns the bare name,
// defaulting to "default" when empty.
func resolveClusterName(id string) string {
	if id == "" {
		return defaultCluster
	}

	if i := strings.LastIndex(id, "cluster/"); i >= 0 {
		return id[i+len("cluster/"):]
	}

	return id
}

// clusterNameFromARN extracts the cluster name from a cluster ARN, or returns
// the input unchanged if it is not an ARN.
func clusterNameFromARN(arn string) string {
	if i := strings.LastIndex(arn, "cluster/"); i >= 0 {
		return arn[i+len("cluster/"):]
	}

	return arn
}

// familyFromTaskDefARN extracts the family from a task-definition ARN
// (…:task-definition/family:revision).
func familyFromTaskDefARN(arn string) string {
	i := strings.LastIndex(arn, "task-definition/")
	if i < 0 {
		return ""
	}

	rest := arn[i+len("task-definition/"):]
	if j := strings.LastIndex(rest, ":"); j >= 0 {
		return rest[:j]
	}

	return rest
}

func copyTags(in []driver.Tag) []driver.Tag {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.Tag, len(in))
	copy(out, in)

	return out
}
