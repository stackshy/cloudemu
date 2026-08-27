package sfn

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// sfnSnapshot is the full serialized state of the Step Functions mock. Each
// store wraps its driver record in an unexported *xData holding a per-record
// mutex that json.Marshal cannot see, so the exported driver value is promoted
// out of each wrapper keyed by its resource id. The execution settle overlay is
// a transient Describe-surface window and is intentionally not captured; neither
// are the per-record mutexes nor the wired *config.Options.
type sfnSnapshot struct {
	Machines   map[string]driver.StateMachine `json:"machines,omitempty"`
	Executions map[string]driver.Execution    `json:"executions,omitempty"`
	Activities map[string]driver.Activity     `json:"activities,omitempty"`
	Aliases    map[string]driver.Alias        `json:"aliases,omitempty"`
	MapRuns    map[string]driver.MapRun       `json:"mapRuns,omitempty"`
	Tasks      map[string]string              `json:"tasks,omitempty"`
}

// snapshotWrapped promotes a store of mutex-guarded *D wrappers to a plain
// map[id]V, reading each record's exported value through get (which locks the
// record's own mutex).
func snapshotWrapped[D, V any](s *memstore.Store[*D], get func(*D) V) map[string]V {
	if s.Len() == 0 {
		return nil
	}

	out := make(map[string]V, s.Len())
	for id, d := range s.All() {
		out[id] = get(d)
	}

	return out
}

// restoreWrapped reinstates each id→V under a freshly-built *D wrapper. build
// takes a pointer to avoid copying heavy driver values by value.
func restoreWrapped[D, V any](s *memstore.Store[*D], in map[string]V, build func(*V) *D) {
	for id, v := range in {
		rec := build(&v)
		s.Set(id, rec)
	}
}

func getSM(d *smData) driver.StateMachine  { d.mu.RLock(); defer d.mu.RUnlock(); return d.sm }
func getExec(d *execData) driver.Execution { d.mu.RLock(); defer d.mu.RUnlock(); return d.exec }
func getAct(d *actData) driver.Activity    { d.mu.RLock(); defer d.mu.RUnlock(); return d.act }
func getAlias(d *aliasData) driver.Alias   { d.mu.RLock(); defer d.mu.RUnlock(); return d.alias }
func getRun(d *mapRunData) driver.MapRun   { d.mu.RLock(); defer d.mu.RUnlock(); return d.run }

func buildSM(v *driver.StateMachine) *smData  { return &smData{sm: *v} }
func buildExec(v *driver.Execution) *execData { return &execData{exec: *v} }
func buildAct(v *driver.Activity) *actData    { return &actData{act: *v} }
func buildAlias(v *driver.Alias) *aliasData   { return &aliasData{alias: *v} }
func buildRun(v *driver.MapRun) *mapRunData   { return &mapRunData{run: *v} }

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Step Functions holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := sfnSnapshot{
		Machines:   snapshotWrapped(m.machines, getSM),
		Executions: snapshotWrapped(m.executions, getExec),
		Activities: snapshotWrapped(m.activities, getAct),
		Aliases:    snapshotWrapped(m.aliases, getAlias),
		MapRuns:    snapshotWrapped(m.mapRuns, getRun),
	}

	m.tasksMu.RLock()
	if len(m.tasks) > 0 {
		snap.Tasks = make(map[string]string, len(m.tasks))
		for k, v := range m.tasks {
			snap.Tasks[k] = v
		}
	}
	m.tasksMu.RUnlock()

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: every state
// machine, execution, activity, alias, and Map Run is reinstated under its
// stored id, along with the activity-task token bookkeeping.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap sfnSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("sfn: parse snapshot: %w", err)
	}

	restoreWrapped(m.machines, snap.Machines, buildSM)
	restoreWrapped(m.executions, snap.Executions, buildExec)
	restoreWrapped(m.activities, snap.Activities, buildAct)
	restoreWrapped(m.aliases, snap.Aliases, buildAlias)
	restoreWrapped(m.mapRuns, snap.MapRuns, buildRun)

	if len(snap.Tasks) > 0 {
		m.tasksMu.Lock()
		for k, v := range snap.Tasks {
			m.tasks[k] = v
		}
		m.tasksMu.Unlock()
	}

	return nil
}
