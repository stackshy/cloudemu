package codeartifact

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// groupRecord is a stored package group. Modes holds the configured mode per
// restriction type (an absent type is INHERIT) and Allowed the sorted allowed
// repository list of each type that is ALLOW_SPECIFIC_REPOSITORIES. Records are
// read and written only under the Mock lock.
type groupRecord struct {
	Domain      string              `json:"domain"`
	Pattern     string              `json:"pattern"`
	Arn         string              `json:"arn"`
	Owner       string              `json:"owner"`
	CreatedTime time.Time           `json:"createdTime"`
	ContactInfo string              `json:"contactInfo,omitempty"`
	Description string              `json:"description,omitempty"`
	Tags        map[string]string   `json:"tags,omitempty"`
	Modes       map[string]string   `json:"modes,omitempty"`
	Allowed     map[string][]string `json:"allowed,omitempty"`
}

// groupKey is the store key of a package group.
func groupKey(domain, pattern string) string {
	return domain + "|" + pattern
}

// restrictionTypes lists the origin restriction types in a fixed order.
func restrictionTypes() []string {
	return []string{driver.RestrictionPublish, driver.RestrictionExternalUpstream, driver.RestrictionInternalUpstream}
}

func validRestrictionType(s string) bool {
	return s == driver.RestrictionPublish || s == driver.RestrictionExternalUpstream || s == driver.RestrictionInternalUpstream
}

func validRestrictionMode(s string) bool {
	switch s {
	case driver.ModeAllow, driver.ModeAllowSpecificRepositories, driver.ModeBlock, driver.ModeInherit:
		return true
	default:
		return false
	}
}

func (g *groupRecord) mode(restrictionType string) string {
	if m, ok := g.Modes[restrictionType]; ok {
		return m
	}

	return driver.ModeInherit
}

func (g *groupRecord) copy() *groupRecord {
	out := *g
	out.Tags = copyTags(g.Tags)
	out.Modes = copyTags(g.Modes)

	if g.Allowed != nil {
		out.Allowed = make(map[string][]string, len(g.Allowed))
		for k, v := range g.Allowed {
			out.Allowed[k] = append([]string(nil), v...)
		}
	}

	return &out
}

// groupView is a stored group with its parsed pattern.
type groupView struct {
	rec *groupRecord
	pat groupPattern
}

// groupViewsLocked returns the domain's groups sorted by pattern. The caller
// holds mu.
func (m *Mock) groupViewsLocked(domain string) []groupView {
	recs := m.groups.Filter(func(k string, _ *groupRecord) bool { return strings.HasPrefix(k, domain+"|") })
	views := make([]groupView, 0, len(recs))

	for _, rec := range recs {
		pat, err := parseGroupPattern(rec.Pattern)
		if err != nil {
			continue
		}

		views = append(views, groupView{rec: rec, pat: pat})
	}

	sort.Slice(views, func(i, j int) bool { return views[i].rec.Pattern < views[j].rec.Pattern })

	return views
}

// depths counts, for each group, how many other groups strictly contain it.
func depths(views []groupView) []int {
	out := make([]int, len(views))

	for i := range views {
		for j := range views {
			if i != j && strictlyCovers(views[j].pat, views[i].pat) {
				out[i]++
			}
		}
	}

	return out
}

func strictlyCovers(b, a groupPattern) bool {
	return b.covers(a) && !a.covers(b)
}

// parentsOf returns, for each group, the index of its parent (the most specific
// group strictly containing it) or -1.
func parentsOf(views []groupView) []int {
	depth := depths(views)
	parents := make([]int, len(views))

	for i := range views {
		parents[i] = -1

		for j := range views {
			if i == j || !strictlyCovers(views[j].pat, views[i].pat) {
				continue
			}

			if parents[i] < 0 || moreSpecific(views, depth, j, parents[i]) {
				parents[i] = j
			}
		}
	}

	return parents
}

// moreSpecific reports whether group a is deeper than group b, breaking ties by
// the longer pattern and then by pattern order so the choice is deterministic.
func moreSpecific(views []groupView, depth []int, a, b int) bool {
	if depth[a] != depth[b] {
		return depth[a] > depth[b]
	}

	pa, pb := views[a].rec.Pattern, views[b].rec.Pattern
	if len(pa) != len(pb) {
		return len(pa) > len(pb)
	}

	return pa < pb
}

// associatedGroup returns the index of the most specific group a package matches
// and whether the match is strong. ok is false when no group matches.
func associatedGroup(views []groupView, format, ns, name string) (idx int, strong, ok bool) {
	depth := depths(views)
	idx = -1

	for i := range views {
		if !views[i].pat.matches(format, ns, name, true) {
			continue
		}

		if idx < 0 || moreSpecific(views, depth, i, idx) {
			idx = i
		}
	}

	if idx < 0 {
		return -1, false, false
	}

	return idx, views[idx].pat.matches(format, ns, name, false), true
}

// effective resolves a group's mode for a restriction type through inheritance.
// src is the index of the group that decides it, or -1 when no group in the
// chain sets one (the mode is then ALLOW).
func effective(views []groupView, parents []int, idx int, restrictionType string) (mode string, src int) {
	for cur := idx; cur >= 0; cur = parents[cur] {
		if m := views[cur].rec.mode(restrictionType); m != driver.ModeInherit {
			return m, cur
		}
	}

	return driver.ModeAllow, -1
}

func groupRef(rec *groupRecord) *driver.GroupReference {
	return &driver.GroupReference{Arn: rec.Arn, Pattern: rec.Pattern}
}

// groupToDriver renders a stored group with its parent and effective modes.
func groupToDriver(views []groupView, parents []int, idx int) *driver.PackageGroup {
	rec := views[idx].rec
	out := &driver.PackageGroup{
		Arn:          rec.Arn,
		Pattern:      rec.Pattern,
		DomainName:   rec.Domain,
		DomainOwner:  rec.Owner,
		CreatedTime:  rec.CreatedTime,
		ContactInfo:  rec.ContactInfo,
		Description:  rec.Description,
		Restrictions: make(map[string]driver.GroupRestriction, len(restrictionTypes())),
	}

	if parents[idx] >= 0 {
		out.Parent = groupRef(views[parents[idx]].rec)
	}

	for _, t := range restrictionTypes() {
		mode, src := effective(views, parents, idx, t)
		r := driver.GroupRestriction{
			Mode:              rec.mode(t),
			EffectiveMode:     mode,
			RepositoriesCount: int64(len(rec.Allowed[t])),
		}

		if r.Mode == driver.ModeInherit && src >= 0 {
			r.InheritedFrom = groupRef(views[src].rec)
		}

		out.Restrictions[t] = r
	}

	return out
}

func indexOfPattern(views []groupView, pattern string) int {
	for i := range views {
		if views[i].rec.Pattern == pattern {
			return i
		}
	}

	return -1
}

// groupLocked resolves a group by pattern within a domain. The caller holds mu.
func (m *Mock) groupLocked(ref driver.DomainRef, pattern string) (views []groupView, idx int, err error) {
	if _, err = m.domainLocked(ref); err != nil {
		return nil, 0, err
	}

	if _, err = parseGroupPattern(pattern); err != nil {
		return nil, 0, err
	}

	views = m.groupViewsLocked(ref.Domain)

	idx = indexOfPattern(views, pattern)
	if idx < 0 {
		return nil, 0, notFound("", pattern,
			"Package group %s not found in domain %s", pattern, ref.Domain)
	}

	return views, idx, nil
}

// CreatePackageGroup creates a package group. The pattern must be valid and
// unused in the domain (patterns are case-sensitive).
func (m *Mock) CreatePackageGroup(_ context.Context, in *driver.CreatePackageGroupInput) (*driver.PackageGroup, error) {
	if _, err := parseGroupPattern(in.Pattern); err != nil {
		return nil, err
	}

	if err := validateGroupText(in.ContactInfo, in.Description); err != nil {
		return nil, err
	}

	if err := validateTags(in.Tags); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	d, err := m.domainLocked(in.Domain)
	if err != nil {
		return nil, err
	}

	key := groupKey(in.Domain.Domain, in.Pattern)
	if m.groups.Has(key) {
		return nil, conflict("", in.Pattern, "Package group %s already exists", in.Pattern)
	}

	m.groups.Set(key, &groupRecord{
		Domain:      d.Name,
		Pattern:     in.Pattern,
		Arn:         m.groupARN(d.Owner, d.Name, in.Pattern),
		Owner:       d.Owner,
		CreatedTime: m.now(),
		ContactInfo: in.ContactInfo,
		Description: in.Description,
		Tags:        copyTags(in.Tags),
	})

	views := m.groupViewsLocked(d.Name)

	return groupToDriver(views, parentsOf(views), indexOfPattern(views, in.Pattern)), nil
}

// validateGroupText checks the contact info and description of a package group.
func validateGroupText(contact, description string) error {
	if len(contact) > maxDescription || hasControl(contact) {
		return validation("contactInfo must be at most %d characters without control characters", maxDescription)
	}

	return validateDescription(description)
}

// DescribePackageGroup returns a package group with its parent and effective
// origin restrictions.
func (m *Mock) DescribePackageGroup(_ context.Context, ref driver.DomainRef, pattern string) (*driver.PackageGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	views, idx, err := m.groupLocked(ref, pattern)
	if err != nil {
		return nil, err
	}

	return groupToDriver(views, parentsOf(views), idx), nil
}

// UpdatePackageGroup changes the contact info and description of a group; the
// pattern and origin configuration are not editable here.
func (m *Mock) UpdatePackageGroup(_ context.Context, in *driver.UpdatePackageGroupInput) (*driver.PackageGroup, error) {
	contact, desc := "", ""
	if in.ContactInfo != nil {
		contact = *in.ContactInfo
	}

	if in.Description != nil {
		desc = *in.Description
	}

	if err := validateGroupText(contact, desc); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	views, idx, err := m.groupLocked(in.Domain, in.Pattern)
	if err != nil {
		return nil, err
	}

	if in.ContactInfo != nil {
		views[idx].rec.ContactInfo = *in.ContactInfo
	}

	if in.Description != nil {
		views[idx].rec.Description = *in.Description
	}

	return groupToDriver(views, parentsOf(views), idx), nil
}

// DeletePackageGroup deletes a group. Its direct children become children of its
// parent, which the computed hierarchy does by itself.
func (m *Mock) DeletePackageGroup(_ context.Context, ref driver.DomainRef, pattern string) (*driver.PackageGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	views, idx, err := m.groupLocked(ref, pattern)
	if err != nil {
		return nil, err
	}

	out := groupToDriver(views, parentsOf(views), idx)

	m.groups.Delete(groupKey(ref.Domain, pattern))

	return out, nil
}

// UpdatePackageGroupOriginConfiguration sets restriction modes and edits the
// allowed repository lists. A list exists only while its restriction is
// ALLOW_SPECIFIC_REPOSITORIES; switching away from that mode clears it.
func (m *Mock) UpdatePackageGroupOriginConfiguration(_ context.Context,
	in *driver.UpdateGroupOriginInput) (*driver.GroupOriginUpdate, error) {
	if err := validateOriginUpdate(in); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	views, idx, err := m.groupLocked(in.Domain, in.Pattern)
	if err != nil {
		return nil, err
	}

	// Work on a copy so a rejected request changes nothing.
	rec := views[idx].rec.copy()

	for t, mode := range in.Restrictions {
		if rec.Modes == nil {
			rec.Modes = map[string]string{}
		}

		rec.Modes[t] = mode

		if mode != driver.ModeAllowSpecificRepositories {
			delete(rec.Allowed, t)
		}
	}

	updates, err := m.applyAllowedEditsLocked(rec, in)
	if err != nil {
		return nil, err
	}

	m.groups.Set(groupKey(in.Domain.Domain, in.Pattern), rec)
	views[idx].rec = rec

	return &driver.GroupOriginUpdate{Group: groupToDriver(views, parentsOf(views), idx), Updates: updates}, nil
}

func validateOriginUpdate(in *driver.UpdateGroupOriginInput) error {
	for t, mode := range in.Restrictions {
		if !validRestrictionType(t) || !validRestrictionMode(mode) {
			return validation("restriction %s=%s is not valid", t, mode)
		}
	}

	for _, a := range append(append([]driver.AllowedRepository(nil), in.AddAllowed...), in.RemoveAllowed...) {
		if !validRestrictionType(a.OriginRestrictionType) {
			return validation("originRestrictionType %q must be PUBLISH, EXTERNAL_UPSTREAM or INTERNAL_UPSTREAM", a.OriginRestrictionType)
		}

		if err := validateRepositoryName(a.RepositoryName); err != nil {
			return err
		}
	}

	return nil
}

// applyAllowedEditsLocked applies the add/remove edits to rec and returns what
// changed, per restriction type: ADDED and REMOVED repository names. A list can
// only be edited while its restriction is ALLOW_SPECIFIC_REPOSITORIES, and added
// repositories must exist in the domain.
func (m *Mock) applyAllowedEditsLocked(rec *groupRecord, in *driver.UpdateGroupOriginInput) (map[string]map[string][]string, error) {
	updates := map[string]map[string][]string{}
	record := func(t, kind, repo string) {
		if updates[t] == nil {
			updates[t] = map[string][]string{}
		}

		updates[t][kind] = append(updates[t][kind], repo)
	}

	for _, a := range in.AddAllowed {
		if rec.mode(a.OriginRestrictionType) != driver.ModeAllowSpecificRepositories {
			return nil, validation("%s is not ALLOW_SPECIFIC_REPOSITORIES, so it has no allowed repository list", a.OriginRestrictionType)
		}

		if !m.repos.Has(repoKey(rec.Domain, a.RepositoryName)) {
			return nil, notFound(driver.ResourceTypeRepository, a.RepositoryName,
				"Repository %s not found in domain %s", a.RepositoryName, rec.Domain)
		}

		if !containsString(rec.Allowed[a.OriginRestrictionType], a.RepositoryName) {
			if rec.Allowed == nil {
				rec.Allowed = map[string][]string{}
			}

			rec.Allowed[a.OriginRestrictionType] = insertSorted(rec.Allowed[a.OriginRestrictionType], a.RepositoryName)
			record(a.OriginRestrictionType, driver.AllowedRepositoryAdded, a.RepositoryName)
		}
	}

	for _, a := range in.RemoveAllowed {
		cur := rec.Allowed[a.OriginRestrictionType]
		if !containsString(cur, a.RepositoryName) {
			continue
		}

		rec.Allowed[a.OriginRestrictionType] = removeString(cur, a.RepositoryName)
		record(a.OriginRestrictionType, driver.AllowedRepositoryRemoved, a.RepositoryName)
	}

	return updates, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}

	return false
}

func insertSorted(list []string, s string) []string {
	out := append(append([]string(nil), list...), s)
	sort.Strings(out)

	return out
}

func removeString(list []string, s string) []string {
	out := make([]string, 0, len(list))

	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}

	return out
}

// dropAllowedRepositoryLocked removes a deleted repository from every package
// group's allowed lists in the domain. The caller holds mu.
func (m *Mock) dropAllowedRepositoryLocked(domain, repo string) {
	for _, g := range m.groups.Filter(func(k string, _ *groupRecord) bool { return strings.HasPrefix(k, domain+"|") }) {
		for t, list := range g.Allowed {
			g.Allowed[t] = removeString(list, repo)
		}
	}
}

// ListPackageGroups returns a deterministic page of the domain's groups whose
// pattern starts with prefix.
func (m *Mock) ListPackageGroups(_ context.Context, ref driver.DomainRef, prefix string,
	page driver.Page) ([]*driver.PackageGroup, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, err := m.domainLocked(ref); err != nil {
		return nil, "", err
	}

	views := m.groupViewsLocked(ref.Domain)
	parents := parentsOf(views)

	var idxs []int

	for i := range views {
		if strings.HasPrefix(views[i].rec.Pattern, prefix) {
			idxs = append(idxs, i)
		}
	}

	return renderGroupPage(views, parents, idxs, page)
}

// ListSubPackageGroups returns a deterministic page of a group's direct children.
func (m *Mock) ListSubPackageGroups(_ context.Context, ref driver.DomainRef, pattern string,
	page driver.Page) ([]*driver.PackageGroup, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	views, idx, err := m.groupLocked(ref, pattern)
	if err != nil {
		return nil, "", err
	}

	parents := parentsOf(views)

	var idxs []int

	for i := range views {
		if parents[i] == idx {
			idxs = append(idxs, i)
		}
	}

	return renderGroupPage(views, parents, idxs, page)
}

func renderGroupPage(views []groupView, parents, idxs []int, page driver.Page) ([]*driver.PackageGroup, string, error) {
	window, next, err := pageOf(idxs, page)
	if err != nil {
		return nil, "", err
	}

	out := make([]*driver.PackageGroup, 0, len(window))
	for _, i := range window {
		out = append(out, groupToDriver(views, parents, i))
	}

	return out, next, nil
}

// ListAllowedRepositoriesForGroup returns a page of the repositories in the
// allowed list of one restriction type.
func (m *Mock) ListAllowedRepositoriesForGroup(_ context.Context, ref driver.DomainRef, pattern, restrictionType string,
	page driver.Page) (repos []string, next string, err error) {
	if !validRestrictionType(restrictionType) {
		return nil, "", validation("originRestrictionType %q must be PUBLISH, EXTERNAL_UPSTREAM or INTERNAL_UPSTREAM", restrictionType)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	views, idx, err := m.groupLocked(ref, pattern)
	if err != nil {
		return nil, "", err
	}

	return pageOf(append([]string(nil), views[idx].rec.Allowed[restrictionType]...), page)
}

// packageCoordinates lists the distinct packages of a domain, sorted. The caller
// holds mu.
func (m *Mock) packageCoordinatesLocked(domain string) []driver.AssociatedPackage {
	pkgs := m.packages.Filter(func(k string, _ *packageRecord) bool { return strings.HasPrefix(k, domain+"/") })
	seen := make(map[string]bool, len(pkgs))
	out := make([]driver.AssociatedPackage, 0, len(pkgs))

	for _, p := range pkgs {
		id := p.Format + "/" + p.Namespace + "/" + p.Name
		if seen[id] {
			continue
		}

		seen[id] = true

		out = append(out, driver.AssociatedPackage{Format: p.Format, Namespace: p.Namespace, Package: p.Name})
	}

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Format != b.Format {
			return a.Format < b.Format
		}

		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}

		return a.Package < b.Package
	})

	return out
}

// ListAssociatedPackages returns a page of the packages whose most specific
// group is the given one. With preview, a group that does not exist yet is
// evaluated as if it did.
func (m *Mock) ListAssociatedPackages(_ context.Context, ref driver.DomainRef, pattern string, preview bool,
	page driver.Page) ([]driver.AssociatedPackage, string, error) {
	pat, err := parseGroupPattern(pattern)
	if err != nil {
		return nil, "", err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, err = m.domainLocked(ref); err != nil {
		return nil, "", err
	}

	views := m.groupViewsLocked(ref.Domain)

	idx := indexOfPattern(views, pattern)
	if idx < 0 {
		if !preview {
			return nil, "", notFound("", pattern,
				"Package group %s not found in domain %s", pattern, ref.Domain)
		}

		views = append(views, groupView{rec: &groupRecord{Domain: ref.Domain, Pattern: pattern}, pat: pat})
		idx = len(views) - 1
	}

	coords := m.packageCoordinatesLocked(ref.Domain)
	out := make([]driver.AssociatedPackage, 0, len(coords))

	for _, p := range coords {
		got, strong, ok := associatedGroup(views, p.Format, p.Namespace, p.Package)
		if !ok || got != idx {
			continue
		}

		p.AssociationType = associationType(strong)
		out = append(out, p)
	}

	return pageOf(out, page)
}

func associationType(strong bool) string {
	if strong {
		return driver.AssociationStrong
	}

	return driver.AssociationWeak
}

// GetAssociatedPackageGroup returns the group a package coordinate belongs to and
// how strongly. The package need not exist in any repository.
func (m *Mock) GetAssociatedPackageGroup(_ context.Context, ref driver.DomainRef,
	format, namespace, pkg string) (*driver.PackageGroup, string, error) {
	if !validFormat(format) {
		return nil, "", validation("package format %q is not one of npm, pypi, maven, nuget, generic, ruby, swift, cargo", format)
	}

	if (namespace != "" && !validComponent(namespace)) || !validComponent(pkg) {
		return nil, "", validation("package coordinates are invalid")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, err := m.domainLocked(ref); err != nil {
		return nil, "", err
	}

	views := m.groupViewsLocked(ref.Domain)

	idx, strong, ok := associatedGroup(views, format, namespace, pkg)
	if !ok {
		return nil, "", notFound("", pkg, "No package group is associated with package %s", pkg)
	}

	return groupToDriver(views, parentsOf(views), idx), associationType(strong), nil
}
