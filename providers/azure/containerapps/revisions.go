package containerapps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

const (
	// modeMultiple is the activeRevisionsMode value that keeps every revision
	// active; any other value (including the default "" / "Single") means only
	// the latest revision stays active.
	modeMultiple = "Multiple"

	// fullTraffic is the weight an app's ingress traffic split must sum to.
	fullTraffic = 100
	// revSuffixLen is how many hex chars of the template hash seed an
	// auto-generated revision suffix when the template sets none.
	revSuffixLen = 8
	// defaultMinReplicas is the replica count reported for an active revision
	// whose template pins no explicit scale.minReplicas.
	defaultMinReplicas = 1

	// Revision provisioning/running/health states mirror the armappcontainers
	// enums a client reads back off a revision.
	revProvisioned = "Provisioned"
	revRunning     = "Running"
	revStopped     = "Stopped"
	healthHealthy  = "Healthy"
	healthNone     = "None"
)

// TrafficWeight is one entry of an app's ingress traffic split: it routes a
// share of inbound requests to a named revision (or the latest revision).
type TrafficWeight struct {
	RevisionName   string `json:"revisionName,omitempty"`
	Weight         int32  `json:"weight"`
	Label          string `json:"label,omitempty"`
	LatestRevision bool   `json:"latestRevision,omitempty"`
}

// Revision is a point-in-time snapshot of a container app's template. Name,
// CreatedTime, Active, Fqdn and Template are durable and persisted with the app;
// the remaining fields are derived at read time from the parent app's
// scale/ingress and are never stored (json:"-"), so a snapshot cannot capture a
// stale computed value.
type Revision struct {
	Name        string `json:"name"`
	CreatedTime string `json:"createdTime"`
	Active      bool   `json:"active"`
	Fqdn        string `json:"fqdn,omitempty"`
	Template    Template

	TrafficWeight     int32  `json:"-"`
	Replicas          int32  `json:"-"`
	ProvisioningState string `json:"-"`
	RunningState      string `json:"-"`
	HealthState       string `json:"-"`
}

// materializeRevisionLocked creates or refreshes the revision the app's current
// template maps to, points LatestRevisionName at it, and reconciles the active
// flags for the app's active-revisions mode. Re-using an explicit
// template.revisionSuffix that already names a revision with a different template
// is rejected, matching Azure ("revision with suffix ... already exists"); an
// identical re-PUT stays idempotent. Callers hold m.mu.
func (m *Mock) materializeRevisionLocked(app *ContainerApp) error {
	suffix := app.Template.RevisionSuffix
	if suffix == "" {
		suffix = templateSuffix(app.Template)
	}

	revName := strings.ToLower(app.Name) + "--" + suffix
	app.LatestRevisionName = revName

	fqdn := ""

	if app.Ingress != nil {
		if domain := m.envDomainLocked(app.EnvironmentID); domain != "" {
			fqdn = revName + "." + domain
		}
	}

	if idx := revisionIndex(app, revName); idx >= 0 {
		if templateSuffix(app.Revisions[idx].Template) != templateSuffix(app.Template) {
			return cerrors.Newf(cerrors.InvalidArgument,
				"revision with suffix %q already exists with a different template", suffix)
		}

		app.Revisions[idx].Fqdn = fqdn
	} else {
		app.Revisions = append(app.Revisions, Revision{
			Name:        revName,
			CreatedTime: m.clock.Now().UTC().Format(time.RFC3339Nano),
			Template:    cloneTemplate(app.Template),
			Fqdn:        fqdn,
		})
	}

	reconcileActive(app, revName)

	return nil
}

// reconcileActive marks the latest revision active. In single-revision mode
// (the default) every other revision is deactivated; in multiple mode the
// others keep whatever active flag they already carry.
func reconcileActive(app *ContainerApp, latest string) {
	multiple := strings.EqualFold(app.ActiveRevMode, modeMultiple)

	for i := range app.Revisions {
		switch {
		case strings.EqualFold(app.Revisions[i].Name, latest):
			app.Revisions[i].Active = true
		case !multiple:
			app.Revisions[i].Active = false
		}
	}
}

// validateTrafficLocked enforces Azure's ingress traffic rules: every entry must
// name an existing revision (or set latestRevision), and the weights must sum to
// 100. An empty traffic block is valid — Azure then routes 100% to the latest
// revision. Callers hold m.mu.
func validateTrafficLocked(app *ContainerApp) error {
	if app.Ingress == nil || len(app.Ingress.Traffic) == 0 {
		return nil
	}

	var sum int32

	for i := range app.Ingress.Traffic {
		t := app.Ingress.Traffic[i]

		switch {
		case t.LatestRevision:
		case t.RevisionName == "":
			return cerrors.New(cerrors.InvalidArgument,
				"traffic weight must specify revisionName or set latestRevision")
		case revisionIndex(app, t.RevisionName) < 0:
			return cerrors.Newf(cerrors.InvalidArgument,
				"traffic references unknown revision %q", t.RevisionName)
		}

		sum += t.Weight
	}

	if sum != fullTraffic {
		return cerrors.Newf(cerrors.InvalidArgument, "sum of traffic weights must be 100, got %d", sum)
	}

	return nil
}

// ListRevisions returns the revision history of a container app, each with its
// derived traffic weight and running state. Returns NotFound when the app does
// not exist.
func (m *Mock) ListRevisions(_ context.Context, sub, rg, appName string) ([]Revision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	app, ok := m.apps.Get(key(sub, rg, typeContainerApps, appName))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container app %q not found", appName)
	}

	out := make([]Revision, 0, len(app.Revisions))
	for i := range app.Revisions {
		out = append(out, withDerived(&app, &app.Revisions[i]))
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedTime != out[j].CreatedTime {
			return out[i].CreatedTime < out[j].CreatedTime
		}

		return out[i].Name < out[j].Name
	})

	return out, nil
}

// GetRevision returns one revision of a container app. Returns NotFound when the
// app or the revision does not exist.
func (m *Mock) GetRevision(_ context.Context, sub, rg, appName, revName string) (Revision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	app, ok := m.apps.Get(key(sub, rg, typeContainerApps, appName))
	if !ok {
		return Revision{}, cerrors.Newf(cerrors.NotFound, "container app %q not found", appName)
	}

	idx := revisionIndex(&app, revName)
	if idx < 0 {
		return Revision{}, cerrors.Newf(cerrors.NotFound, "revision %q not found", revName)
	}

	return withDerived(&app, &app.Revisions[idx]), nil
}

// ActivateRevision marks a revision active. Returns NotFound when the app or the
// revision does not exist.
func (m *Mock) ActivateRevision(_ context.Context, sub, rg, appName, revName string) error {
	return m.setRevisionActive(sub, rg, appName, revName, true)
}

// DeactivateRevision marks a revision inactive. Returns NotFound when the app or
// the revision does not exist.
func (m *Mock) DeactivateRevision(_ context.Context, sub, rg, appName, revName string) error {
	return m.setRevisionActive(sub, rg, appName, revName, false)
}

// RestartRevision restarts a revision's replicas. The emulator holds no live
// replicas, so this is a no-op beyond validating the app and revision exist.
func (m *Mock) RestartRevision(_ context.Context, sub, rg, appName, revName string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	app, ok := m.apps.Get(key(sub, rg, typeContainerApps, appName))
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container app %q not found", appName)
	}

	if revisionIndex(&app, revName) < 0 {
		return cerrors.Newf(cerrors.NotFound, "revision %q not found", revName)
	}

	return nil
}

func (m *Mock) setRevisionActive(sub, rg, appName, revName string, active bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, typeContainerApps, appName)

	app, ok := m.apps.Get(k)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container app %q not found", appName)
	}

	idx := revisionIndex(&app, revName)
	if idx < 0 {
		return cerrors.Newf(cerrors.NotFound, "revision %q not found", revName)
	}

	app.Revisions = cloneRevisions(app.Revisions)
	app.Revisions[idx].Active = active

	// Single-revision mode admits exactly one active revision: activating one
	// makes it the sole active revision (and the traffic target), so it can
	// never leave two active at once. Multiple mode flips the one flag alone.
	if active && !strings.EqualFold(app.ActiveRevMode, modeMultiple) {
		for i := range app.Revisions {
			if i != idx {
				app.Revisions[i].Active = false
			}
		}

		app.LatestRevisionName = app.Revisions[idx].Name
	}

	m.apps.Set(k, app)

	return nil
}

// withDerived returns a copy of rev with its read-time fields (traffic weight,
// replica count and provisioning/running/health states) computed from the
// parent app's ingress and scale.
func withDerived(app *ContainerApp, rev *Revision) Revision {
	out := *rev
	out.TrafficWeight = trafficWeightFor(app, out.Name)
	out.ProvisioningState = revProvisioned

	if out.Active {
		out.RunningState = revRunning
		out.HealthState = healthHealthy
		out.Replicas = minReplicas(&out.Template)
	} else {
		out.RunningState = revStopped
		out.HealthState = healthNone
		out.Replicas = 0
	}

	return out
}

// trafficWeightFor sums the ingress weights routed to revName. With no explicit
// traffic block Azure sends 100% to the latest revision, so that is what a
// revision reports absent configuration.
func trafficWeightFor(app *ContainerApp, revName string) int32 {
	if app.Ingress == nil || len(app.Ingress.Traffic) == 0 {
		if strings.EqualFold(revName, app.LatestRevisionName) {
			return fullTraffic
		}

		return 0
	}

	var sum int32

	for i := range app.Ingress.Traffic {
		t := app.Ingress.Traffic[i]
		if strings.EqualFold(t.RevisionName, revName) ||
			(t.LatestRevision && strings.EqualFold(revName, app.LatestRevisionName)) {
			sum += t.Weight
		}
	}

	return sum
}

func minReplicas(t *Template) int32 {
	if t.Scale != nil && t.Scale.MinReplicas != nil {
		return *t.Scale.MinReplicas
	}

	return defaultMinReplicas
}

// revisionIndex returns the index of the revision named revName (case-insensitive)
// or -1 when the app has no such revision.
func revisionIndex(app *ContainerApp, revName string) int {
	for i := range app.Revisions {
		if strings.EqualFold(app.Revisions[i].Name, revName) {
			return i
		}
	}

	return -1
}

// templateSuffix derives a stable revision suffix from a template's content, so
// two identical templates map to the same revision and a changed template mints
// a new one — mirroring Azure's content-addressed auto suffix.
func templateSuffix(t Template) string {
	data, _ := json.Marshal(t)
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])[:revSuffixLen]
}

func cloneRevisions(in []Revision) []Revision {
	if in == nil {
		return nil
	}

	out := make([]Revision, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Template = cloneTemplate(in[i].Template)
	}

	return out
}
