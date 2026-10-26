package codeartifact

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// originToWire renders how a package version entered the repository.
func originToWire(o driver.VersionOrigin) map[string]any {
	out := map[string]any{"originType": o.OriginType}
	if o.RepositoryName != "" {
		out["domainEntryPoint"] = map[string]any{"repositoryName": o.RepositoryName}
	}

	return out
}

func assetToWire(a *driver.Asset) map[string]any {
	return map[string]any{"name": a.Name, "size": a.Size, "hashes": a.Hashes}
}

// identityToWire renders the identity members shared by the asset, readme and
// dependency responses.
func identityToWire(id *driver.VersionIdentity) map[string]any {
	out := map[string]any{"format": id.Format, "package": id.Package, "version": id.Version, "versionRevision": id.Revision}
	putNonEmpty(out, "namespace", id.Namespace)

	return out
}

func (h *Handler) listPackageVersions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	page, ok := pageFromQuery(w, q)
	if !ok {
		return
	}

	out, err := h.versions.ListPackageVersions(r.Context(), &driver.ListPackageVersionsInput{
		Ref: pkgRefFromQuery(q), Status: q.Get("status"), SortBy: q.Get("sortBy"), OriginType: q.Get("originType"), Page: page,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	versions := make([]map[string]any, 0, len(out.Versions))
	for _, v := range out.Versions {
		versions = append(versions, map[string]any{
			"version": v.Version, "revision": v.Revision, "status": v.Status, "origin": originToWire(v.Origin),
		})
	}

	body := map[string]any{"format": out.Format, "package": out.Package, "versions": versions}
	putNonEmpty(body, "namespace", out.Namespace)
	putNonEmpty(body, "defaultDisplayVersion", out.DefaultDisplayVersion)
	putNonEmpty(body, "nextToken", out.NextToken)

	writeJSON(w, body)
}

func (h *Handler) describePackageVersion(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	v, err := h.versions.DescribePackageVersion(r.Context(), pkgRefFromQuery(q), q.Get("version"))
	if err != nil {
		writeErr(w, err)

		return
	}

	desc := map[string]any{
		"format": v.Format, "packageName": v.PackageName, "displayName": v.DisplayName, "version": v.Version,
		"revision": v.Revision, "status": v.Status, "origin": originToWire(v.Origin),
	}
	putNonEmpty(desc, "namespace", v.Namespace)

	if !v.PublishedTime.IsZero() {
		desc["publishedTime"] = epochSeconds(v.PublishedTime)
	}

	writeJSON(w, map[string]any{"packageVersion": desc})
}

func (h *Handler) publishPackageVersion(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	content, err := io.ReadAll(http.MaxBytesReader(w, r.Body, driver.MaxAssetBytes+1))
	if err != nil {
		writeUploadError(w, err)

		return
	}

	res, err := h.versions.PublishPackageVersion(r.Context(), &driver.PublishInput{
		Ref: pkgRefFromQuery(q), Version: q.Get("version"), AssetName: q.Get("asset"),
		SHA256: r.Header.Get("x-amz-content-sha256"), Content: content, Unfinished: q.Get("unfinished") == queryTrue,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	body := identityToWire(&res.VersionIdentity)
	body["status"] = res.Status
	body["asset"] = assetToWire(&res.Asset)

	writeJSON(w, body)
}

// writeUploadError reports why an asset upload body could not be read: past the
// size limit it is the quota error, anything else (a dropped connection) is a
// malformed request.
func writeUploadError(w http.ResponseWriter, err error) {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeError(w, http.StatusPaymentRequired, driver.ExServiceQuota, errorBody{
			Message: "asset exceeds the emulator's " + strconv.Itoa(driver.MaxAssetBytes) + "-byte limit", ResourceType: driver.ResourceTypeAsset,
		})

		return
	}

	writeValidation(w, "could not read the asset content: "+err.Error())
}

func (h *Handler) listPackageVersionAssets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	page, ok := pageFromQuery(w, q)
	if !ok {
		return
	}

	list, next, err := h.versions.ListPackageVersionAssets(r.Context(), pkgRefFromQuery(q), q.Get("version"), page)
	if err != nil {
		writeErr(w, err)

		return
	}

	assets := make([]map[string]any, 0, len(list.Assets))
	for i := range list.Assets {
		assets = append(assets, assetToWire(&list.Assets[i]))
	}

	body := identityToWire(&list.VersionIdentity)
	body["assets"] = assets
	putNonEmpty(body, "nextToken", next)

	writeJSON(w, body)
}

func (h *Handler) getPackageVersionAsset(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	a, err := h.versions.GetPackageVersionAsset(r.Context(), pkgRefFromQuery(q), q.Get("version"), q.Get("asset"), q.Get("revision"))
	if err != nil {
		writeErr(w, err)

		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(a.Content)))
	w.Header().Set("X-AssetName", a.Name)
	w.Header().Set("X-PackageVersion", a.Version)
	w.Header().Set("X-PackageVersionRevision", a.Revision)
	w.WriteHeader(http.StatusOK)

	_, _ = w.Write(a.Content)
}

func (h *Handler) getPackageVersionReadme(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	id, err := h.versions.GetPackageVersionReadme(r.Context(), pkgRefFromQuery(q), q.Get("version"))
	if err != nil {
		writeErr(w, err)

		return
	}

	body := identityToWire(id)
	body["readme"] = ""

	writeJSON(w, body)
}

func (h *Handler) listPackageVersionDependencies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	id, err := h.versions.ListPackageVersionDependencies(r.Context(), pkgRefFromQuery(q), q.Get("version"))
	if err != nil {
		writeErr(w, err)

		return
	}

	body := identityToWire(id)
	body["dependencies"] = []map[string]any{}

	writeJSON(w, body)
}

// versionsRequest is the body shared by the bulk package version operations.
type versionsRequest struct {
	Versions         []string          `json:"versions"`
	VersionRevisions map[string]string `json:"versionRevisions"`
	ExpectedStatus   string            `json:"expectedStatus"`
	TargetStatus     string            `json:"targetStatus"`
	AllowOverwrite   bool              `json:"allowOverwrite"`
	IncludeUpstream  bool              `json:"includeFromUpstream"`
}

func versionsResultToWire(res *driver.VersionsResult) map[string]any {
	ok := make(map[string]any, len(res.Successful))
	for v, s := range res.Successful {
		ok[v] = map[string]any{"revision": s.Revision, "status": s.Status}
	}

	failed := make(map[string]any, len(res.Failed))
	for v, f := range res.Failed {
		failed[v] = map[string]any{"errorCode": f.ErrorCode, "errorMessage": f.ErrorMessage}
	}

	return map[string]any{"successfulVersions": ok, "failedVersions": failed}
}

// bulk decodes a bulk request and runs op on it, writing the per-version result.
func bulk(w http.ResponseWriter, r *http.Request, op func(*http.Request, *versionsRequest) (*driver.VersionsResult, error)) {
	var req versionsRequest
	if !decodeBody(w, r, &req) {
		return
	}

	res, err := op(r, &req)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, versionsResultToWire(res))
}

func versionsInput(r *http.Request, req *versionsRequest) driver.VersionsInput {
	return driver.VersionsInput{
		Ref: pkgRefFromQuery(r.URL.Query()), Versions: req.Versions, VersionRevisions: req.VersionRevisions, ExpectedStatus: req.ExpectedStatus,
	}
}

func (h *Handler) updatePackageVersionsStatus(w http.ResponseWriter, r *http.Request) {
	bulk(w, r, func(r *http.Request, req *versionsRequest) (*driver.VersionsResult, error) {
		return h.versions.UpdatePackageVersionsStatus(r.Context(), &driver.UpdateStatusInput{
			VersionsInput: versionsInput(r, req), TargetStatus: req.TargetStatus,
		})
	})
}

func (h *Handler) disposePackageVersions(w http.ResponseWriter, r *http.Request) {
	bulk(w, r, func(r *http.Request, req *versionsRequest) (*driver.VersionsResult, error) {
		in := versionsInput(r, req)

		return h.versions.DisposePackageVersions(r.Context(), &in)
	})
}

func (h *Handler) deletePackageVersions(w http.ResponseWriter, r *http.Request) {
	bulk(w, r, func(r *http.Request, req *versionsRequest) (*driver.VersionsResult, error) {
		in := versionsInput(r, req)

		return h.versions.DeletePackageVersions(r.Context(), &in)
	})
}

func (h *Handler) copyPackageVersions(w http.ResponseWriter, r *http.Request) {
	bulk(w, r, func(r *http.Request, req *versionsRequest) (*driver.VersionsResult, error) {
		q := r.URL.Query()

		return h.versions.CopyPackageVersions(r.Context(), &driver.CopyInput{
			Ref: driver.PackageRef{
				Domain: q.Get("domain"), Owner: q.Get("domain-owner"), Format: q.Get("format"),
				Namespace: q.Get("namespace"), Package: q.Get("package"),
			},
			SourceRepository: q.Get("source-repository"), DestinationRepository: q.Get("destination-repository"),
			Versions: req.Versions, VersionRevisions: req.VersionRevisions,
			AllowOverwrite: req.AllowOverwrite, IncludeFromUpstream: req.IncludeUpstream,
		})
	})
}
