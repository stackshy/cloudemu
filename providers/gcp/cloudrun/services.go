package cloudrun

import (
	"context"
	"fmt"
	"reflect"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
)

const (
	ingressDefault      = "INGRESS_TRAFFIC_ALL"
	trafficTypeLatest   = "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST"
	trafficTypeRevision = "TRAFFIC_TARGET_ALLOCATION_TYPE_REVISION"
	revSuffixBytes      = 2  // 4 hex chars, matching Cloud Run's revision id suffix
	uriHashLen          = 10 // length of the uid-derived hash in the *.run.app URL
	fullPercent         = 100
)

// CreateService stores a service spec, materializes its first revision, and
// returns the reconciled service serving traffic at a stable URL.
//
//nolint:gocritic // hugeParam: cfg is passed by value to satisfy the CloudRun driver interface.
func (m *Mock) CreateService(_ context.Context, cfg driver.ServiceConfig) (*driver.Service, error) {
	name := lastSegment(cfg.Name)
	if name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "service name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.services.Has(name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "service %q already exists", name)
	}

	now := m.opts.Clock.Now()
	svc := &driver.Service{
		Name:       name,
		UID:        newID(uidBytes),
		Generation: 1,
		CreateTime: now,
	}
	applyServiceConfig(svc, &cfg)

	rev := m.materializeRevision(svc, now)
	reconcile(svc, rev, now, regionOrDefault(cfg.Location))

	m.services.Set(name, svc)

	return cloneService(svc), nil
}

// GetService returns a service by id or fully qualified name.
func (m *Mock) GetService(_ context.Context, name string) (*driver.Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	svc, ok := m.services.Get(lastSegment(name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "service %q not found", lastSegment(name))
	}

	return cloneService(svc), nil
}

// ListServices returns every stored service, sorted by id.
func (m *Mock) ListServices(_ context.Context) ([]driver.Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	svcs := m.services.SortedValues()
	out := make([]driver.Service, 0, len(svcs))

	for _, s := range svcs {
		out = append(out, *cloneService(s))
	}

	return out, nil
}

// UpdateService applies an in-place update, materializes a new revision, bumps
// the generation, and returns the reconciled service.
//
//nolint:gocritic // hugeParam: cfg is passed by value to satisfy the CloudRun driver interface.
func (m *Mock) UpdateService(_ context.Context, cfg driver.ServiceConfig) (*driver.Service, error) {
	name := lastSegment(cfg.Name)
	if name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "service name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	svc, ok := m.services.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "service %q not found", name)
	}

	created, uid, gen := svc.CreateTime, svc.UID, svc.Generation+1
	before := snapshotTemplate(svc)

	applyUpdate(svc, &cfg)

	svc.CreateTime, svc.UID, svc.Generation = created, uid, gen
	now := m.opts.Clock.Now()
	region := regionOrDefault(cfg.Location)
	after := snapshotTemplate(svc)

	// Real Cloud Run cuts a new revision only when the revision template changes.
	// A traffic-, label-, or annotation-only update leaves the template intact,
	// so we reconcile status without advancing the revision pointers.
	if templatesEqual(&before, &after) {
		reconcileStatus(svc, now, region)
	} else {
		rev := m.materializeRevision(svc, now)
		reconcile(svc, rev, now, region)
	}

	m.services.Set(name, svc)

	return cloneService(svc), nil
}

// applyUpdate overlays cfg onto svc: a maskless config (Terraform's full PUT)
// replaces every mutable field; a masked config merges only the named paths,
// preserving everything the caller did not send.
func applyUpdate(svc *driver.Service, cfg *driver.ServiceConfig) {
	if len(cfg.UpdateMask) == 0 {
		applyServiceConfig(svc, cfg)
		return
	}

	mergeServiceConfig(svc, cfg, newFieldMask(cfg.UpdateMask))
}

// DeleteService removes a service, all of its revisions, and any Go handler
// registered for it (see RegisterHandler) — otherwise a redeployed service
// reusing the same id would silently inherit the old deployment's handler
// instead of the documented no-handler echo stub.
func (m *Mock) DeleteService(_ context.Context, name string) error {
	id := lastSegment(name)

	m.mu.Lock()

	if !m.services.Has(id) {
		m.mu.Unlock()

		return cerrors.Newf(cerrors.NotFound, "service %q not found", id)
	}

	m.services.Delete(id)

	for _, key := range m.revisions.Keys() {
		if rev, ok := m.revisions.Get(key); ok && rev.Service == id {
			m.revisions.Delete(key)
		}
	}

	m.mu.Unlock()

	m.handlersMu.Lock()
	delete(m.handlers, id)
	m.handlersMu.Unlock()

	return nil
}

// ListRevisions returns every stored revision of the named service, sorted by id.
func (m *Mock) ListRevisions(_ context.Context, serviceName string) ([]driver.Revision, error) {
	id := lastSegment(serviceName)

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.services.Has(id) {
		return nil, cerrors.Newf(cerrors.NotFound, "service %q not found", id)
	}

	return collectByParent(m.revisions.SortedValues(), id,
		func(r *driver.Revision) string { return r.Service }, cloneRevision), nil
}

// GetRevision returns a revision by id or fully qualified name.
func (m *Mock) GetRevision(_ context.Context, name string) (*driver.Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rev, ok := m.revisions.Get(lastSegment(name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "revision %q not found", lastSegment(name))
	}

	return cloneRevision(rev), nil
}

// DeleteRevision removes a single revision of a service.
func (m *Mock) DeleteRevision(_ context.Context, name string) error {
	id := lastSegment(name)

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.revisions.Has(id) {
		return cerrors.Newf(cerrors.NotFound, "revision %q not found", id)
	}

	m.revisions.Delete(id)

	return nil
}

// applyServiceConfig overlays cfg's mutable fields onto svc, defaulting ingress
// and launch stage when the caller omitted them.
func applyServiceConfig(svc *driver.Service, cfg *driver.ServiceConfig) {
	ingress := cfg.Ingress
	if ingress == "" {
		ingress = ingressDefault
	}

	launchStage := cfg.LaunchStage
	if launchStage == "" {
		launchStage = launchStageGA
	}

	svc.Description = cfg.Description
	svc.Ingress = ingress
	svc.LaunchStage = launchStage
	svc.Containers = cloneContainers(cfg.Containers)
	svc.ServiceAccount = cfg.ServiceAccount
	svc.Timeout = cfg.Timeout
	svc.ExecutionEnvironment = cfg.ExecutionEnvironment
	svc.VPCAccess = cloneVPCAccess(cfg.VPCAccess)
	svc.Scaling = cloneScaling(cfg.Scaling)
	svc.Traffic = cloneTraffic(cfg.Traffic)
	svc.Labels = cloneMap(cfg.Labels)
	svc.Annotations = cloneMap(cfg.Annotations)
	svc.TemplateLabels = cloneMap(cfg.TemplateLabels)
	svc.TemplateAnnotations = cloneMap(cfg.TemplateAnnotations)
}

// fieldMask is a parsed FieldMask over a Service PATCH body. A top-level path
// (e.g. "traffic") gates its field; a "template" (or "template.<sub>") path
// gates a revision-template field.
type fieldMask map[string]bool

func newFieldMask(paths []string) fieldMask {
	m := make(fieldMask, len(paths))
	for _, p := range paths {
		m[p] = true
	}

	return m
}

func (m fieldMask) has(field string) bool { return m[field] }

// hasTemplate reports whether the template as a whole ("template") or the named
// template subfield ("template.<sub>") is masked.
func (m fieldMask) hasTemplate(sub string) bool {
	return m["template"] || m["template."+sub]
}

// mergeServiceConfig overlays only the masked paths of cfg onto svc, leaving
// every unmasked field (containers, scaling, description, …) untouched.
func mergeServiceConfig(svc *driver.Service, cfg *driver.ServiceConfig, mask fieldMask) {
	mergeServiceLevel(svc, cfg, mask)
	mergeTemplateLevel(svc, cfg, mask)
}

func mergeServiceLevel(svc *driver.Service, cfg *driver.ServiceConfig, mask fieldMask) {
	if mask.has("description") {
		svc.Description = cfg.Description
	}

	if mask.has("ingress") {
		if cfg.Ingress != "" {
			svc.Ingress = cfg.Ingress
		} else {
			svc.Ingress = ingressDefault
		}
	}

	if mask.has("labels") {
		svc.Labels = cloneMap(cfg.Labels)
	}

	if mask.has("annotations") {
		svc.Annotations = cloneMap(cfg.Annotations)
	}

	if mask.has("traffic") {
		svc.Traffic = cloneTraffic(cfg.Traffic)
	}
}

func mergeTemplateLevel(svc *driver.Service, cfg *driver.ServiceConfig, mask fieldMask) {
	if mask.hasTemplate("containers") {
		svc.Containers = cloneContainers(cfg.Containers)
	}

	if mask.hasTemplate("scaling") {
		svc.Scaling = cloneScaling(cfg.Scaling)
	}

	if mask.hasTemplate("vpcAccess") {
		svc.VPCAccess = cloneVPCAccess(cfg.VPCAccess)
	}

	if mask.hasTemplate("serviceAccount") {
		svc.ServiceAccount = cfg.ServiceAccount
	}

	if mask.hasTemplate("timeout") {
		svc.Timeout = cfg.Timeout
	}

	if mask.hasTemplate("executionEnvironment") {
		svc.ExecutionEnvironment = cfg.ExecutionEnvironment
	}

	if mask.hasTemplate("labels") {
		svc.TemplateLabels = cloneMap(cfg.TemplateLabels)
	}

	if mask.hasTemplate("annotations") {
		svc.TemplateAnnotations = cloneMap(cfg.TemplateAnnotations)
	}
}

// templateSnapshot captures the revision-defining fields of a service so an
// update can tell whether the template changed (and thus a revision is due).
type templateSnapshot struct {
	Containers           []driver.Container
	ServiceAccount       string
	Timeout              string
	ExecutionEnvironment string
	VPCAccess            *driver.VpcAccess
	Scaling              *driver.ServiceScaling
	Labels               map[string]string
	Annotations          map[string]string
}

func snapshotTemplate(svc *driver.Service) templateSnapshot {
	return templateSnapshot{
		Containers:           svc.Containers,
		ServiceAccount:       svc.ServiceAccount,
		Timeout:              svc.Timeout,
		ExecutionEnvironment: svc.ExecutionEnvironment,
		VPCAccess:            svc.VPCAccess,
		Scaling:              svc.Scaling,
		Labels:               svc.TemplateLabels,
		Annotations:          svc.TemplateAnnotations,
	}
}

func templatesEqual(a, b *templateSnapshot) bool {
	return reflect.DeepEqual(a, b)
}

// materializeRevision creates and stores a new revision from svc's current
// template, named {service}-{generation:05d}-{suffix}, and returns it.
func (m *Mock) materializeRevision(svc *driver.Service, now time.Time) *driver.Revision {
	revName := fmt.Sprintf("%s-%05d-%s", svc.Name, svc.Generation, newID(revSuffixBytes))
	rev := &driver.Revision{
		Name:                 revName,
		UID:                  newID(uidBytes),
		Generation:           svc.Generation,
		Service:              svc.Name,
		CreateTime:           now,
		UpdateTime:           now,
		LaunchStage:          svc.LaunchStage,
		Containers:           cloneContainers(svc.Containers),
		ServiceAccount:       svc.ServiceAccount,
		Timeout:              svc.Timeout,
		ExecutionEnvironment: svc.ExecutionEnvironment,
		VPCAccess:            cloneVPCAccess(svc.VPCAccess),
		Scaling:              cloneScaling(svc.Scaling),
		Conditions:           []driver.Condition{{Type: condReady, State: stateSucceeded, Reason: "Ready"}},
		Etag:                 newEtag(now, svc.Generation),
	}

	m.revisions.Set(revName, rev)

	return rev
}

// reconcile rolls the newly materialized revision up onto svc: revision
// pointers, URL, traffic status, terminal condition, generation echoes, and
// etag — the observed state a real reconcile would report.
func reconcile(svc *driver.Service, rev *driver.Revision, now time.Time, region string) {
	svc.LatestCreatedRevision = rev.Name
	svc.LatestReadyRevision = rev.Name
	reconcileStatus(svc, now, region)
}

// reconcileStatus rolls the observed status a real reconcile would report onto
// svc without touching the revision pointers, so a template-unchanged update
// (e.g. traffic-only) can update traffic status and etag without cutting a
// spurious revision.
func reconcileStatus(svc *driver.Service, now time.Time, region string) {
	svc.UpdateTime = now
	svc.ObservedGeneration = svc.Generation
	svc.Reconciling = false
	svc.Etag = newEtag(now, svc.Generation)

	if svc.URI == "" {
		svc.URI = serviceURI(svc, region)
	}

	if len(svc.Traffic) == 0 {
		svc.Traffic = []driver.TrafficTarget{{Type: trafficTypeLatest, Percent: fullPercent}}
	}

	svc.TrafficStatuses = trafficStatuses(svc)
	svc.TerminalCondition = &driver.Condition{Type: condReady, State: stateSucceeded, Reason: "Ready"}
	svc.Conditions = []driver.Condition{{Type: condReady, State: stateSucceeded, Reason: "Ready"}}
}

// regionOrDefault falls back to a canonical region when the path carried none.
func regionOrDefault(region string) string {
	if region == "" {
		return "us-central1"
	}

	return region
}

// serviceURI returns the stable *.run.app URL Cloud Run assigns a service.
func serviceURI(svc *driver.Service, region string) string {
	hash := svc.UID
	if len(hash) > uriHashLen {
		hash = hash[:uriHashLen]
	}

	return fmt.Sprintf("https://%s-%s.%s.run.app", svc.Name, hash, region)
}

// trafficStatuses resolves a service's requested traffic split into observed
// statuses, pinning LATEST targets to the latest ready revision and attaching
// each tagged target's per-tag URL.
func trafficStatuses(svc *driver.Service) []driver.TrafficTarget {
	out := make([]driver.TrafficTarget, 0, len(svc.Traffic))

	for _, t := range svc.Traffic {
		st := driver.TrafficTarget{Type: t.Type, Percent: t.Percent, Tag: t.Tag}

		if t.Type == trafficTypeLatest || t.Revision == "" {
			st.Type = trafficTypeLatest
			st.Revision = svc.LatestReadyRevision
		} else {
			st.Type = trafficTypeRevision
			st.Revision = t.Revision
		}

		if t.Tag != "" {
			st.URI = taggedURI(svc.URI, t.Tag)
		}

		out = append(out, st)
	}

	return out
}

// taggedURI derives a per-tag revision URL from a service URL.
func taggedURI(serviceURL, tag string) string {
	const scheme = "https://"
	if len(serviceURL) > len(scheme) && serviceURL[:len(scheme)] == scheme {
		return scheme + tag + "---" + serviceURL[len(scheme):]
	}

	return serviceURL
}

func cloneService(s *driver.Service) *driver.Service {
	cp := *s
	cp.Containers = cloneContainers(s.Containers)
	cp.Labels = cloneMap(s.Labels)
	cp.Annotations = cloneMap(s.Annotations)
	cp.TemplateLabels = cloneMap(s.TemplateLabels)
	cp.TemplateAnnotations = cloneMap(s.TemplateAnnotations)
	cp.Conditions = append([]driver.Condition(nil), s.Conditions...)
	cp.Traffic = cloneTraffic(s.Traffic)
	cp.TrafficStatuses = cloneTraffic(s.TrafficStatuses)
	cp.VPCAccess = cloneVPCAccess(s.VPCAccess)
	cp.Scaling = cloneScaling(s.Scaling)

	if s.TerminalCondition != nil {
		tc := *s.TerminalCondition
		cp.TerminalCondition = &tc
	}

	return &cp
}

func cloneRevision(r *driver.Revision) *driver.Revision {
	cp := *r
	cp.Containers = cloneContainers(r.Containers)
	cp.Conditions = append([]driver.Condition(nil), r.Conditions...)
	cp.VPCAccess = cloneVPCAccess(r.VPCAccess)
	cp.Scaling = cloneScaling(r.Scaling)

	return &cp
}

func cloneTraffic(in []driver.TrafficTarget) []driver.TrafficTarget {
	if in == nil {
		return nil
	}

	return append([]driver.TrafficTarget(nil), in...)
}

func cloneScaling(in *driver.ServiceScaling) *driver.ServiceScaling {
	if in == nil {
		return nil
	}

	cp := *in

	return &cp
}
