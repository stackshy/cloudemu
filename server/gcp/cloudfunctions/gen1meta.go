package cloudfunctions

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strconv"
)

// gen1Meta is the GCP-specific gen1 (v1) output-only metadata a real client
// reads back from Get/List but that has no portable Serverless-driver field:
// the runtime service account, ingress policy, docker registry, the current
// build id and the monotonically increasing deploy generation (versionId).
type gen1Meta struct {
	serviceAccountEmail string
	ingressSettings     string
	dockerRegistry      string
	buildID             string
	versionID           int64
	// The remaining fields are client-owned inputs a real gen1 client reads back
	// from Get/List. They have no portable Serverless-driver equivalent, so the
	// wire handler carries them here and echoes them from toCloudFunction.
	description                string
	eventTrigger               *eventTrigger
	sourceRepository           *sourceRepository
	sourceArchiveURL           string
	sourceUploadURL            string
	maxInstances               int
	minInstances               int
	vpcConnector               string
	vpcConnectorEgressSettings string
}

// newGen1Meta builds the initial metadata for a freshly created v1 function,
// honoring any values the create body set and falling back to the defaults real
// Cloud Functions assigns.
func newGen1Meta(body *cloudFunction, project string) *gen1Meta {
	m := &gen1Meta{
		serviceAccountEmail:        body.ServiceAccountEmail,
		ingressSettings:            body.IngressSettings,
		dockerRegistry:             body.DockerRegistry,
		buildID:                    newBuildID(),
		versionID:                  1,
		description:                body.Description,
		eventTrigger:               body.EventTrigger,
		sourceRepository:           body.SourceRepository,
		sourceArchiveURL:           body.SourceArchiveURL,
		sourceUploadURL:            body.SourceUploadURL,
		maxInstances:               body.MaxInstances,
		minInstances:               body.MinInstances,
		vpcConnector:               body.VPCConnector,
		vpcConnectorEgressSettings: body.VPCConnectorEgressSettings,
	}

	applyGen1Defaults(m, project)

	return m
}

// applyGen1Defaults fills any unset gen1 metadata field with the value real
// Cloud Functions defaults to.
func applyGen1Defaults(m *gen1Meta, project string) {
	if m.serviceAccountEmail == "" {
		m.serviceAccountEmail = gen1ServiceAccount(project)
	}

	if m.ingressSettings == "" {
		m.ingressSettings = defaultIngress
	}

	if m.dockerRegistry == "" {
		m.dockerRegistry = defaultDockerRegistry
	}

	if m.buildID == "" {
		m.buildID = newBuildID()
	}

	if m.versionID == 0 {
		m.versionID = 1
	}
}

// putGen1Meta stores freshly created metadata under the function's canonical name.
func (h *Handler) putGen1Meta(name string, m *gen1Meta) {
	h.mu.Lock()
	h.gen1Meta[name] = m
	h.mu.Unlock()
}

// bumpGen1Meta advances the deploy generation for an updated function: versionId
// increments, a new build id is cut, and the gen1 metadata carried in the PATCH
// body is applied under the update mask. A function whose metadata is missing
// (created straight through the portable API) is seeded first so the bump still
// lands on version 2.
func (h *Handler) bumpGen1Meta(name string, body *cloudFunction, project string, mask updateMask) {
	h.mu.Lock()
	defer h.mu.Unlock()

	m := h.gen1Meta[name]
	if m == nil {
		m = &gen1Meta{versionID: 1}
		applyGen1Defaults(m, project)
	}

	// serviceAccountEmail/ingressSettings/dockerRegistry are always server-defaulted
	// in real gen1, so they are set when supplied but never cleared.
	if body.ServiceAccountEmail != "" {
		m.serviceAccountEmail = body.ServiceAccountEmail
	}

	if body.IngressSettings != "" {
		m.ingressSettings = body.IngressSettings
	}

	if body.DockerRegistry != "" {
		m.dockerRegistry = body.DockerRegistry
	}

	applyGen1PatchFields(m, body, mask)

	m.versionID++
	m.buildID = newBuildID()
	h.gen1Meta[name] = m
}

// applyGen1PatchFields applies the client-owned gen1 fields from a PATCH body
// under the update mask: a masked field absent from the body is cleared, while an
// unmasked (legacy) PATCH merges only non-zero values.
func applyGen1PatchFields(m *gen1Meta, body *cloudFunction, mask updateMask) {
	applyMaskedStr(mask, "description", &m.description, body.Description)
	applyMaskedStr(mask, "vpcConnector", &m.vpcConnector, body.VPCConnector)
	applyMaskedStr(mask, "vpcConnectorEgressSettings", &m.vpcConnectorEgressSettings, body.VPCConnectorEgressSettings)
	applyMaskedInt(mask, "maxInstances", &m.maxInstances, body.MaxInstances)
	applyMaskedInt(mask, "minInstances", &m.minInstances, body.MinInstances)

	if mask.covers("eventTrigger") && (mask.explicit() || body.EventTrigger != nil) {
		m.eventTrigger = body.EventTrigger
	}

	if mask.covers("sourceRepository") && (mask.explicit() || body.SourceRepository != nil) {
		m.sourceRepository = body.SourceRepository
	}

	// A redeploy that carries a fresh source records it so Get echoes the current
	// deploy source.
	if body.SourceArchiveURL != "" {
		m.sourceArchiveURL = body.SourceArchiveURL
	}

	if body.SourceUploadURL != "" {
		m.sourceUploadURL = body.SourceUploadURL
	}
}

// gen1MetaFor returns a copy of the stored metadata for name, or freshly
// defaulted metadata (versionId 1) when none is stored — the case for a function
// created directly through the portable API rather than the wire handler.
func (h *Handler) gen1MetaFor(name, project string) gen1Meta {
	h.mu.RLock()
	m := h.gen1Meta[name]
	h.mu.RUnlock()

	if m != nil {
		return *m
	}

	out := gen1Meta{versionID: 1}
	applyGen1Defaults(&out, project)

	return out
}

// gen1ServiceAccount is the App Engine default service account real gen1 Cloud
// Functions runs a function as when none is specified.
func gen1ServiceAccount(project string) string {
	return project + "@appspot.gserviceaccount.com"
}

// gen2ServiceAccount is the Compute Engine default service account real gen2
// Cloud Functions runs a function as when none is specified. The numeric project
// number is unknown to the emulator, so the project id stands in.
func gen2ServiceAccount(project string) string {
	return project + "-compute@developer.gserviceaccount.com"
}

// newBuildID returns a random build identifier, the value real Cloud Functions
// assigns per deploy and echoes back as buildId.
func newBuildID() string {
	b := make([]byte, buildIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "build-0"
	}

	return "build-" + hex.EncodeToString(b)
}

// pageRange is a half-open [start, end) slice of a sorted result set.
type pageRange struct {
	start int
	end   int
}

// paginate resolves the pageSize/pageToken query parameters against a result set
// of length total, returning the slice to serve and the nextPageToken to
// advertise (empty when the page is the last). The token is the base64-encoded
// next offset, matching the opaque-cursor contract real clients expect.
func paginate(total int, q url.Values) (page pageRange, nextToken string) {
	start := decodePageToken(q.Get("pageToken"))
	if start < 0 || start > total {
		start = 0
	}

	size := parsePageSize(q.Get("pageSize"))

	end := total
	if size > 0 && start+size < total {
		end = start + size
	}

	var next string
	if end < total {
		next = encodePageToken(end)
	}

	return pageRange{start: start, end: end}, next
}

func parsePageSize(s string) int {
	if s == "" {
		return 0
	}

	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}

	return n
}

func encodePageToken(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodePageToken(tok string) int {
	if tok == "" {
		return 0
	}

	raw, err := base64.StdEncoding.DecodeString(tok)
	if err != nil {
		return 0
	}

	n, err := strconv.Atoi(string(raw))
	if err != nil {
		return 0
	}

	return n
}
