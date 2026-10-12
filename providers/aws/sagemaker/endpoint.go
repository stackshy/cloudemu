package sagemaker

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// defaultVariantWeight is the InitialVariantWeight SageMaker assigns when the
// request omits it.

// endpointTransition is the in-flight (read-time) state of an endpoint that is
// Creating, Updating or Deleting under --async-settle. The store always holds
// the FINAL state; reads overlay this window against the Clock, so no goroutine
// is needed and the default (synchronous) mode never creates one. Transitions
// are not snapshotted: a restored mock reports final states.
type endpointTransition struct {
	kind       string // driver.EndpointCreating | EndpointUpdating | EndpointDeleting
	startedAt  time.Time
	readyAt    time.Time
	prev       []driver.VariantSummary // what was serving before the transition
	prevConfig string
}

func (t *endpointTransition) active(now time.Time) bool { return now.Before(t.readyAt) }

// CreateEndpoint creates an endpoint. In synchronous mode it is InService
// immediately; under --async-settle it is Creating for a short window.
func (m *Mock) CreateEndpoint(ctx context.Context, cfg driver.EndpointSpec) (*driver.Endpoint, error) {
	ep, err := m.createEndpoint(cfg)
	if err != nil {
		return nil, err
	}

	m.emitEndpointEvents(ctx, ep, driver.EndpointCreating, driver.EndpointInService)

	return ep, nil
}

// createEndpoint holds the endpoint lock for the whole create, so the event is
// published by the caller after it is released.
func (m *Mock) createEndpoint(cfg driver.EndpointSpec) (*driver.Endpoint, error) {
	if cfg.EndpointName == "" {
		return nil, errors.New(errors.InvalidArgument, "endpointName is required")
	}

	if cfg.ConfigName == "" {
		return nil, errors.New(errors.InvalidArgument, "endpointConfigName is required")
	}

	ec, ok := m.endpointConfigs.Get(cfg.ConfigName)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "endpoint config %q not found", cfg.ConfigName)
	}

	m.epMu.Lock()
	defer m.epMu.Unlock()

	now := m.opts.Clock.Now()
	stamp := m.now()
	arn := m.arn("endpoint/" + cfg.EndpointName)
	ep := &driver.Endpoint{
		EndpointName:     cfg.EndpointName,
		EndpointARN:      arn,
		ConfigName:       cfg.ConfigName,
		Status:           driver.EndpointInService,
		Variants:         copyVariants(ec.ProductionVariants),
		VariantSummaries: m.variantSummaries(ec.ProductionVariants, stamp),
		CreationTime:     stamp,
		LastModifiedTime: stamp,
		Tags:             copyTags(cfg.Tags),
	}

	if !m.endpoints.SetIfAbsent(cfg.EndpointName, ep) {
		return nil, errors.Newf(errors.AlreadyExists, "endpoint %q already exists", cfg.EndpointName)
	}

	m.setTags(arn, cfg.Tags)
	m.beginEndpointTransition(cfg.EndpointName, driver.EndpointCreating, now, settle.DefaultEndpointSettle, nil, "")

	return m.observeEndpoint(ep, now), nil
}

// variantSummaries builds the runtime view of config variants: serving counts
// equal the configured values, and each variant lists the container images of
// its model.
func (m *Mock) variantSummaries(variants []driver.ProductionVariant, stamp string) []driver.VariantSummary {
	out := make([]driver.VariantSummary, 0, len(variants))

	for i := range variants {
		v := &variants[i]
		weight := v.InitialVariantWeight

		out = append(out, driver.VariantSummary{
			VariantName:          v.VariantName,
			DeployedImages:       m.deployedImages(v.ModelName, stamp),
			CurrentWeight:        weight,
			DesiredWeight:        weight,
			CurrentInstanceCount: v.InitialInstanceCount,
			DesiredInstanceCount: v.InitialInstanceCount,
		})
	}

	return out
}

// deployedImages lists the images of a variant's model. An unknown model has no
// images (endpoint configs are not validated against models, as before).
func (m *Mock) deployedImages(modelName, stamp string) []driver.DeployedImage {
	model, ok := m.models.Get(modelName)
	if !ok {
		return nil
	}

	out := make([]driver.DeployedImage, 0, len(model.Containers))

	for _, c := range model.Containers {
		if c.Image == "" {
			continue
		}

		out = append(out, driver.DeployedImage{SpecifiedImage: c.Image, ResolvedImage: c.Image, ResolutionTime: stamp})
	}

	return out
}

// beginEndpointTransition records an in-flight window for name. With
// --async-settle off the duration is zero and nothing is recorded. The caller
// holds epMu.
func (m *Mock) beginEndpointTransition(
	name, kind string, now time.Time, d time.Duration, prev []driver.VariantSummary, prevConfig string,
) {
	dur := m.opts.SettleDuration(d)
	if dur <= 0 {
		delete(m.epTrans, name)

		return
	}

	m.epTrans[name] = &endpointTransition{
		kind: kind, startedAt: now, readyAt: now.Add(dur), prev: copySummaries(prev), prevConfig: prevConfig,
	}
}

// observeEndpoint returns a deep copy of ep with the transition overlay (if
// any) applied at now. The caller holds epMu.
func (m *Mock) observeEndpoint(ep *driver.Endpoint, now time.Time) *driver.Endpoint {
	out := cloneEndpoint(ep)

	tr, ok := m.epTrans[ep.EndpointName]
	if !ok || !tr.active(now) {
		return out
	}

	started := tr.startedAt.UTC().Format(time.RFC3339Nano)
	out.Status = tr.kind

	if tr.kind == driver.EndpointDeleting {
		return out
	}

	desired := out.VariantSummaries
	out.VariantSummaries = overlaySummaries(tr, desired, started)

	if tr.kind == driver.EndpointUpdating && tr.prevConfig != "" {
		out.Pending = &driver.PendingDeployment{ConfigName: out.ConfigName, Variants: copySummaries(desired), StartTime: started}
		out.ConfigName = tr.prevConfig
	}

	if tr.kind == driver.EndpointCreating {
		out.Pending = &driver.PendingDeployment{ConfigName: out.ConfigName, Variants: copySummaries(desired), StartTime: started}
	}

	return out
}

// overlaySummaries merges what served before the transition (Current*) with
// what it is moving to (Desired*), marking each variant's in-flight status.
func overlaySummaries(tr *endpointTransition, final []driver.VariantSummary, started string) []driver.VariantSummary {
	status := driver.VariantUpdating
	if tr.kind == driver.EndpointCreating {
		status = driver.VariantCreating
	}

	prev := make(map[string]driver.VariantSummary, len(tr.prev))
	for _, p := range tr.prev {
		prev[p.VariantName] = p
	}

	out := make([]driver.VariantSummary, 0, len(final))

	for _, f := range final {
		s := f
		s.DeployedImages = append([]driver.DeployedImage(nil), f.DeployedImages...)
		s.CurrentWeight, s.CurrentInstanceCount = 0, 0

		if p, ok := prev[f.VariantName]; ok {
			s.CurrentWeight, s.CurrentInstanceCount = p.CurrentWeight, p.CurrentInstanceCount
		}

		s.VariantStatus = []driver.VariantStatus{{Status: status, StartTime: started}}
		out = append(out, s)
	}

	return out
}

func copySummaries(in []driver.VariantSummary) []driver.VariantSummary {
	if in == nil {
		return nil
	}

	out := make([]driver.VariantSummary, len(in))

	for i, s := range in {
		s.DeployedImages = append([]driver.DeployedImage(nil), s.DeployedImages...)
		s.VariantStatus = append([]driver.VariantStatus(nil), s.VariantStatus...)
		out[i] = s
	}

	return out
}

// cloneEndpoint deep-copies every slice and pointer so callers never alias
// stored state.
func cloneEndpoint(in *driver.Endpoint) *driver.Endpoint {
	out := *in
	out.Variants = copyVariants(in.Variants)
	out.Tags = copyTags(in.Tags)
	out.VariantSummaries = copySummaries(in.VariantSummaries)
	out.LastDeploymentConfig = append([]byte(nil), in.LastDeploymentConfig...)

	if in.Pending != nil {
		p := *in.Pending
		p.Variants = copySummaries(in.Pending.Variants)
		out.Pending = &p
	}

	return &out
}

// liveEndpoint returns the observed endpoint, purging one whose Deleting window
// has elapsed. The caller holds epMu.
func (m *Mock) liveEndpoint(name string, now time.Time) (*driver.Endpoint, bool) {
	ep, ok := m.endpoints.Get(name)
	if !ok {
		return nil, false
	}

	if tr, has := m.epTrans[name]; has && tr.kind == driver.EndpointDeleting && !tr.active(now) {
		m.purgeEndpoint(name)

		return nil, false
	}

	return m.observeEndpoint(ep, now), true
}

func (m *Mock) purgeEndpoint(name string) {
	m.endpoints.Delete(name)
	delete(m.epTrans, name)
	m.dropTags(m.arn("endpoint/" + name))
}

// endpointStatus returns the observed status of name, or "" when it does not
// exist.
func (m *Mock) endpointStatus(name string) string {
	m.epMu.Lock()
	defer m.epMu.Unlock()

	ep, ok := m.liveEndpoint(name, m.opts.Clock.Now())
	if !ok {
		return ""
	}

	return ep.Status
}

// DescribeEndpoint returns an endpoint with its observed lifecycle state.
func (m *Mock) DescribeEndpoint(_ context.Context, name string) (*driver.Endpoint, error) {
	m.epMu.Lock()
	defer m.epMu.Unlock()

	ep, ok := m.liveEndpoint(name, m.opts.Clock.Now())
	if !ok {
		return nil, errors.Newf(errors.NotFound, "endpoint %q not found", name)
	}

	return ep, nil
}

// ListEndpoints lists endpoints with their observed status; filters and sort
// apply to that observed state.
//
//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (m *Mock) ListEndpoints(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.Endpoint], error) {
	m.epMu.Lock()
	now := m.opts.Clock.Now()
	stored := m.endpoints.SortedValues()
	observed := make([]*driver.Endpoint, 0, len(stored))

	for _, ep := range stored {
		if live, ok := m.liveEndpoint(ep.EndpointName, now); ok {
			observed = append(observed, live)
		}
	}
	m.epMu.Unlock()

	return listItems(observed, listSpec[driver.Endpoint]{
		name:     func(t *driver.Endpoint) string { return t.EndpointName },
		status:   func(t *driver.Endpoint) string { return t.Status },
		created:  func(t *driver.Endpoint) string { return t.CreationTime },
		modified: func(t *driver.Endpoint) string { return t.LastModifiedTime },
	}, opts)
}

// requireInService returns the observed endpoint or the error an update should
// fail with. Only an InService endpoint accepts updates. The caller holds epMu.
func (m *Mock) requireInService(name string, now time.Time) (*driver.Endpoint, error) {
	ep, ok := m.liveEndpoint(name, now)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "endpoint %q not found", name)
	}

	if ep.Status != driver.EndpointInService {
		return nil, errors.Newf(errors.FailedPrecondition,
			"Cannot update endpoint %q: its status is %s, not InService", name, ep.Status)
	}

	return ep, nil
}

// UpdateEndpoint deploys a new endpoint configuration. The endpoint stays
// available (Updating serves traffic) and returns to InService.
//
//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (m *Mock) UpdateEndpoint(ctx context.Context, spec driver.UpdateEndpointSpec) (*driver.Endpoint, error) {
	ep, err := m.updateEndpoint(spec)
	if err != nil {
		return nil, err
	}

	m.emitEndpointEvents(ctx, ep, driver.EndpointUpdating, driver.EndpointInService)

	return ep, nil
}

//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (m *Mock) updateEndpoint(spec driver.UpdateEndpointSpec) (*driver.Endpoint, error) {
	ec, ok := m.endpointConfigs.Get(spec.ConfigName)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "endpoint config %q not found", spec.ConfigName)
	}

	m.epMu.Lock()
	defer m.epMu.Unlock()

	now := m.opts.Clock.Now()

	current, err := m.requireInService(spec.EndpointName, now)
	if err != nil {
		return nil, err
	}

	stamp := m.now()
	summaries := m.variantSummaries(ec.ProductionVariants, stamp)
	retainSummaries(summaries, current.VariantSummaries, spec)

	updated := *current
	updated.ConfigName = spec.ConfigName
	updated.Variants = copyVariants(ec.ProductionVariants)
	updated.VariantSummaries = summaries
	updated.LastModifiedTime = stamp
	updated.Pending = nil

	switch {
	case len(spec.DeploymentConfig) > 0:
		updated.LastDeploymentConfig = append([]byte(nil), spec.DeploymentConfig...)
	case !spec.RetainDeploymentConfig:
		updated.LastDeploymentConfig = nil
	}

	m.endpoints.Set(spec.EndpointName, &updated)
	m.beginEndpointTransition(spec.EndpointName, driver.EndpointUpdating, now,
		settle.DefaultEndpointUpdateSettle, current.VariantSummaries, current.ConfigName)

	return m.observeEndpoint(&updated, now), nil
}

// retainSummaries carries a same-named variant's desired weight and instance
// count over from the endpoint's current state when the request asks to retain
// variant properties (minus any excluded property).
//
//nolint:gocritic // driver-signature params and SDK-shaped helpers
func retainSummaries(next, current []driver.VariantSummary, spec driver.UpdateEndpointSpec) {
	if !spec.RetainAllVariantProperties {
		return
	}

	excluded := map[string]bool{}
	for _, e := range spec.ExcludeRetained {
		excluded[e] = true
	}

	byName := make(map[string]driver.VariantSummary, len(current))
	for _, c := range current {
		byName[c.VariantName] = c
	}

	for i := range next {
		c, ok := byName[next[i].VariantName]
		if !ok {
			continue
		}

		if !excluded["DesiredWeight"] {
			next[i].CurrentWeight, next[i].DesiredWeight = c.DesiredWeight, c.DesiredWeight
		}

		if !excluded["DesiredInstanceCount"] {
			next[i].CurrentInstanceCount, next[i].DesiredInstanceCount = c.DesiredInstanceCount, c.DesiredInstanceCount
		}
	}
}

// UpdateEndpointWeightsAndCapacities changes the traffic weight and/or instance
// count of existing variants without changing the endpoint configuration.
func (m *Mock) UpdateEndpointWeightsAndCapacities(
	ctx context.Context, name string, weights []driver.VariantWeight,
) (*driver.Endpoint, error) {
	ep, err := m.updateEndpointWeights(name, weights)
	if err != nil {
		return nil, err
	}

	m.emitEndpointEvents(ctx, ep, driver.EndpointUpdating, driver.EndpointInService)

	return ep, nil
}

func (m *Mock) updateEndpointWeights(name string, weights []driver.VariantWeight) (*driver.Endpoint, error) {
	if len(weights) == 0 {
		return nil, errors.New(errors.InvalidArgument, "desiredWeightsAndCapacities must contain at least 1 item")
	}

	m.epMu.Lock()
	defer m.epMu.Unlock()

	now := m.opts.Clock.Now()

	current, err := m.requireInService(name, now)
	if err != nil {
		return nil, err
	}

	summaries := copySummaries(current.VariantSummaries)
	if err := applyWeights(summaries, weights); err != nil {
		return nil, err
	}

	updated := *current
	updated.VariantSummaries = summaries
	updated.LastModifiedTime = m.now()
	updated.Pending = nil

	m.endpoints.Set(name, &updated)
	m.beginEndpointTransition(name, driver.EndpointUpdating, now,
		settle.DefaultEndpointUpdateSettle, current.VariantSummaries, "")

	return m.observeEndpoint(&updated, now), nil
}

// applyWeights validates and applies desired weights/capacities onto the
// variant summaries (Desired* and, as the end state, Current*).
func applyWeights(summaries []driver.VariantSummary, weights []driver.VariantWeight) error {
	index := make(map[string]int, len(summaries))
	for i := range summaries {
		index[summaries[i].VariantName] = i
	}

	for _, w := range weights {
		i, ok := index[w.VariantName]
		if !ok {
			return errors.Newf(errors.InvalidArgument, "variant %q not found on the endpoint", w.VariantName)
		}

		if w.DesiredWeight != nil {
			if *w.DesiredWeight < 0 {
				return errors.Newf(errors.InvalidArgument, "desiredWeight for variant %q must not be negative", w.VariantName)
			}

			summaries[i].DesiredWeight, summaries[i].CurrentWeight = *w.DesiredWeight, *w.DesiredWeight
		}

		if w.DesiredInstanceCount != nil {
			if *w.DesiredInstanceCount < 1 {
				return errors.Newf(errors.InvalidArgument, "desiredInstanceCount for variant %q must be at least 1", w.VariantName)
			}

			summaries[i].DesiredInstanceCount, summaries[i].CurrentInstanceCount = *w.DesiredInstanceCount, *w.DesiredInstanceCount
		}
	}

	return nil
}

// DeleteEndpoint deletes an endpoint: immediately, or after a Deleting window
// under --async-settle. Deleting an endpoint that is already Deleting is a
// no-op.
func (m *Mock) DeleteEndpoint(ctx context.Context, name string) error {
	ep, err := m.deleteEndpoint(name)
	if err != nil || ep == nil {
		return err
	}

	m.emitEndpointEvents(ctx, ep, driver.EndpointDeleting)

	return nil
}

// deleteEndpoint returns the endpoint as it was when deletion began, or nil
// when the delete was a no-op (already Deleting).
func (m *Mock) deleteEndpoint(name string) (*driver.Endpoint, error) {
	m.epMu.Lock()
	defer m.epMu.Unlock()

	now := m.opts.Clock.Now()

	ep, ok := m.liveEndpoint(name, now)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "endpoint %q not found", name)
	}

	if ep.Status == driver.EndpointDeleting {
		return nil, nil
	}

	dur := m.opts.SettleDuration(settle.DefaultEndpointDeleteSettle)
	if dur <= 0 {
		m.purgeEndpoint(name)

		return ep, nil
	}

	m.beginEndpointTransition(name, driver.EndpointDeleting, now, settle.DefaultEndpointDeleteSettle, nil, "")

	return ep, nil
}
