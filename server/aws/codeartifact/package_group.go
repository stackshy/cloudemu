package codeartifact

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

func groupRefToWire(g *driver.GroupReference) map[string]any {
	return map[string]any{"arn": g.Arn, "pattern": g.Pattern}
}

// groupToWire renders a package group as PackageGroupDescription (and, member for
// member, PackageGroupSummary).
func groupToWire(g *driver.PackageGroup) map[string]any {
	restrictions := make(map[string]any, len(g.Restrictions))

	for t, r := range g.Restrictions {
		entry := map[string]any{"mode": r.Mode, "effectiveMode": r.EffectiveMode, "repositoriesCount": r.RepositoriesCount}
		if r.InheritedFrom != nil {
			entry["inheritedFrom"] = groupRefToWire(r.InheritedFrom)
		}

		restrictions[t] = entry
	}

	out := map[string]any{
		"arn": g.Arn, "pattern": g.Pattern, "domainName": g.DomainName, "domainOwner": g.DomainOwner,
		"createdTime":         epochSeconds(g.CreatedTime),
		"originConfiguration": map[string]any{"restrictions": restrictions},
	}
	putNonEmpty(out, "contactInfo", g.ContactInfo)
	putNonEmpty(out, "description", g.Description)

	if g.Parent != nil {
		out["parent"] = groupRefToWire(g.Parent)
	}

	return out
}

func writeGroupPage(w http.ResponseWriter, groups []*driver.PackageGroup, next string) {
	list := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		list = append(list, groupToWire(g))
	}

	body := map[string]any{"packageGroups": list}
	putNonEmpty(body, "nextToken", next)

	writeJSON(w, body)
}

// createPackageGroupRequest is the CreatePackageGroup body.
type createPackageGroupRequest struct {
	PackageGroup string    `json:"packageGroup"`
	ContactInfo  string    `json:"contactInfo"`
	Description  string    `json:"description"`
	Tags         []tagPair `json:"tags"`
}

func (h *Handler) createPackageGroup(w http.ResponseWriter, r *http.Request) {
	var req createPackageGroupRequest
	if !decodeBody(w, r, &req) {
		return
	}

	out, err := h.groups.CreatePackageGroup(r.Context(), &driver.CreatePackageGroupInput{
		Domain: domainRefFromQuery(r.URL.Query()), Pattern: req.PackageGroup,
		ContactInfo: req.ContactInfo, Description: req.Description, Tags: tagsToMap(req.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"packageGroup": groupToWire(out)})
}

func (h *Handler) describePackageGroup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	out, err := h.groups.DescribePackageGroup(r.Context(), domainRefFromQuery(q), q.Get("package-group"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"packageGroup": groupToWire(out)})
}

// updatePackageGroupRequest is the UpdatePackageGroup body; an absent field is
// left unchanged.
type updatePackageGroupRequest struct {
	PackageGroup string  `json:"packageGroup"`
	ContactInfo  *string `json:"contactInfo"`
	Description  *string `json:"description"`
}

func (h *Handler) updatePackageGroup(w http.ResponseWriter, r *http.Request) {
	var req updatePackageGroupRequest
	if !decodeBody(w, r, &req) {
		return
	}

	out, err := h.groups.UpdatePackageGroup(r.Context(), &driver.UpdatePackageGroupInput{
		Domain: domainRefFromQuery(r.URL.Query()), Pattern: req.PackageGroup, ContactInfo: req.ContactInfo, Description: req.Description,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"packageGroup": groupToWire(out)})
}

func (h *Handler) deletePackageGroup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	out, err := h.groups.DeletePackageGroup(r.Context(), domainRefFromQuery(q), q.Get("package-group"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"packageGroup": groupToWire(out)})
}

// groupOriginRequest is the UpdatePackageGroupOriginConfiguration body.
type groupOriginRequest struct {
	Restrictions              map[string]string `json:"restrictions"`
	AddAllowedRepositories    []allowedRepoPair `json:"addAllowedRepositories"`
	RemoveAllowedRepositories []allowedRepoPair `json:"removeAllowedRepositories"`
}

// allowedRepoPair mirrors one PackageGroupAllowedRepository object.
type allowedRepoPair struct {
	RepositoryName        string `json:"repositoryName"`
	OriginRestrictionType string `json:"originRestrictionType"`
}

func allowedRepos(list []allowedRepoPair) []driver.AllowedRepository {
	out := make([]driver.AllowedRepository, 0, len(list))
	for _, a := range list {
		out = append(out, driver.AllowedRepository{RepositoryName: a.RepositoryName, OriginRestrictionType: a.OriginRestrictionType})
	}

	return out
}

func (h *Handler) updatePackageGroupOrigin(w http.ResponseWriter, r *http.Request) {
	var req groupOriginRequest
	if !decodeBody(w, r, &req) {
		return
	}

	q := r.URL.Query()

	out, err := h.groups.UpdatePackageGroupOriginConfiguration(r.Context(), &driver.UpdateGroupOriginInput{
		Domain: domainRefFromQuery(q), Pattern: q.Get("package-group"), Restrictions: req.Restrictions,
		AddAllowed: allowedRepos(req.AddAllowedRepositories), RemoveAllowed: allowedRepos(req.RemoveAllowedRepositories),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	updates := make(map[string]any, len(out.Updates))
	for t, byKind := range out.Updates {
		updates[t] = byKind
	}

	writeJSON(w, map[string]any{"packageGroup": groupToWire(out.Group), "allowedRepositoryUpdates": updates})
}

func (h *Handler) listPackageGroups(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	page, ok := pageFromQuery(w, q)
	if !ok {
		return
	}

	groups, next, err := h.groups.ListPackageGroups(r.Context(), domainRefFromQuery(q), q.Get("prefix"), page)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeGroupPage(w, groups, next)
}

func (h *Handler) listSubPackageGroups(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	page, ok := pageFromQuery(w, q)
	if !ok {
		return
	}

	groups, next, err := h.groups.ListSubPackageGroups(r.Context(), domainRefFromQuery(q), q.Get("package-group"), page)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeGroupPage(w, groups, next)
}

func (h *Handler) listAssociatedPackages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	page, ok := pageFromQuery(w, q)
	if !ok {
		return
	}

	pkgs, next, err := h.groups.ListAssociatedPackages(r.Context(), domainRefFromQuery(q), q.Get("package-group"),
		q.Get("preview") == queryTrue, page)
	if err != nil {
		writeErr(w, err)

		return
	}

	list := make([]map[string]any, 0, len(pkgs))

	for _, p := range pkgs {
		entry := map[string]any{"format": p.Format, "package": p.Package, "associationType": p.AssociationType}
		putNonEmpty(entry, "namespace", p.Namespace)

		list = append(list, entry)
	}

	body := map[string]any{"packages": list}
	putNonEmpty(body, "nextToken", next)

	writeJSON(w, body)
}

func (h *Handler) getAssociatedPackageGroup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	g, strength, err := h.groups.GetAssociatedPackageGroup(r.Context(), domainRefFromQuery(q),
		q.Get("format"), q.Get("namespace"), q.Get("package"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"packageGroup": groupToWire(g), "associationType": strength})
}

func (h *Handler) listAllowedRepositories(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	page, ok := pageFromQuery(w, q)
	if !ok {
		return
	}

	repos, next, err := h.groups.ListAllowedRepositoriesForGroup(r.Context(), domainRefFromQuery(q), q.Get("package-group"),
		q.Get("originRestrictionType"), page)
	if err != nil {
		writeErr(w, err)

		return
	}

	if repos == nil {
		repos = []string{}
	}

	body := map[string]any{"allowedRepositories": repos}
	putNonEmpty(body, "nextToken", next)

	writeJSON(w, body)
}
