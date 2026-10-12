package sagemaker

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

const (
	maxLineageNameLen     = 120
	maxLineageTypeLen     = 256
	maxLineageDescLen     = 3072
	maxLineagePropKeyLen  = 2500
	maxArtifactPropValLen = 4096
	maxOtherPropValLen    = 2500
	maxSourceURILen       = 2048
)

var (
	lineageNamePattern = regexp.MustCompile(`^[a-zA-Z0-9](-*[a-zA-Z0-9]){0,119}$`)
	contextNamePattern = regexp.MustCompile(`^[a-zA-Z0-9]([-_]*[a-zA-Z0-9]){0,119}$`)
)

var (
	sourceIDTypes = map[string]bool{"MD5Hash": true, "S3ETag": true, "S3Version": true, "Custom": true}                                                                                                                             //nolint:gochecknoglobals,lll // static table
	actionStatus  = map[string]bool{"": true, "Unknown": true, "InProgress": true, "Completed": true, "Failed": true, "Stopping": true, "Stopped": true}                                                                            //nolint:gochecknoglobals,lll // static table
	assocTypes    = map[string]bool{"": true, driver.AssociationContributedTo: true, driver.AssociationAssociatedWith: true, driver.AssociationDerivedFrom: true, driver.AssociationProduced: true, driver.AssociationSameAs: true} //nolint:gochecknoglobals,lll // static table
)

func (m *Mock) entityARN(kind, name string) string {
	return m.arn(strings.ToLower(kind) + "/" + name)
}

func cloneEntity(in *driver.LineageEntity) *driver.LineageEntity {
	out := *in
	out.Properties = copyStrMap(in.Properties)
	out.Source.SourceTypes = append([]driver.LineageSourceType(nil), in.Source.SourceTypes...)

	if in.Metadata != nil {
		md := *in.Metadata
		out.Metadata = &md
	}

	return &out
}

// ensureLineageGroup creates the account's default lineage group the first time
// any lineage entity is created. The caller holds entityMu.
func (m *Mock) ensureLineageGroup() {
	m.lineageGroup.SetIfAbsent("default", m.now())
}

//nolint:gocyclo // validation mirrors the service rules
func validateEntitySpec(kind string, spec *driver.LineageEntitySpec) error {
	pattern := lineageNamePattern
	if kind == driver.LineageContext {
		pattern = contextNamePattern
	}

	switch {
	case spec.Name == "" && kind != driver.LineageArtifact:
		return errors.Newf(errors.InvalidArgument, "%sName is required", strings.ToLower(kind))
	case spec.Name != "" && (len(spec.Name) > maxLineageNameLen || !pattern.MatchString(spec.Name)):
		return errors.Newf(errors.InvalidArgument, "%sName %q must be 1-%d valid characters", strings.ToLower(kind), spec.Name, maxLineageNameLen)
	case len(spec.Type) > maxLineageTypeLen:
		return errors.Newf(errors.InvalidArgument, "%sType must be at most %d characters", strings.ToLower(kind), maxLineageTypeLen)
	case spec.Source.SourceURI == "" || len(spec.Source.SourceURI) > maxSourceURILen:
		return errors.Newf(errors.InvalidArgument, "source.sourceUri is required and at most %d characters", maxSourceURILen)
	case len(spec.Description) > maxLineageDescLen:
		return errors.Newf(errors.InvalidArgument, "description must be at most %d characters", maxLineageDescLen)
	case !actionStatus[spec.Status]:
		return errors.Newf(errors.InvalidArgument, "status %q is not a valid action status", spec.Status)
	}

	for _, st := range spec.Source.SourceTypes {
		if !sourceIDTypes[st.IDType] {
			return errors.Newf(errors.InvalidArgument, "sourceTypes[].sourceIdType %q must be MD5Hash, S3ETag, S3Version or Custom", st.IDType)
		}
	}

	if err := validateLineageProps(kind, spec.Properties); err != nil {
		return err
	}

	return validateTags(spec.Tags)
}

func validateLineageProps(kind string, props map[string]string) error {
	if len(props) > driver.MaxLineageProperties {
		return errors.Newf(errors.InvalidArgument, "properties must have at most %d entries", driver.MaxLineageProperties)
	}

	maxVal := maxOtherPropValLen
	if kind == driver.LineageArtifact {
		maxVal = maxArtifactPropValLen
	}

	for k, v := range props {
		if len(k) > maxLineagePropKeyLen || len(v) > maxVal {
			return errors.Newf(errors.InvalidArgument, "property %q exceeds the key/value length limits", k)
		}
	}

	return nil
}

// createEntity creates an artifact, action or context under entityMu.
func (m *Mock) createEntity(kind string, spec *driver.LineageEntitySpec) (*driver.LineageEntity, error) {
	if err := validateEntitySpec(kind, spec); err != nil {
		return nil, err
	}

	m.entityMu.Lock()
	defer m.entityMu.Unlock()

	var arn string

	if kind == driver.LineageArtifact { //nolint:nestif // validation
		id, err := idgen.GenerateLongID("")
		if err != nil {
			return nil, errors.Newf(errors.Internal, "could not mint an artifact id: %v", err)
		}

		arn = m.entityARN(kind, id)

		// Artifact names (when set) and source URIs are unique per account.
		for _, e := range m.lineageEntities.All() {
			if e.Kind != driver.LineageArtifact {
				continue
			}

			if spec.Name != "" && e.Name == spec.Name {
				return nil, errors.Newf(errors.AlreadyExists, "artifact %q already exists: %s", spec.Name, e.ARN)
			}

			if e.Source.SourceURI == spec.Source.SourceURI {
				return nil, errors.Newf(errors.AlreadyExists, "an artifact with source URI %q already exists: %s", spec.Source.SourceURI, e.ARN)
			}
		}
	} else {
		arn = m.entityARN(kind, spec.Name)
	}

	now := m.now()
	e := &driver.LineageEntity{
		Kind: kind, ARN: arn, Name: spec.Name, Type: spec.Type, Source: spec.Source, Description: spec.Description,
		Status: spec.Status, Properties: spec.Properties, Metadata: spec.Metadata, CreationTime: now, LastModifiedTime: now,
	}
	e = cloneEntity(e)

	if !m.lineageEntities.SetIfAbsent(arn, e) {
		return nil, errors.Newf(errors.AlreadyExists, "%s %q already exists", strings.ToLower(kind), spec.Name)
	}

	m.ensureLineageGroup()
	m.setTags(arn, spec.Tags)

	return cloneEntity(e), nil
}

// describeEntity returns the entity of the given kind at arn.
func (m *Mock) describeEntity(kind, arn string) (*driver.LineageEntity, error) {
	e, ok := m.lineageEntities.Get(arn)
	if !ok || e.Kind != kind {
		return nil, errors.Newf(errors.NotFound, "%s %q not found", strings.ToLower(kind), arn)
	}

	return cloneEntity(e), nil
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (m *Mock) listEntities(kind string, opts driver.ListOptions) (driver.ListPage[driver.LineageEntity], error) {
	typeKey := kind + "Type"

	return listFrom(m.lineageEntities, listSpec[driver.LineageEntity]{
		name:         func(t *driver.LineageEntity) string { return t.Name },
		created:      func(t *driver.LineageEntity) string { return t.CreationTime },
		defaultOrder: driver.SortDescending,
		extra: func(t *driver.LineageEntity, f map[string]string) bool {
			if t.Kind != kind {
				return false
			}

			if v := f[typeKey]; v != "" && t.Type != v {
				return false
			}

			return f["SourceUri"] == "" || t.Source.SourceURI == f["SourceUri"]
		},
		clone: func(t *driver.LineageEntity) driver.LineageEntity { return *cloneEntity(t) },
	}, opts)
}

// updateEntity updates an entity by key (ARN for artifacts, name otherwise).
//
//nolint:gocyclo // validation mirrors the service rules
func (m *Mock) updateEntity(kind string, up *driver.LineageEntityUpdate) (*driver.LineageEntity, error) {
	if up.Description != nil && len(*up.Description) > maxLineageDescLen {
		return nil, errors.Newf(errors.InvalidArgument, "description must be at most %d characters", maxLineageDescLen)
	}

	if up.Status != nil && !actionStatus[*up.Status] {
		return nil, errors.Newf(errors.InvalidArgument, "status %q is not a valid action status", *up.Status)
	}

	if err := validateLineageProps(kind, up.Properties); err != nil {
		return nil, err
	}

	m.entityMu.Lock()
	defer m.entityMu.Unlock()

	arn := up.Key
	if kind != driver.LineageArtifact {
		arn = m.entityARN(kind, up.Key)
	}

	cur, err := m.describeEntity(kind, arn)
	if err != nil {
		return nil, err
	}

	if up.Name != nil && *up.Name != cur.Name {
		if *up.Name != "" && (len(*up.Name) > maxLineageNameLen || !lineageNamePattern.MatchString(*up.Name)) {
			return nil, errors.Newf(errors.InvalidArgument, "artifactName %q must be 1-%d valid characters", *up.Name, maxLineageNameLen)
		}

		for _, e := range m.lineageEntities.All() {
			if e.Kind == kind && e.ARN != arn && *up.Name != "" && e.Name == *up.Name {
				return nil, errors.Newf(errors.AlreadyExists, "artifact %q already exists", *up.Name)
			}
		}

		cur.Name = *up.Name
	}

	if up.Description != nil {
		cur.Description = *up.Description
	}

	if up.Status != nil {
		cur.Status = *up.Status
	}

	if up.Properties != nil {
		cur.Properties = copyStrMap(up.Properties)
	}

	for _, k := range up.PropertiesToRemove {
		delete(cur.Properties, k)
	}

	cur.LastModifiedTime = m.now()
	m.lineageEntities.Set(arn, cur)

	return cloneEntity(cur), nil
}

// deleteEntity deletes an entity that has no associations left.
func (m *Mock) deleteEntity(kind, arn string) (string, error) {
	m.entityMu.Lock()
	defer m.entityMu.Unlock()

	if _, err := m.describeEntity(kind, arn); err != nil {
		return "", err
	}

	for _, a := range m.associations.All() {
		if a.SourceARN == arn || a.DestinationARN == arn {
			return "", errors.Newf(errors.FailedPrecondition, "%s %q still has associations; delete them first", strings.ToLower(kind), arn)
		}
	}

	m.lineageEntities.Delete(arn)
	m.dropTags(arn)

	return arn, nil
}

// --- Artifact ---

//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (m *Mock) CreateArtifact(_ context.Context, spec driver.LineageEntitySpec) (*driver.LineageEntity, error) {
	return m.createEntity(driver.LineageArtifact, &spec)
}

func (m *Mock) DescribeArtifact(_ context.Context, arn string) (*driver.LineageEntity, error) {
	if err := validateARN("artifactArn", arn); err != nil {
		return nil, err
	}

	return m.describeEntity(driver.LineageArtifact, arn)
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (m *Mock) ListArtifacts(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.LineageEntity], error) {
	return m.listEntities(driver.LineageArtifact, opts)
}

func (m *Mock) UpdateArtifact(_ context.Context, up driver.LineageEntityUpdate) (*driver.LineageEntity, error) {
	return m.updateEntity(driver.LineageArtifact, &up)
}

// DeleteArtifact deletes an artifact addressed by ARN or by its source URI.
func (m *Mock) DeleteArtifact(_ context.Context, arn, sourceURI string) (string, error) {
	if arn == "" && sourceURI == "" {
		return "", errors.New(errors.InvalidArgument, "specify either artifactArn or source")
	}

	if arn != "" {
		if err := validateARN("artifactArn", arn); err != nil {
			return "", err
		}
	}

	if arn == "" {
		m.entityMu.Lock()

		for _, e := range m.lineageEntities.All() {
			if e.Kind == driver.LineageArtifact && e.Source.SourceURI == sourceURI {
				arn = e.ARN
			}
		}

		m.entityMu.Unlock()

		if arn == "" {
			return "", errors.Newf(errors.NotFound, "no artifact with source URI %q", sourceURI)
		}
	}

	return m.deleteEntity(driver.LineageArtifact, arn)
}

// --- Action ---

//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (m *Mock) CreateAction(_ context.Context, spec driver.LineageEntitySpec) (*driver.LineageEntity, error) {
	return m.createEntity(driver.LineageAction, &spec)
}

func (m *Mock) DescribeAction(_ context.Context, name string) (*driver.LineageEntity, error) {
	return m.describeEntity(driver.LineageAction, m.entityARN(driver.LineageAction, name))
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (m *Mock) ListActions(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.LineageEntity], error) {
	return m.listEntities(driver.LineageAction, opts)
}

func (m *Mock) UpdateAction(_ context.Context, up driver.LineageEntityUpdate) (*driver.LineageEntity, error) {
	return m.updateEntity(driver.LineageAction, &up)
}

func (m *Mock) DeleteAction(_ context.Context, name string) (string, error) {
	return m.deleteEntity(driver.LineageAction, m.entityARN(driver.LineageAction, name))
}

// --- Context ---

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (m *Mock) CreateContext(_ context.Context, spec driver.LineageEntitySpec) (*driver.LineageEntity, error) {
	return m.createEntity(driver.LineageContext, &spec)
}

func (m *Mock) DescribeContext(_ context.Context, name string) (*driver.LineageEntity, error) {
	return m.describeEntity(driver.LineageContext, m.entityARN(driver.LineageContext, name))
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (m *Mock) ListContexts(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.LineageEntity], error) {
	return m.listEntities(driver.LineageContext, opts)
}

func (m *Mock) UpdateContext(_ context.Context, up driver.LineageEntityUpdate) (*driver.LineageEntity, error) {
	return m.updateEntity(driver.LineageContext, &up)
}

func (m *Mock) DeleteContext(_ context.Context, name string) (string, error) {
	return m.deleteEntity(driver.LineageContext, m.entityARN(driver.LineageContext, name))
}

// --- Associations ---

// endpointKinds are the ARN resource types an association may connect.
var endpointKinds = map[string]bool{ //nolint:gochecknoglobals // static table
	"experiment": true, "experiment-trial-component": true, "artifact": true, "action": true, "context": true,
}

func assocKey(src, dst string) string { return src + "\x00" + dst }

// endpoint resolves an association endpoint ARN to its resource type and
// display name/type, or an error when it is malformed or does not exist. The
// caller holds entityMu.
func (m *Mock) endpoint(arn string) (kind, name, typ string, err error) {
	prefix := "arn:aws:sagemaker:" + m.opts.Region + ":" + m.opts.AccountID + ":"
	if !strings.HasPrefix(arn, prefix) {
		return "", "", "", errors.Newf(errors.InvalidArgument, "%q is not a valid SageMaker ARN for this account and region", arn)
	}

	kind, rest, ok := strings.Cut(strings.TrimPrefix(arn, prefix), "/")
	if !ok || !endpointKinds[kind] {
		return "", "", "", errors.Newf(errors.InvalidArgument,
			"%q must be an experiment, trial component, artifact, action or context ARN", arn)
	}

	switch kind {
	case "experiment":
		if m.experiments.Has(rest) {
			return kind, rest, "Experiment", nil
		}
	case "experiment-trial-component": //nolint:goconst // SDK enum literals
		if tc, found := m.trialComponents.Get(tcKey(rest)); found {
			return kind, tc.Name, "TrialComponent", nil //nolint:goconst // SDK enum literals
		}
	default:
		if e, found := m.lineageEntities.Get(arn); found {
			return kind, e.Name, e.Type, nil
		}
	}

	return "", "", "", errors.Newf(errors.NotFound, "%s %q not found", kind, arn)
}

// AddAssociation connects two entities. Re-adding an existing edge succeeds
// and keeps the original.
func (m *Mock) AddAssociation(_ context.Context, sourceARN, destinationARN, associationType string) error {
	if !assocTypes[associationType] {
		return errors.Newf(errors.InvalidArgument, "associationType %q must be ContributedTo, AssociatedWith, DerivedFrom, Produced or SameAs", associationType) //nolint:lll // SDK-shaped literal
	}

	for field, a := range map[string]string{"sourceArn": sourceARN, "destinationArn": destinationARN} {
		if err := validateARN(field, a); err != nil {
			return err
		}
	}

	m.entityMu.Lock()
	defer m.entityMu.Unlock()

	for _, a := range []string{sourceARN, destinationARN} {
		if _, _, _, err := m.endpoint(a); err != nil {
			return err
		}
	}

	m.associations.SetIfAbsent(assocKey(sourceARN, destinationARN), &driver.Association{
		SourceARN: sourceARN, DestinationARN: destinationARN, Type: associationType, CreationTime: m.now(),
	})
	m.ensureLineageGroup()

	return nil
}

func (m *Mock) DeleteAssociation(_ context.Context, sourceARN, destinationARN string) error {
	m.entityMu.Lock()
	defer m.entityMu.Unlock()

	if !m.associations.Delete(assocKey(sourceARN, destinationARN)) {
		return errors.Newf(errors.NotFound, "no association from %q to %q", sourceARN, destinationARN)
	}

	return nil
}

// removeAssociationsOf drops every association touching arn (used when a trial
// component or experiment is deleted, so no dangling edge remains). The caller
// holds entityMu.
func (m *Mock) removeAssociationsOf(arn string) {
	for key, a := range m.associations.All() {
		if a.SourceARN == arn || a.DestinationARN == arn {
			m.associations.Delete(key)
		}
	}
}

// ListAssociations lists associations with their endpoints resolved.
//
//nolint:gocritic // SDK-shaped; see nongoals/standards
func (m *Mock) ListAssociations(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.AssociationSummary], error) {
	m.entityMu.Lock()
	items := make([]*driver.AssociationSummary, 0, m.associations.Len())

	for _, a := range m.associations.SortedValues() {
		s := driver.AssociationSummary{Association: *a}
		_, s.SourceName, s.SourceType, _ = m.endpoint(a.SourceARN)
		_, s.DestinationName, s.DestinationType, _ = m.endpoint(a.DestinationARN)
		items = append(items, &s)
	}
	m.entityMu.Unlock()

	return listItems(items, listSpec[driver.AssociationSummary]{
		name:         func(t *driver.AssociationSummary) string { return t.SourceARN + "\x00" + t.DestinationARN },
		created:      func(t *driver.AssociationSummary) string { return t.CreationTime },
		defaultOrder: driver.SortDescending,
		keys: map[string]func(*driver.AssociationSummary) string{
			"SourceArn":       func(t *driver.AssociationSummary) string { return t.SourceARN },
			"DestinationArn":  func(t *driver.AssociationSummary) string { return t.DestinationARN },
			"SourceType":      func(t *driver.AssociationSummary) string { return t.SourceType },
			"DestinationType": func(t *driver.AssociationSummary) string { return t.DestinationType },
		},
		extra: func(t *driver.AssociationSummary, f map[string]string) bool {
			for key, got := range map[string]string{
				"AssociationType": t.Type, "SourceArn": t.SourceARN, "DestinationArn": t.DestinationARN,
				"SourceType": t.SourceType, "DestinationType": t.DestinationType,
			} {
				if want := f[key]; want != "" && want != got {
					return false
				}
			}

			return true
		},
	}, opts)
}

// --- QueryLineage ---

// lineageNode is a vertex of the lineage graph with the attributes filters use.
type lineageNode struct {
	vertex   driver.LineageVertex
	created  string
	modified string
	props    map[string]string
}

// QueryLineage walks the association graph from one start entity.
//
//nolint:gocritic,gocyclo // validation mirrors the service rules
func (m *Mock) QueryLineage(_ context.Context, q driver.LineageQuery) (*driver.LineageResult, error) {
	if len(q.StartARNs) != 1 {
		return nil, errors.New(errors.InvalidArgument, "startArns must contain exactly 1 item")
	}

	if err := validateARN("startArns", q.StartARNs[0]); err != nil {
		return nil, err
	}

	if q.MaxDepth < 0 || q.MaxDepth > driver.MaxLineageQueryDepth {
		return nil, errors.Newf(errors.InvalidArgument, "maxDepth must be between 1 and %d", driver.MaxLineageQueryDepth)
	}

	if q.MaxResults < 0 || q.MaxResults > driver.MaxLineageQueryResults {
		return nil, errors.Newf(errors.InvalidArgument, "maxResults must be between 1 and %d", driver.MaxLineageQueryResults)
	}

	dir := orDefault(q.Direction, driver.LineageBoth)
	if dir != driver.LineageBoth && dir != driver.LineageAscendants && dir != driver.LineageDescendants {
		return nil, errors.Newf(errors.InvalidArgument, "direction %q must be Both, Ascendants or Descendants", q.Direction)
	}

	m.entityMu.Lock()
	start := q.StartARNs[0]

	if _, _, _, err := m.endpoint(start); err != nil {
		m.entityMu.Unlock()

		return nil, err
	}

	assocs := m.associations.SortedValues()
	nodes := map[string]lineageNode{}

	for _, a := range assocs {
		for _, arn := range []string{a.SourceARN, a.DestinationARN} {
			if _, seen := nodes[arn]; !seen {
				nodes[arn] = m.nodeOf(arn)
			}
		}
	}
	m.entityMu.Unlock()

	order, edges := traverse(assocs, start, dir, orDefaultInt(q.MaxDepth, driver.MaxLineageQueryDepth))

	return pageLineage(q, order, edges, nodes)
}

func orDefaultInt(v, def int) int {
	if v == 0 {
		return def
	}

	return v
}

// nodeOf describes the lineage vertex of arn. The caller holds entityMu.
func (m *Mock) nodeOf(arn string) lineageNode {
	kind, _, typ, err := m.endpoint(arn)
	if err != nil {
		return lineageNode{vertex: driver.LineageVertex{ARN: arn}}
	}

	n := lineageNode{vertex: driver.LineageVertex{ARN: arn, Type: typ}}

	switch kind {
	case "experiment-trial-component":
		if tc, ok := m.trialComponents.Get(tcKey(strings.TrimPrefix(arn, m.arn("experiment-trial-component/")))); ok {
			n.vertex.LineageType = "TrialComponent"
			n.created, n.modified = tc.CreationTime, tc.LastModifiedTime
		}
	case "artifact", "action", "context":
		if e, ok := m.lineageEntities.Get(arn); ok {
			n.vertex.LineageType = e.Kind
			n.created, n.modified, n.props = e.CreationTime, e.LastModifiedTime, copyStrMap(e.Properties)
		}
	}

	return n
}

// traverse does a breadth-first walk from start up to maxDepth associations
// away. Ascendants follow edges toward their sources, Descendants toward their
// destinations. It returns the visited ARNs in deterministic order (excluding
// start) and the traversed edges.
func traverse(assocs []*driver.Association, start, dir string, maxDepth int) (order []string, edges []driver.LineageEdge) {
	visited := map[string]bool{start: true}
	frontier := []string{start}

	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var next []string

		for _, cur := range frontier {
			for _, a := range assocs {
				var other string

				switch {
				case a.SourceARN == cur && dir != driver.LineageAscendants:
					other = a.DestinationARN
				case a.DestinationARN == cur && dir != driver.LineageDescendants:
					other = a.SourceARN
				default:
					continue
				}

				edges = append(edges, driver.LineageEdge{SourceARN: a.SourceARN, DestinationARN: a.DestinationARN, AssociationType: a.Type})

				if !visited[other] {
					visited[other] = true

					next = append(next, other)
				}
			}
		}

		sort.Strings(next)
		order = append(order, next...)
		frontier = next
	}

	return order, dedupeEdges(edges)
}

func dedupeEdges(in []driver.LineageEdge) []driver.LineageEdge {
	seen := map[string]bool{}
	out := make([]driver.LineageEdge, 0, len(in))

	for _, e := range in {
		k := assocKey(e.SourceARN, e.DestinationARN)
		if !seen[k] {
			seen[k] = true

			out = append(out, e)
		}
	}

	return out
}

// pageLineage applies the query filters and pages the vertices.
//
//nolint:gocritic,lll // driver-signature params and SDK-shaped helpers
func pageLineage(q driver.LineageQuery, order []string, edges []driver.LineageEdge, nodes map[string]lineageNode) (*driver.LineageResult, error) {
	matched := make([]driver.LineageVertex, 0, len(order))
	keep := map[string]bool{}

	for _, arn := range order {
		n := nodes[arn]
		if lineageMatches(&q, &n) {
			matched = append(matched, n.vertex)
			keep[arn] = true
		}
	}

	size := orDefaultInt(q.MaxResults, driver.MaxLineageQueryResults)

	page, err := pagination.Paginate(matched, q.NextToken, size)
	if err != nil {
		return nil, errors.New(errors.InvalidArgument, "Invalid value for NextToken")
	}

	res := &driver.LineageResult{Vertices: append([]driver.LineageVertex(nil), page.Items...), NextToken: page.NextPageToken}
	if res.Vertices == nil {
		res.Vertices = []driver.LineageVertex{}
	}

	if q.IncludeEdges {
		shown := map[string]bool{q.StartARNs[0]: true}
		for _, v := range res.Vertices {
			shown[v.ARN] = true
		}

		for _, e := range edges {
			if shown[e.SourceARN] && shown[e.DestinationARN] {
				res.Edges = append(res.Edges, e)
			}
		}
	}

	return res, nil
}

func lineageMatches(q *driver.LineageQuery, n *lineageNode) bool {
	if len(q.LineageTypes) > 0 && !contains(q.LineageTypes, n.vertex.LineageType) {
		return false
	}

	if len(q.Types) > 0 && !contains(q.Types, n.vertex.Type) {
		return false
	}

	for k, v := range q.Properties {
		if n.props[k] != v {
			return false
		}
	}

	return inTimeWindow(n.created, q.CreatedAfter, q.CreatedBefore) && inTimeWindow(n.modified, q.ModifiedAfter, q.ModifiedBefore)
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}

	return false
}

// inTimeWindow reports whether ts is after `after` and before `before`
// (either bound may be empty).
func inTimeWindow(ts, after, before string) bool {
	t := parseTime(ts)

	if after != "" && !t.After(parseTime(after)) {
		return false
	}

	return before == "" || t.Before(parseTime(before))
}

// --- Lineage group ---

// lineageGroupOf returns the default lineage group once any lineage entity has
// been created, accepting the group's name or ARN.
func (m *Mock) lineageGroupOf(nameOrARN string) (*driver.LineageGroup, error) {
	created, ok := m.lineageGroup.Get("default")
	if !ok {
		return nil, errors.Newf(errors.NotFound, "lineage group %q not found", nameOrARN)
	}

	arn := m.arn("lineage-group/" + driver.DefaultLineageGroupName)
	if nameOrARN != driver.DefaultLineageGroupName && nameOrARN != arn {
		return nil, errors.Newf(errors.NotFound, "lineage group %q not found", nameOrARN)
	}

	return &driver.LineageGroup{
		Name: driver.DefaultLineageGroupName, ARN: arn, DisplayName: driver.DefaultLineageGroupName,
		CreationTime: created, LastModifiedTime: created,
	}, nil
}

func (m *Mock) DescribeLineageGroup(_ context.Context, nameOrARN string) (*driver.LineageGroup, error) {
	return m.lineageGroupOf(nameOrARN)
}

// ListLineageGroups lists the account's single, auto-created lineage group.
//
//nolint:gocritic // SDK-shaped; see nongoals/standards
func (m *Mock) ListLineageGroups(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.LineageGroup], error) {
	items := make([]*driver.LineageGroup, 0, 1)

	if g, err := m.lineageGroupOf(driver.DefaultLineageGroupName); err == nil {
		items = append(items, g)
	}

	return listItems(items, listSpec[driver.LineageGroup]{
		name:    func(t *driver.LineageGroup) string { return t.Name },
		created: func(t *driver.LineageGroup) string { return t.CreationTime },
	}, opts)
}

// GetLineageGroupPolicy returns the group ARN; a resource policy can only be
// attached through AWS RAM, which the emulator does not model, so none is set.
//
//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (m *Mock) GetLineageGroupPolicy(_ context.Context, nameOrARN string) (string, string, error) {
	g, err := m.lineageGroupOf(nameOrARN)
	if err != nil {
		return "", "", err
	}

	return g.ARN, "", nil
}
