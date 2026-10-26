package codeartifact

import (
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// maxUpstreamSearch bounds how many upstream repositories are searched when a
// package version is looked up through a repository's upstream chain.
const maxUpstreamSearch = 25

// defaultRestrictionsLocked returns the origin controls a package gets when its
// first version is published directly. Real AWS bases the default on the
// package's group: with every restriction ALLOW (or no group) a directly
// published package is Publish ALLOW and Upstream BLOCK; otherwise both ALLOW.
// The caller holds mu.
func (m *Mock) defaultRestrictionsLocked(domain, format, namespace, name string) driver.OriginRestrictions {
	views := m.groupViewsLocked(domain)

	if idx, _, ok := associatedGroup(views, format, namespace, name); ok {
		parents := parentsOf(views)

		for _, t := range restrictionTypes() {
			if mode, _ := effective(views, parents, idx, t); mode != driver.ModeAllow {
				return driver.OriginRestrictions{Publish: driver.OriginAllow, Upstream: driver.OriginAllow}
			}
		}
	}

	return driver.OriginRestrictions{Publish: driver.OriginAllow, Upstream: driver.OriginBlock}
}

// publishBlockedLocked reports why publishing to repo is not allowed for a
// package, or "" when it is. Package-level BLOCK wins, then the package's group:
// a weak (variant) match is blocked, as are BLOCK and a repository missing from
// an ALLOW_SPECIFIC_REPOSITORIES list. The caller holds mu.
func (m *Mock) publishBlockedLocked(domain, repo, format, namespace, name string, p *packageRecord) string {
	if p != nil && p.Restrictions.Publish == driver.OriginBlock {
		return "the package's origin controls block publishing"
	}

	views := m.groupViewsLocked(domain)

	idx, strong, ok := associatedGroup(views, format, namespace, name)
	if !ok {
		return ""
	}

	if !strong {
		return "the package name is a variant of package group " + views[idx].rec.Pattern + ", so publishing is blocked"
	}

	mode, src := effective(views, parentsOf(views), idx, driver.RestrictionPublish)

	switch mode {
	case driver.ModeBlock:
		return "package group " + views[src].rec.Pattern + " blocks publishing"
	case driver.ModeAllowSpecificRepositories:
		if !containsString(views[src].rec.Allowed[driver.RestrictionPublish], repo) {
			return "package group " + views[src].rec.Pattern + " does not allow publishing to repository " + repo
		}
	}

	return ""
}

// findVersionLocked looks a package version up in a repository and, when
// includeUpstream is set, in the repositories upstream of it (breadth first, at
// most 25 searched, cycle-safe), or nil. The caller holds mu.
func (m *Mock) findVersionLocked(domain, repo, format, namespace, name, version string, includeUpstream bool) *versionRecord {
	visited := map[string]bool{}
	queue := []string{repo}

	for len(queue) > 0 && len(visited) < maxUpstreamSearch {
		cur := queue[0]
		queue = queue[1:]

		if visited[cur] {
			continue
		}

		visited[cur] = true

		if p, ok := m.packages.Get(packageKey(domain, cur, format, namespace, name)); ok {
			if found, ok := p.Versions[version]; ok {
				return found
			}
		}

		if !includeUpstream {
			break
		}

		if r, ok := m.repos.Get(repoKey(domain, cur)); ok {
			for _, u := range r.Upstreams {
				queue = append(queue, u.RepositoryName)
			}
		}
	}

	return nil
}
