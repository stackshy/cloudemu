// Package batch provides an in-memory mock implementation of AWS Batch: the
// compute-environment, job-queue, and job-definition control plane. Resources
// provision synchronously (VALID/ACTIVE immediately), ARNs and ECS cluster ARNs
// are synthesized, and job-definition revisions increment on re-register.
package batch

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

// Compile-time check that Mock implements driver.Batch.
var _ driver.Batch = (*Mock)(nil)

// Resource kinds, used as the ARN resource-type segment and to route tag ops.
const (
	kindComputeEnvironment = "compute-environment"
	kindJobQueue           = "job-queue"
	kindJobDefinition      = "job-definition"
)

// Mock is an in-memory implementation of AWS Batch. Each resource kind lives in
// its own store keyed by name; job-definition revisions are keyed by
// "name:revision" with a separate max-revision counter. Every stored value is a
// plain JSON-serializable struct, so snapshotting is a direct store dump and
// reads are copy-on-write (mutations build fresh values, never mutating stored
// ones in place).
type Mock struct {
	computeEnvs  *memstore.Store[driver.ComputeEnvironment]
	jobQueues    *memstore.Store[driver.JobQueue]
	jobDefs      *memstore.Store[driver.JobDefinition]
	jobDefMaxRev *memstore.Store[int32]

	// registerMu serializes job-definition revision allocation so two concurrent
	// RegisterJobDefinition calls for the same name can't claim the same revision.
	registerMu sync.Mutex

	opts *config.Options
}

// New creates a new AWS Batch mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		computeEnvs:  memstore.New[driver.ComputeEnvironment](),
		jobQueues:    memstore.New[driver.JobQueue](),
		jobDefs:      memstore.New[driver.JobDefinition](),
		jobDefMaxRev: memstore.New[int32](),
		opts:         opts,
	}
}

func (m *Mock) arn(kind, resource string) string {
	return idgen.AWSARN("batch", m.opts.Region, m.opts.AccountID, kind+"/"+resource)
}

// ecsClusterARN synthesizes the stable ECS cluster ARN Batch reports for a
// managed compute environment (arn:aws:ecs:...:cluster/<name>_Batch_<uuid>).
func (m *Mock) ecsClusterARN(name, uuid string) string {
	return idgen.AWSARN("ecs", m.opts.Region, m.opts.AccountID, "cluster/"+name+"_Batch_"+uuid)
}

func jobDefKey(name string, revision int32) string {
	return name + ":" + strconv.FormatInt(int64(revision), 10)
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}

	out := make([]string, len(in))
	copy(out, in)

	return out
}

func copyRaw(in json.RawMessage) json.RawMessage {
	if in == nil {
		return nil
	}

	out := make(json.RawMessage, len(in))
	copy(out, in)

	return out
}

func copyInt32(in *int32) *int32 {
	if in == nil {
		return nil
	}

	v := *in

	return &v
}

func copyBool(in *bool) *bool {
	if in == nil {
		return nil
	}

	v := *in

	return &v
}

// describeByNames returns the named resources (by name or ARN) in request
// order, or every resource (name order) when no filter is given, cloning each so
// callers never share a stored value.
func describeByNames[V any](store *memstore.Store[V], names []string, kind string, clone func(V) V) []V {
	if len(names) == 0 {
		all := store.SortedValues()
		out := make([]V, 0, len(all))

		for i := range all {
			out = append(out, clone(all[i]))
		}

		return out
	}

	out := make([]V, 0, len(names))

	for _, n := range names {
		if v, ok := store.Get(nameFromARN(kind, n)); ok {
			out = append(out, clone(v))
		}
	}

	return out
}

// nameFromARN returns the bare resource name for a value that may be either a
// name or a full Batch ARN, so Describe/Delete accept both forms.
func nameFromARN(kind, s string) string {
	prefix := ":" + kind + "/"

	i := strings.Index(s, prefix)
	if i < 0 {
		return s
	}

	return s[i+len(prefix):]
}
