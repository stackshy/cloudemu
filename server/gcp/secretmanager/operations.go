package secretmanager

import (
	"errors"
	"hash/crc32"
	"net/http"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// castagnoli is the CRC32C polynomial table GCP Secret Manager checksums with.
var castagnoli = crc32.MakeTable(crc32.Castagnoli) //nolint:gochecknoglobals // immutable lookup table

// crc32c returns the Castagnoli CRC32C of data, as GCP Secret Manager reports it.
func crc32c(data []byte) uint32 {
	return crc32.Checksum(data, castagnoli)
}

// maskHas reports whether a field-update mask names any of the given field
// aliases. An empty mask is treated as "update everything present in the body".
func maskHas(mask string, fields ...string) bool {
	if strings.TrimSpace(mask) == "" {
		return true
	}

	for _, f := range strings.Split(mask, ",") {
		f = strings.TrimSpace(f)
		for _, want := range fields {
			if f == want {
				return true
			}
		}
	}

	return false
}

// filterSecrets applies a GCP list filter of the form "labels.<k>=<v>" (the
// common case); an empty or unrecognized filter passes everything through.
func filterSecrets(secrets []secretsdriver.SecretInfo, filter string) []secretsdriver.SecretInfo {
	key, val, ok := parseLabelFilter(filter)
	if !ok {
		return secrets
	}

	out := make([]secretsdriver.SecretInfo, 0, len(secrets))

	for i := range secrets {
		if secrets[i].Tags[key] == val {
			out = append(out, secrets[i])
		}
	}

	return out
}

// parseLabelFilter parses a "labels.<key>=<value>" filter expression.
func parseLabelFilter(filter string) (key, val string, ok bool) {
	filter = strings.TrimSpace(filter)
	if !strings.HasPrefix(filter, "labels.") {
		return "", "", false
	}

	kv := strings.TrimPrefix(filter, "labels.")

	k, v, found := strings.Cut(kv, "=")
	if !found {
		return "", "", false
	}

	return strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"`), true
}

// filterVersions applies a GCP version list filter. Only "state:<STATE>" (e.g.
// "state:ENABLED") is recognized; other filters pass everything through.
func filterVersions(versions []secretsdriver.SecretVersion, filter string) []secretsdriver.SecretVersion {
	filter = strings.TrimSpace(filter)

	state, ok := strings.CutPrefix(filter, "state:")
	if !ok {
		return versions
	}

	state = strings.ToUpper(strings.TrimSpace(state))

	out := make([]secretsdriver.SecretVersion, 0, len(versions))

	for _, v := range versions {
		vs := v.State
		if vs == "" {
			vs = secretsdriver.VersionEnabled
		}

		if vs == state {
			out = append(out, v)
		}
	}

	return out
}

const (
	defaultPageSize = 100
	maxPageSize     = 25000
)

// pageSize reads ?pageSize, clamping to a sane default and ceiling.
func pageSize(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if err != nil || n <= 0 {
		return defaultPageSize
	}

	if n > maxPageSize {
		return maxPageSize
	}

	return n
}

// pageToken reads the opaque ?pageToken continuation cursor.
func pageToken(r *http.Request) string {
	return r.URL.Query().Get("pageToken")
}

// versionLess orders two numeric version ids ascending; non-numeric ids fall
// back to string order.
func versionLess(a, b string) bool {
	ai, aerr := strconv.Atoi(a)
	bi, berr := strconv.Atoi(b)

	if aerr == nil && berr == nil {
		return ai < bi
	}

	return a < b
}

func (h *Handler) createSecret(w http.ResponseWriter, r *http.Request, rt route) {
	var req createSecretRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	// Real GCP requires a replication policy on create and rejects an empty
	// user-managed replica list with INVALID_ARGUMENT.
	if req.Replication == nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", "replication is required")
		return
	}

	if req.Replication.UserManaged != nil && len(req.Replication.UserManaged.Replicas) == 0 {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", "userManaged replication requires at least one replica")
		return
	}

	id := r.URL.Query().Get("secretId")

	info, err := h.secrets.CreateSecret(r.Context(), secretsdriver.SecretConfig{
		Name:           id,
		Tags:           req.Labels,
		Replication:    replicationFromJSON(req.Replication),
		Annotations:    req.Annotations,
		TTL:            req.TTL,
		ExpireTime:     req.ExpireTime,
		Rotation:       rotationFromJSON(req.Rotation),
		Topics:         topicsFromJSON(req.Topics),
		VersionAliases: req.VersionAliases,
	}, nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toSecretJSON(rt.project, info))
}

// rotationFromJSON decodes a wire rotation policy into the driver model.
func rotationFromJSON(rot *rotationJSON) *secretsdriver.GCPRotation {
	if rot == nil {
		return nil
	}

	return &secretsdriver.GCPRotation{RotationPeriod: rot.RotationPeriod, NextRotationTime: rot.NextRotationTime}
}

// topicsFromJSON flattens wire topics to their names.
func topicsFromJSON(topics []topicJSON) []string {
	if len(topics) == 0 {
		return nil
	}

	out := make([]string, 0, len(topics))
	for _, t := range topics {
		out = append(out, t.Name)
	}

	return out
}

// secretReplication fetches the parent secret's replication policy for
// rendering version replicationStatus; nil when the secret can't be read.
func (h *Handler) secretReplication(r *http.Request, secret string) *secretsdriver.GCPReplication {
	info, err := h.secrets.GetSecret(r.Context(), secret)
	if err != nil {
		return nil
	}

	return info.Replication
}

func (h *Handler) getSecret(w http.ResponseWriter, r *http.Request, rt route) {
	info, err := h.secrets.GetSecret(r.Context(), rt.secret)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toSecretJSON(rt.project, info))
}

func (h *Handler) listSecrets(w http.ResponseWriter, r *http.Request, rt route) {
	infos, err := h.secrets.ListSecrets(r.Context())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	infos = filterSecrets(infos, r.URL.Query().Get("filter"))

	// Stable order by secret name so offset page tokens stay meaningful across
	// calls (ListSecrets iterates a map, which is unordered).
	page, err := pagination.PaginateSorted(infos,
		func(a, b secretsdriver.SecretInfo) bool { return a.Name < b.Name },
		pageToken(r), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	out := make([]secretJSON, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, toSecretJSON(rt.project, &page.Items[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listSecretsResponse{
		Secrets:       out,
		TotalSize:     len(infos),
		NextPageToken: page.NextPageToken,
	})
}

func (h *Handler) deleteSecret(w http.ResponseWriter, r *http.Request, rt route) {
	if err := h.secrets.DeleteSecret(r.Context(), rt.secret); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// google.protobuf.Empty.
	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}

func (h *Handler) addVersion(w http.ResponseWriter, r *http.Request, rt route) {
	var req addVersionRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	// GCP rejects an empty payload, and verifies a client-supplied CRC32C
	// (Castagnoli) checksum against the received data.
	if len(req.Payload.Data) == 0 {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", "payload data is required")
		return
	}

	if req.Payload.DataCrc32c != 0 {
		if got := int64(crc32c(req.Payload.Data)); got != req.Payload.DataCrc32c {
			gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", "payload data corrupted: dataCrc32c mismatch")
			return
		}
	}

	ver, err := h.secrets.PutSecretValue(r.Context(), rt.secret, req.Payload.Data)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toVersionJSON(rt.project, rt.secret, ver, h.secretReplication(r, rt.secret)))
}

func (h *Handler) listVersions(w http.ResponseWriter, r *http.Request, rt route) {
	versions, err := h.secrets.ListSecretVersions(r.Context(), rt.secret)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	versions = filterVersions(versions, r.URL.Query().Get("filter"))
	rep := h.secretReplication(r, rt.secret)

	// Real GCP lists versions newest-first (descending version number).
	page, err := pagination.PaginateSorted(versions,
		func(a, b secretsdriver.SecretVersion) bool { return versionLess(b.VersionID, a.VersionID) },
		pageToken(r), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	out := make([]versionResourceJSON, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, toVersionJSON(rt.project, rt.secret, &page.Items[i], rep))
	}

	gcprest.WriteJSON(w, http.StatusOK, listVersionsResponse{
		Versions:      out,
		TotalSize:     len(versions),
		NextPageToken: page.NextPageToken,
	})
}

func (h *Handler) patchSecret(w http.ResponseWriter, r *http.Request, rt route) {
	if h.gcp == nil {
		writeUnsupported(w)
		return
	}

	var req patchSecretRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	// The update mask names the fields to change. GCP masks name JSON fields in
	// camelCase; a few clients emit snake_case, so accept both.
	mask := r.URL.Query().Get("updateMask")
	patch := secretsdriver.GCPSecretPatch{}

	if maskHas(mask, "labels") {
		patch.Labels = req.Labels
		patch.SetLabels = true
	}

	if maskHas(mask, "annotations") {
		patch.Annotations = req.Annotations
		patch.SetAnnotations = true
	}

	if maskHas(mask, "topics") {
		patch.Topics = topicsFromJSON(req.Topics)
		patch.SetTopics = true
	}

	if maskHas(mask, "versionAliases", "version_aliases") {
		patch.VersionAliases = req.VersionAliases
		patch.SetVersionAliases = true
	}

	if maskHas(mask, "rotation") {
		patch.Rotation = rotationFromJSON(req.Rotation)
		patch.SetRotation = true
	}

	if maskHas(mask, "expireTime", "expire_time") {
		patch.ExpireTime = req.ExpireTime
		patch.SetExpireTime = true
	}

	if maskHas(mask, "ttl") {
		patch.TTL = req.TTL
		patch.SetExpireTime = true
	}

	patch.Etag = req.Etag

	info, err := h.gcp.PatchSecret(r.Context(), rt.secret, patch)
	if err != nil {
		writeSecretErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toSecretJSON(rt.project, info))
}

// writePreconditionErr writes GCP's 412 conditionNotMet response for a stale
// etag precondition (matching GCS/Compute Engine's fingerprint convention
// elsewhere in cloudemu) and reports whether err was one. The caller maps any
// other error itself.
func writePreconditionErr(w http.ResponseWriter, err error) bool {
	var preErr *secretsdriver.GCPSecretPreconditionError
	if !errors.As(err, &preErr) {
		return false
	}

	gcprest.WriteError(w, http.StatusPreconditionFailed, "conditionNotMet", preErr.Error())

	return true
}

// writeSecretErr maps a *secretsdriver.GCPSecretPreconditionError to GCP's
// 412 conditionNotMet response and any other error through the shared
// gcprest mapping.
func writeSecretErr(w http.ResponseWriter, err error) {
	if !writePreconditionErr(w, err) {
		gcprest.WriteCErr(w, err)
	}
}

// mutateVersion applies an enable/disable/destroy lifecycle verb to a version.
func (h *Handler) mutateVersion(w http.ResponseWriter, r *http.Request, rt route, verb string) {
	if h.gcp == nil {
		writeUnsupported(w)
		return
	}

	var req lifecycleVerbRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	var (
		ver *secretsdriver.SecretVersion
		err error
	)

	switch verb {
	case verbEnable:
		ver, err = h.gcp.EnableSecretVersion(r.Context(), rt.secret, driverVersion(rt.version), req.Etag)
	case verbDisable:
		ver, err = h.gcp.DisableSecretVersion(r.Context(), rt.secret, driverVersion(rt.version), req.Etag)
	case verbDestroy:
		ver, err = h.gcp.DestroySecretVersion(r.Context(), rt.secret, driverVersion(rt.version), req.Etag)
	default:
		writeUnsupported(w)
		return
	}

	if err != nil {
		if writePreconditionErr(w, err) {
			return
		}

		// An illegal state transition (e.g. destroy/disable/enable on a DESTROYED
		// version) is FAILED_PRECONDITION, which GCP reports as HTTP 400. The
		// shared gcprest mapping would turn it into 409, so answer 400 locally
		// without altering that cross-cutting mapping.
		if cerrors.IsFailedPrecondition(err) {
			gcprest.WriteError(w, http.StatusBadRequest, "failedPrecondition", err.Error())
			return
		}

		gcprest.WriteCErr(w, err)

		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toVersionJSON(rt.project, rt.secret, ver, h.secretReplication(r, rt.secret)))
}

func (h *Handler) getVersion(w http.ResponseWriter, r *http.Request, rt route) {
	ver, err := h.secrets.GetSecretValue(r.Context(), rt.secret, driverVersion(rt.version))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toVersionJSON(rt.project, rt.secret, ver, h.secretReplication(r, rt.secret)))
}

func (h *Handler) accessVersion(w http.ResponseWriter, r *http.Request, rt route) {
	ver, err := h.secrets.GetSecretValue(r.Context(), rt.secret, driverVersion(rt.version))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// A version's payload can only be accessed in the ENABLED state; disabled
	// and destroyed versions fail with FAILED_PRECONDITION, matching real GCP.
	if ver.State != "" && ver.State != secretsdriver.VersionEnabled {
		gcprest.WriteError(w, http.StatusBadRequest, "failedPrecondition",
			"cannot access version "+ver.VersionID+" in state "+ver.State)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, accessResponse{
		Name:    versionName(rt.project, rt.secret, ver.VersionID),
		Payload: payloadJSON{Data: ver.Value, DataCrc32c: int64(crc32c(ver.Value))},
	})
}
