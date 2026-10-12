package sagemaker

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// wireLineageSource is the JSON shape of an entity source (artifacts carry
// SourceTypes; actions and contexts carry SourceId and SourceType).
type wireLineageSource struct {
	SourceURI   string `json:"SourceUri"`
	SourceID    string `json:"SourceId,omitempty"`
	SourceType  string `json:"SourceType,omitempty"`
	SourceTypes []struct {
		SourceIDType string `json:"SourceIdType"`
		Value        string `json:"Value"`
	} `json:"SourceTypes,omitempty"`
}

func toLineageSource(in wireLineageSource) driver.LineageSource {
	out := driver.LineageSource{SourceURI: in.SourceURI, SourceID: in.SourceID, SourceType: in.SourceType}
	for _, t := range in.SourceTypes {
		out.SourceTypes = append(out.SourceTypes, driver.LineageSourceType{IDType: t.SourceIDType, Value: t.Value})
	}

	return out
}

func fromLineageSource(in driver.LineageSource) map[string]any {
	out := map[string]any{"SourceUri": in.SourceURI}
	setIf(out, "SourceId", in.SourceID)
	setIf(out, "SourceType", in.SourceType)

	if len(in.SourceTypes) > 0 {
		types := make([]map[string]any, 0, len(in.SourceTypes))
		for _, t := range in.SourceTypes {
			types = append(types, map[string]any{"SourceIdType": t.IDType, "Value": t.Value})
		}

		out["SourceTypes"] = types
	}

	return out
}

// lineageKinds maps an entity kind to its wire member names.
type lineageNames struct{ arn, name, typ, summaries string }

var lineageWire = map[string]lineageNames{ //nolint:gochecknoglobals // SDK-shaped
	driver.LineageArtifact: {"ArtifactArn", "ArtifactName", "ArtifactType", "ArtifactSummaries"},
	driver.LineageAction:   {"ActionArn", "ActionName", "ActionType", "ActionSummaries"},
	driver.LineageContext:  {"ContextArn", "ContextName", "ContextType", "ContextSummaries"},
}

func entitySummary(e *driver.LineageEntity) map[string]any {
	n := lineageWire[e.Kind]
	out := map[string]any{
		n.arn: e.ARN, n.typ: e.Type, "Source": fromLineageSource(e.Source),
		"CreationTime": epoch(e.CreationTime), "LastModifiedTime": epoch(e.LastModifiedTime),
	}
	setIf(out, n.name, e.Name)
	setIf(out, "Status", e.Status)

	return out
}

func entityDescribe(e *driver.LineageEntity, lineageGroupARN string) map[string]any {
	out := entitySummary(e)
	delete(out, "Status")
	setIf(out, "Status", e.Status)
	setIf(out, "Description", e.Description)
	setIf(out, "LineageGroupArn", lineageGroupARN)

	if e.Properties != nil {
		out["Properties"] = e.Properties
	}

	if md := fromMetadata(e.Metadata); md != nil {
		out["MetadataProperties"] = md
	}

	return out
}

// wireEntityRequest is the union of the create/update members of the three
// entity kinds.
type wireEntityRequest struct {
	ArtifactName       string            `json:"ArtifactName"`
	ArtifactType       string            `json:"ArtifactType"`
	ActionName         string            `json:"ActionName"`
	ActionType         string            `json:"ActionType"`
	ContextName        string            `json:"ContextName"`
	ContextType        string            `json:"ContextType"`
	ArtifactArn        string            `json:"ArtifactArn"`
	Source             wireLineageSource `json:"Source"`
	Description        *string           `json:"Description"`
	Status             *string           `json:"Status"`
	Properties         map[string]string `json:"Properties"`
	PropertiesToRemove []string          `json:"PropertiesToRemove"`
	MetadataProperties *wireMetadata     `json:"MetadataProperties"`
	Tags               []wireTag         `json:"Tags"`
}

//nolint:gocritic // large value params mirror the driver interface
func (q wireEntityRequest) spec(kind string) driver.LineageEntitySpec {
	spec := driver.LineageEntitySpec{
		Source: toLineageSource(q.Source), Description: strPtrIf(q.Description), Status: strPtrIf(q.Status),
		Properties: q.Properties, Metadata: toMetadata(q.MetadataProperties), Tags: toTags(q.Tags),
	}

	switch kind {
	case driver.LineageArtifact:
		spec.Name, spec.Type = q.ArtifactName, q.ArtifactType
	case driver.LineageAction:
		spec.Name, spec.Type = q.ActionName, q.ActionType
	default:
		spec.Name, spec.Type = q.ContextName, q.ContextType
	}

	return spec
}

//nolint:gocritic // large value params mirror the driver interface
func (q wireEntityRequest) update(kind string) driver.LineageEntityUpdate {
	up := driver.LineageEntityUpdate{
		Description: q.Description, Status: q.Status, Properties: q.Properties, PropertiesToRemove: q.PropertiesToRemove,
	}

	switch kind {
	case driver.LineageArtifact:
		up.Key = q.ArtifactArn

		if q.ArtifactName != "" {
			n := q.ArtifactName
			up.Name = &n
		}
	case driver.LineageAction:
		up.Key = q.ActionName
	default:
		up.Key = q.ContextName
	}

	return up
}

//nolint:dupl,lll,revive // per-resource shim mirrors the SDK shape; SDK-shaped literal; SDK-shaped
func (h *Handler) createEntityOp(kind string, create func(*http.Request, driver.LineageEntitySpec) (*driver.LineageEntity, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req wireEntityRequest
		if !wire.DecodeJSON(w, r, &req) {
			return
		}

		e, err := create(r, req.spec(kind))
		if err != nil {
			writeDriverError(w, err)

			return
		}

		wire.WriteJSON(w, map[string]any{lineageWire[kind].arn: e.ARN})
	}
}

//nolint:lll // SDK-shaped literal
func (h *Handler) describeEntityOp(_, keyField string, describe func(*http.Request, string) (*driver.LineageEntity, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, ok := decodeName1(w, r, keyField)
		if !ok {
			return
		}

		e, err := describe(r, key)
		if err != nil {
			writeDriverError(w, err)

			return
		}

		wire.WriteJSON(w, entityDescribe(e, h.lineageGroupARN(r)))
	}
}

// lineageGroupARN returns the default lineage group's ARN when it exists.
func (h *Handler) lineageGroupARN(r *http.Request) string {
	g, err := h.svc.DescribeLineageGroup(r.Context(), driver.DefaultLineageGroupName)
	if err != nil {
		return ""
	}

	return g.ARN
}

//nolint:lll // SDK-shaped literal
func (*Handler) listEntityOp(kind string, list func(*http.Request, driver.ListOptions) (driver.ListPage[driver.LineageEntity], error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeListReq(w, r)
		if !ok {
			return
		}

		page, err := list(r, req.options())
		if err != nil {
			writeDriverError(w, err)

			return
		}

		writeSummaries(w, lineageWire[kind].summaries, page, entitySummary)
	}
}

//nolint:dupl,lll,revive // per-resource shim mirrors the SDK shape; SDK-shaped literal; SDK-shaped
func (h *Handler) updateEntityOp(kind string, update func(*http.Request, driver.LineageEntityUpdate) (*driver.LineageEntity, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req wireEntityRequest
		if !wire.DecodeJSON(w, r, &req) {
			return
		}

		e, err := update(r, req.update(kind))
		if err != nil {
			writeDriverError(w, err)

			return
		}

		wire.WriteJSON(w, map[string]any{lineageWire[kind].arn: e.ARN})
	}
}

func (h *Handler) createArtifact(w http.ResponseWriter, r *http.Request) {
	h.createEntityOp(driver.LineageArtifact, func(r *http.Request, s driver.LineageEntitySpec) (*driver.LineageEntity, error) {
		return h.svc.CreateArtifact(r.Context(), s)
	})(w, r)
}

func (h *Handler) describeArtifact(w http.ResponseWriter, r *http.Request) {
	h.describeEntityOp(driver.LineageArtifact, "ArtifactArn", func(r *http.Request, k string) (*driver.LineageEntity, error) {
		return h.svc.DescribeArtifact(r.Context(), k)
	})(w, r)
}

func (h *Handler) listArtifacts(w http.ResponseWriter, r *http.Request) {
	h.listEntityOp(driver.LineageArtifact, func(r *http.Request, o driver.ListOptions) (driver.ListPage[driver.LineageEntity], error) {
		return h.svc.ListArtifacts(r.Context(), o)
	})(w, r)
}

func (h *Handler) updateArtifact(w http.ResponseWriter, r *http.Request) {
	h.updateEntityOp(driver.LineageArtifact, func(r *http.Request, u driver.LineageEntityUpdate) (*driver.LineageEntity, error) {
		return h.svc.UpdateArtifact(r.Context(), u)
	})(w, r)
}

func (h *Handler) deleteArtifact(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ArtifactArn string `json:"ArtifactArn"`
		Source      *struct {
			SourceURI string `json:"SourceUri"`
		} `json:"Source"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	uri := ""
	if req.Source != nil {
		uri = req.Source.SourceURI
	}

	arn, err := h.svc.DeleteArtifact(r.Context(), req.ArtifactArn, uri)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ArtifactArn": arn})
}

func (h *Handler) createAction(w http.ResponseWriter, r *http.Request) {
	h.createEntityOp(driver.LineageAction, func(r *http.Request, s driver.LineageEntitySpec) (*driver.LineageEntity, error) {
		return h.svc.CreateAction(r.Context(), s)
	})(w, r)
}

func (h *Handler) describeAction(w http.ResponseWriter, r *http.Request) {
	h.describeEntityOp(driver.LineageAction, "ActionName", func(r *http.Request, k string) (*driver.LineageEntity, error) {
		return h.svc.DescribeAction(r.Context(), k)
	})(w, r)
}

func (h *Handler) listActions(w http.ResponseWriter, r *http.Request) {
	h.listEntityOp(driver.LineageAction, func(r *http.Request, o driver.ListOptions) (driver.ListPage[driver.LineageEntity], error) {
		return h.svc.ListActions(r.Context(), o)
	})(w, r)
}

func (h *Handler) updateAction(w http.ResponseWriter, r *http.Request) {
	h.updateEntityOp(driver.LineageAction, func(r *http.Request, u driver.LineageEntityUpdate) (*driver.LineageEntity, error) {
		return h.svc.UpdateAction(r.Context(), u)
	})(w, r)
}

func (h *Handler) deleteAction(w http.ResponseWriter, r *http.Request) {
	h.deleteByName(w, r, "ActionName", "ActionArn", h.svc.DeleteAction)
}

func (h *Handler) createContext(w http.ResponseWriter, r *http.Request) {
	h.createEntityOp(driver.LineageContext, func(r *http.Request, s driver.LineageEntitySpec) (*driver.LineageEntity, error) {
		return h.svc.CreateContext(r.Context(), s)
	})(w, r)
}

func (h *Handler) describeContext(w http.ResponseWriter, r *http.Request) {
	h.describeEntityOp(driver.LineageContext, "ContextName", func(r *http.Request, k string) (*driver.LineageEntity, error) {
		return h.svc.DescribeContext(r.Context(), k)
	})(w, r)
}

func (h *Handler) listContexts(w http.ResponseWriter, r *http.Request) {
	h.listEntityOp(driver.LineageContext, func(r *http.Request, o driver.ListOptions) (driver.ListPage[driver.LineageEntity], error) {
		return h.svc.ListContexts(r.Context(), o)
	})(w, r)
}

func (h *Handler) updateContext(w http.ResponseWriter, r *http.Request) {
	h.updateEntityOp(driver.LineageContext, func(r *http.Request, u driver.LineageEntityUpdate) (*driver.LineageEntity, error) {
		return h.svc.UpdateContext(r.Context(), u)
	})(w, r)
}

func (h *Handler) deleteContext(w http.ResponseWriter, r *http.Request) {
	h.deleteByName(w, r, "ContextName", "ContextArn", h.svc.DeleteContext)
}

// deleteByName serves Delete operations that take a name and answer with the ARN.
func (*Handler) deleteByName(w http.ResponseWriter, r *http.Request, field, arnField string,
	del func(context.Context, string) (string, error),
) {
	name, ok := decodeName1(w, r, field)
	if !ok {
		return
	}

	arn, err := del(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{arnField: arn})
}

// --- Associations ---

func (h *Handler) addAssociation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SourceArn       string `json:"SourceArn"`
		DestinationArn  string `json:"DestinationArn"`
		AssociationType string `json:"AssociationType"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.svc.AddAssociation(r.Context(), req.SourceArn, req.DestinationArn, req.AssociationType); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"SourceArn": req.SourceArn, "DestinationArn": req.DestinationArn})
}

func (h *Handler) deleteAssociation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SourceArn      string `json:"SourceArn"`
		DestinationArn string `json:"DestinationArn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.svc.DeleteAssociation(r.Context(), req.SourceArn, req.DestinationArn); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"SourceArn": req.SourceArn, "DestinationArn": req.DestinationArn})
}

func (h *Handler) listAssociations(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	page, err := h.svc.ListAssociations(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "AssociationSummaries", page, func(a *driver.AssociationSummary) map[string]any {
		out := map[string]any{
			"SourceArn": a.SourceARN, "DestinationArn": a.DestinationARN, "CreationTime": epoch(a.CreationTime),
		}
		setIf(out, "SourceName", a.SourceName)
		setIf(out, "DestinationName", a.DestinationName)
		setIf(out, "SourceType", a.SourceType)
		setIf(out, "DestinationType", a.DestinationType)
		setIf(out, "AssociationType", a.Type)

		return out
	})
}

func (h *Handler) queryLineage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StartArns    []string `json:"StartArns"`
		Direction    string   `json:"Direction"`
		IncludeEdges bool     `json:"IncludeEdges"`
		MaxDepth     int      `json:"MaxDepth"`
		MaxResults   int      `json:"MaxResults"`
		NextToken    string   `json:"NextToken"`
		Filters      struct {
			LineageTypes   []string          `json:"LineageTypes"`
			Types          []string          `json:"Types"`
			Properties     map[string]string `json:"Properties"`
			CreatedBefore  *float64          `json:"CreatedBefore"`
			CreatedAfter   *float64          `json:"CreatedAfter"`
			ModifiedBefore *float64          `json:"ModifiedBefore"`
			ModifiedAfter  *float64          `json:"ModifiedAfter"`
		} `json:"Filters"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	f := req.Filters
	res, err := h.svc.QueryLineage(r.Context(), driver.LineageQuery{
		StartARNs: req.StartArns, Direction: req.Direction, IncludeEdges: req.IncludeEdges, MaxDepth: req.MaxDepth,
		MaxResults: req.MaxResults, NextToken: req.NextToken, LineageTypes: f.LineageTypes, Types: f.Types, Properties: f.Properties,
		CreatedBefore: isoFrom(f.CreatedBefore), CreatedAfter: isoFrom(f.CreatedAfter),
		ModifiedBefore: isoFrom(f.ModifiedBefore), ModifiedAfter: isoFrom(f.ModifiedAfter),
	})

	if err != nil {
		writeDriverError(w, err)

		return
	}

	vertices := make([]map[string]any, 0, len(res.Vertices))

	for _, v := range res.Vertices {
		e := map[string]any{"Arn": v.ARN}
		setIf(e, "Type", v.Type)
		setIf(e, "LineageType", v.LineageType)
		vertices = append(vertices, e)
	}

	edges := make([]map[string]any, 0, len(res.Edges))

	for _, e := range res.Edges {
		m := map[string]any{"SourceArn": e.SourceARN, "DestinationArn": e.DestinationARN}
		setIf(m, "AssociationType", e.AssociationType)
		edges = append(edges, m)
	}

	resp := map[string]any{"Vertices": vertices, "Edges": edges}
	if res.NextToken != "" {
		resp["NextToken"] = res.NextToken
	}

	wire.WriteJSON(w, resp)
}

// --- Lineage group ---

func lineageGroupToWire(g *driver.LineageGroup) map[string]any {
	return map[string]any{
		"LineageGroupName": g.Name, "LineageGroupArn": g.ARN, "DisplayName": g.DisplayName,
		"CreationTime": epoch(g.CreationTime), "LastModifiedTime": epoch(g.LastModifiedTime),
	}
}

func (h *Handler) describeLineageGroup(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "LineageGroupName")
	if !ok {
		return
	}

	g, err := h.svc.DescribeLineageGroup(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, lineageGroupToWire(g))
}

func (h *Handler) listLineageGroups(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	page, err := h.svc.ListLineageGroups(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "LineageGroupSummaries", page, lineageGroupToWire)
}

func (h *Handler) getLineageGroupPolicy(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "LineageGroupName")
	if !ok {
		return
	}

	arn, policy, err := h.svc.GetLineageGroupPolicy(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	resp := map[string]any{"LineageGroupArn": arn}
	setIf(resp, "ResourcePolicy", policy)

	wire.WriteJSON(w, resp)
}
