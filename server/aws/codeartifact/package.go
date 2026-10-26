package codeartifact

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// originConfigToWire renders a package's origin configuration.
func originConfigToWire(r driver.OriginRestrictions) map[string]any {
	return map[string]any{"restrictions": map[string]any{"publish": r.Publish, "upstream": r.Upstream}}
}

// packageDescriptionToWire renders a package as a PackageDescription.
func packageDescriptionToWire(p *driver.Package) map[string]any {
	out := map[string]any{"format": p.Format, "name": p.Name, "originConfiguration": originConfigToWire(p.Restrictions)}
	putNonEmpty(out, "namespace", p.Namespace)

	return out
}

// packageSummaryToWire renders a package as a PackageSummary.
func packageSummaryToWire(p *driver.Package) map[string]any {
	out := map[string]any{"format": p.Format, "package": p.Name, "originConfiguration": originConfigToWire(p.Restrictions)}
	putNonEmpty(out, "namespace", p.Namespace)

	return out
}

func (h *Handler) listPackages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	page, ok := pageFromQuery(w, q)
	if !ok {
		return
	}

	pkgs, next, err := h.packages.ListPackages(r.Context(), &driver.ListPackagesInput{
		Repository: repoRefFromQuery(q), Format: q.Get("format"), Namespace: q.Get("namespace"), Prefix: q.Get("package-prefix"),
		Publish: q.Get("publish"), Upstream: q.Get("upstream"), Page: page,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	list := make([]map[string]any, 0, len(pkgs))
	for _, p := range pkgs {
		list = append(list, packageSummaryToWire(p))
	}

	body := map[string]any{"packages": list}
	putNonEmpty(body, "nextToken", next)

	writeJSON(w, body)
}

func (h *Handler) describePackage(w http.ResponseWriter, r *http.Request) {
	out, err := h.packages.DescribePackage(r.Context(), pkgRefFromQuery(r.URL.Query()))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"package": packageDescriptionToWire(out)})
}

// putPackageOriginRequest is the PutPackageOriginConfiguration body.
type putPackageOriginRequest struct {
	Restrictions struct {
		Publish  string `json:"publish"`
		Upstream string `json:"upstream"`
	} `json:"restrictions"`
}

func (h *Handler) putPackageOriginConfiguration(w http.ResponseWriter, r *http.Request) {
	var req putPackageOriginRequest
	if !decodeBody(w, r, &req) {
		return
	}

	out, err := h.packages.PutPackageOriginConfiguration(r.Context(), pkgRefFromQuery(r.URL.Query()),
		driver.OriginRestrictions{Publish: req.Restrictions.Publish, Upstream: req.Restrictions.Upstream})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"originConfiguration": originConfigToWire(out.Restrictions)})
}

func (h *Handler) deletePackage(w http.ResponseWriter, r *http.Request) {
	out, err := h.packages.DeletePackage(r.Context(), pkgRefFromQuery(r.URL.Query()))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"deletedPackage": packageSummaryToWire(out)})
}
