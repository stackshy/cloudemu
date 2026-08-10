package opensearch

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// servePackages routes the /2021-01-01/packages/* subtree.
func (h *Handler) servePackages(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		if r.Method == http.MethodPost {
			h.createPackage(w, r)

			return
		}

		methodNotAllowed(w)

		return
	}

	switch rest[0] {
	case "describe":
		h.describePackages(w, r)
	case "update":
		h.updatePackage(w, r)
	case "updateScope":
		h.updatePackageScope(w, r)
	case "associate":
		h.associatePackage(w, r, rest[1:])
	case "associateMultiple":
		h.associatePackages(w, r)
	case "dissociate":
		h.dissociatePackage(w, r, rest[1:])
	case "dissociateMultiple":
		h.dissociatePackages(w, r)
	default:
		h.servePackageByID(w, r, rest)
	}
}

// servePackageByID handles /packages/{id}, /packages/{id}/history, and
// /packages/{id}/domains.
func (h *Handler) servePackageByID(w http.ResponseWriter, r *http.Request, rest []string) {
	id := rest[0]

	if len(rest) == 1 {
		if r.Method == http.MethodDelete {
			h.deletePackage(w, r, id)

			return
		}

		methodNotAllowed(w)

		return
	}

	if len(rest) == 2 && r.Method == http.MethodGet {
		switch rest[1] {
		case "history":
			h.getPackageVersionHistory(w, r, id)

			return
		case "domains":
			h.listDomainsForPackage(w, r, id)

			return
		}
	}

	notFoundPath(w, r.URL.Path)
}

func (h *Handler) createPackage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PackageName        string `json:"PackageName"`
		PackageType        string `json:"PackageType"`
		PackageDescription string `json:"PackageDescription"`
		EngineVersion      string `json:"EngineVersion"`
		PackageSource      struct {
			S3BucketName string `json:"S3BucketName"`
			S3Key        string `json:"S3Key"`
		} `json:"PackageSource"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	out, err := h.os.CreatePackage(r.Context(), driver.CreatePackageInput{
		PackageName:        req.PackageName,
		PackageType:        req.PackageType,
		PackageDescription: req.PackageDescription,
		EngineVersion:      req.EngineVersion,
		S3BucketName:       req.PackageSource.S3BucketName,
		S3Key:              req.PackageSource.S3Key,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"PackageDetails": packageToWire(out)})
}

func (h *Handler) deletePackage(w http.ResponseWriter, r *http.Request, id string) {
	out, err := h.os.DeletePackage(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"PackageDetails": packageToWire(out)})
}

func (h *Handler) describePackages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MaxResults int32  `json:"MaxResults"`
		NextToken  string `json:"NextToken"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	list, next, err := h.os.DescribePackages(r.Context(), driver.Page{NextToken: req.NextToken, MaxResults: req.MaxResults})
	if err != nil {
		writeErr(w, err)

		return
	}

	details := make([]map[string]any, 0, len(list))
	for i := range list {
		details = append(details, packageToWire(&list[i]))
	}

	writeJSON(w, withNext(map[string]any{"PackageDetailsList": details}, next))
}

func (h *Handler) updatePackage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PackageID          string `json:"PackageID"`
		PackageDescription string `json:"PackageDescription"`
		PackageSource      struct {
			S3BucketName string `json:"S3BucketName"`
			S3Key        string `json:"S3Key"`
		} `json:"PackageSource"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	out, err := h.os.UpdatePackage(r.Context(), req.PackageID, req.PackageDescription, req.PackageSource.S3BucketName, req.PackageSource.S3Key)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"PackageDetails": packageToWire(out)})
}

func (h *Handler) updatePackageScope(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PackageID       string   `json:"PackageID"`
		Operation       string   `json:"Operation"`
		PackageUserList []string `json:"PackageUserList"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	id, users, err := h.os.UpdatePackageScope(r.Context(), req.PackageID, req.Operation, req.PackageUserList)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"PackageID": id, "Operation": req.Operation, "PackageUserList": users})
}

func (h *Handler) getPackageVersionHistory(w http.ResponseWriter, r *http.Request, id string) {
	pkgID, history, next, err := h.os.GetPackageVersionHistory(r.Context(), id, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"PackageID": pkgID, "PackageVersionHistoryList": history}, next))
}

// associatePackage handles POST /packages/associate/{PackageID}/{DomainName}.
func (h *Handler) associatePackage(w http.ResponseWriter, r *http.Request, rest []string) {
	const wantSegs = 2
	if len(rest) != wantSegs || r.Method != http.MethodPost {
		notFoundPath(w, r.URL.Path)

		return
	}

	out, err := h.os.AssociatePackage(r.Context(), rest[0], rest[1])
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"DomainPackageDetails": associationToWire(out)})
}

func (h *Handler) associatePackages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PackageList []string `json:"PackageList"`
		DomainName  string   `json:"DomainName"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	list, err := h.os.AssociatePackages(r.Context(), req.PackageList, req.DomainName)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"DomainPackageDetailsList": associationsToWire(list)})
}

// dissociatePackage handles POST /packages/dissociate/{PackageID}/{DomainName}.
func (h *Handler) dissociatePackage(w http.ResponseWriter, r *http.Request, rest []string) {
	const wantSegs = 2
	if len(rest) != wantSegs || r.Method != http.MethodPost {
		notFoundPath(w, r.URL.Path)

		return
	}

	out, err := h.os.DissociatePackage(r.Context(), rest[0], rest[1])
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"DomainPackageDetails": associationToWire(out)})
}

func (h *Handler) dissociatePackages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PackageList []string `json:"PackageList"`
		DomainName  string   `json:"DomainName"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	list, err := h.os.DissociatePackages(r.Context(), req.PackageList, req.DomainName)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"DomainPackageDetailsList": associationsToWire(list)})
}

func (h *Handler) listPackagesForDomain(w http.ResponseWriter, r *http.Request, domainName string) {
	list, next, err := h.os.ListPackagesForDomain(r.Context(), domainName, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"DomainPackageDetailsList": associationsToWire(list)}, next))
}

func (h *Handler) listDomainsForPackage(w http.ResponseWriter, r *http.Request, id string) {
	list, next, err := h.os.ListDomainsForPackage(r.Context(), id, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"DomainPackageDetailsList": associationsToWire(list)}, next))
}

// associationsToWire renders a list of associations.
func associationsToWire(list []driver.DomainPackageAssociation) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for i := range list {
		out = append(out, associationToWire(&list[i]))
	}

	return out
}
