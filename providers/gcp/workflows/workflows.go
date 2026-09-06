// Package workflows provides an in-memory mock of the Google Cloud Workflows
// control plane (workflows.googleapis.com/v1). It models workflow definitions
// and the long-running operations their mutating RPCs return. It is
// control-plane only: workflow execution (executions, callbacks, step logs) is
// out of scope.
//
// A workflow's revisionId is derived deterministically from its
// revision-defining fields (sourceContents, serviceAccount) and bumps only when
// one of them changes on a Patch — matching real GCP, where "modifying
// source_contents or service_account results in a new workflow revision". Every
// other computed field (state, createTime, updateTime) is minted once and stays
// stable across reads so a Terraform refresh never drifts.
package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	wdriver "github.com/stackshy/cloudemu/v2/services/workflows/driver"
)

var _ wdriver.Workflows = (*Mock)(nil)

const (
	workflowsColl = "workflows"

	// stateActive is the output-only lifecycle state a healthy workflow reports.
	stateActive = "ACTIVE"

	// revisionSuffixLen is the number of hex characters in the "-XXX" suffix of a
	// revisionId (real GCP uses a short alphanumeric code, e.g. 000001-a1b).
	revisionSuffixLen = 3
)

// Mock is the in-memory Cloud Workflows control-plane implementation. The single
// resource collection is keyed by its full GCP resource name.
type Mock struct {
	mu sync.RWMutex

	items      *memstore.Store[wdriver.Resource]
	operations *memstore.Store[wdriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Cloud Workflows mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		items:      memstore.New[wdriver.Resource](),
		operations: memstore.New[wdriver.Operation](),
		opts:       opts,
	}
}

// resourceName builds the full resource name for a workflow.
func resourceName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + workflowsColl + "/" + id
}

// notFoundErr builds the NOT_FOUND error carrying the full resource name, as
// real Cloud Workflows does for a Get/Patch/Delete of a missing workflow.
func notFoundErr(project, location, id string) error {
	return cerrors.Newf(cerrors.NotFound, "workflow %q not found", resourceName(project, location, id))
}

// newOp records a completed operation scoped to the project+location it acted in
// and returns it. The caller holds the write lock.
func (m *Mock) newOp(project, location, opType, target string) *wdriver.Operation {
	scope := "projects/" + project + "/locations/" + location
	op := wdriver.Operation{
		Name:       fmt.Sprintf("%s/operations/operation-%d-%s", scope, m.opSeq.Add(1), idgen.UUID()),
		Done:       true,
		TargetName: target,
		Type:       opType,
	}
	m.operations.Set(op.Name, op)

	return &op
}

// CreateWorkflow provisions a new workflow with computed output fields derived
// deterministically, and returns the completed LRO.
func (m *Mock) CreateWorkflow(_ context.Context, cfg *wdriver.Config) (*wdriver.Resource, *wdriver.Operation, error) {
	if cfg.ID == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "workflow id is required")
	}

	if cfg.Location == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "location is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(cfg.Project, cfg.Location, cfg.ID)
	if m.items.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "workflow %q already exists", cfg.ID)
	}

	now := m.opts.Clock.Now().UTC()
	res := wdriver.Resource{
		Project:            cfg.Project,
		Location:           cfg.Location,
		ID:                 cfg.ID,
		Revision:           1,
		State:              stateActive,
		CreateTime:         now,
		UpdateTime:         now,
		RevisionCreateTime: now,
		Fields:             cloneRawMap(cfg.Fields),
	}
	res.RevisionID = mintRevisionID(res.Revision, revisionSignature(res.Fields))
	m.items.Set(key, res)

	op := m.newOp(cfg.Project, cfg.Location, "create", key)
	out := cloneResource(&res)

	return &out, op, nil
}

// GetWorkflow returns a workflow by identity, cloned.
func (m *Mock) GetWorkflow(_ context.Context, project, location, id string) (*wdriver.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r, ok := m.items.Get(resourceName(project, location, id))
	if !ok {
		return nil, notFoundErr(project, location, id)
	}

	out := cloneResource(&r)

	return &out, nil
}

// ListWorkflows returns every workflow in a project+location.
func (m *Mock) ListWorkflows(_ context.Context, project, location string) ([]wdriver.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/locations/" + location + "/" + workflowsColl + "/"
	all := m.items.SortedValues()
	out := make([]wdriver.Resource, 0, len(all))

	for i := range all {
		key := resourceName(all[i].Project, all[i].Location, all[i].ID)
		if strings.HasPrefix(key, prefix) {
			out = append(out, cloneResource(&all[i]))
		}
	}

	return out, nil
}

// PatchWorkflow applies a masked update and returns the completed LRO. Only the
// masked top-level body fields are written; a field outside the mask is left
// untouched. The revisionId (and revisionCreateTime) bump only when a
// revision-defining field (sourceContents or serviceAccount) actually changes.
func (m *Mock) PatchWorkflow(_ context.Context, cfg *wdriver.Config, mask []string) (
	*wdriver.Resource, *wdriver.Operation, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(cfg.Project, cfg.Location, cfg.ID)

	r, ok := m.items.Get(key)
	if !ok {
		return nil, nil, notFoundErr(cfg.Project, cfg.Location, cfg.ID)
	}

	oldSig := revisionSignature(r.Fields)
	applyMask(&r, cfg.Fields, mask)

	now := m.opts.Clock.Now().UTC()
	r.UpdateTime = now

	if revisionSignature(r.Fields) != oldSig {
		r.Revision++
		r.RevisionID = mintRevisionID(r.Revision, revisionSignature(r.Fields))
		r.RevisionCreateTime = now
	}

	m.items.Set(key, r)

	op := m.newOp(cfg.Project, cfg.Location, "update", key)
	out := cloneResource(&r)

	return &out, op, nil
}

// DeleteWorkflow removes a workflow and returns the completed LRO.
func (m *Mock) DeleteWorkflow(_ context.Context, project, location, id string) (*wdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(project, location, id)
	if !m.items.Has(key) {
		return nil, notFoundErr(project, location, id)
	}

	m.items.Delete(key)

	return m.newOp(project, location, "delete", key), nil
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op
// id an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*wdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &wdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// applyMask folds the masked top-level body fields from desired into the stored
// resource. A mask path's first segment names the top-level field to replace; a
// masked field absent from desired is deleted. An empty mask replaces every
// field present in desired (lenient full-body update).
func applyMask(r *wdriver.Resource, desired map[string]json.RawMessage, mask []string) {
	if r.Fields == nil {
		r.Fields = map[string]json.RawMessage{}
	}

	if len(mask) == 0 {
		for k, v := range desired {
			r.Fields[k] = append(json.RawMessage(nil), v...)
		}

		return
	}

	for _, path := range mask {
		field := path
		if i := strings.IndexByte(path, '.'); i >= 0 {
			field = path[:i]
		}

		if v, ok := desired[field]; ok {
			r.Fields[field] = append(json.RawMessage(nil), v...)
			continue
		}

		delete(r.Fields, field)
	}
}

// revisionSignature returns the concatenation of the revision-defining fields
// (sourceContents, serviceAccount) so a Patch can detect whether the workflow
// revision must bump. Real GCP mints a new revision only when one of these two
// fields changes; every other field (labels, description, …) leaves the
// revision untouched.
func revisionSignature(fields map[string]json.RawMessage) string {
	return string(fields["sourceContents"]) + "\x00" + string(fields["serviceAccount"])
}

// mintRevisionID derives the deterministic "NNNNNN-XXX" revisionId a workflow
// reports: a zero-padded revision number and a short suffix hashed from the
// revision-defining signature. It is stable for a given revision+signature, so a
// Terraform refresh reads the same value, and changes cleanly when the source or
// service account changes.
func mintRevisionID(revision uint64, signature string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(signature))
	suffix := fmt.Sprintf("%016x", h.Sum64())[:revisionSuffixLen]

	return fmt.Sprintf("%06d-%s", revision, suffix)
}
