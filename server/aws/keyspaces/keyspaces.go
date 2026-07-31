package keyspaces

import (
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/keyspaces"
	"github.com/aws/aws-sdk-go-v2/service/keyspaces/types"

	"github.com/stackshy/cloudemu/v2/server/wire"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

func (h *Handler) createKeyspace(w http.ResponseWriter, r *http.Request) {
	var in keyspaces.CreateKeyspaceInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	cfg := ksdriver.CreateKeyspaceConfig{Name: aws.ToString(in.KeyspaceName), Tags: tagMap(in.Tags)}
	if in.ReplicationSpecification != nil {
		cfg.ReplicationStrategy = string(in.ReplicationSpecification.ReplicationStrategy)
		cfg.ReplicationRegions = in.ReplicationSpecification.RegionList
	}

	ks, err := h.db.CreateKeyspace(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, keyspaces.CreateKeyspaceOutput{ResourceArn: aws.String(ks.ARN)})
}

func (h *Handler) getKeyspace(w http.ResponseWriter, r *http.Request) {
	var in keyspaces.GetKeyspaceInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	ks, err := h.db.GetKeyspace(r.Context(), aws.ToString(in.KeyspaceName))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, keyspaces.GetKeyspaceOutput{
		KeyspaceName:        aws.String(ks.Name),
		ResourceArn:         aws.String(ks.ARN),
		ReplicationStrategy: types.Rs(ks.ReplicationStrategy),
		ReplicationRegions:  ks.ReplicationRegions,
	})
}

func (h *Handler) listKeyspaces(w http.ResponseWriter, r *http.Request) {
	var in keyspaces.ListKeyspacesInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	all, err := h.db.ListKeyspaces(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	page, next, err := paginate(all, in.MaxResults, in.NextToken)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := keyspaces.ListKeyspacesOutput{NextToken: next}
	for i := range page {
		out.Keyspaces = append(out.Keyspaces, toWireKeyspaceSummary(&page[i]))
	}

	writeJSON(w, out)
}

func (h *Handler) updateKeyspace(w http.ResponseWriter, r *http.Request) {
	var in keyspaces.UpdateKeyspaceInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	var addRegions []string
	if in.ReplicationSpecification != nil {
		addRegions = in.ReplicationSpecification.RegionList
	}

	ks, err := h.db.UpdateKeyspace(r.Context(), aws.ToString(in.KeyspaceName), addRegions)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, keyspaces.UpdateKeyspaceOutput{ResourceArn: aws.String(ks.ARN)})
}

func (h *Handler) deleteKeyspace(w http.ResponseWriter, r *http.Request) {
	var in keyspaces.DeleteKeyspaceInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	if err := h.db.DeleteKeyspace(r.Context(), aws.ToString(in.KeyspaceName)); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, keyspaces.DeleteKeyspaceOutput{})
}
