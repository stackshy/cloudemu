package keyspaces

import (
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/keyspaces"
	"github.com/aws/aws-sdk-go-v2/service/keyspaces/types"

	"github.com/stackshy/cloudemu/v2/server/wire"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

func (h *Handler) createType(w http.ResponseWriter, r *http.Request) {
	var in keyspaces.CreateTypeInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	fields := make([]ksdriver.FieldDefinition, 0, len(in.FieldDefinitions))
	for _, f := range in.FieldDefinitions {
		fields = append(fields, ksdriver.FieldDefinition{Name: aws.ToString(f.Name), Type: aws.ToString(f.Type)})
	}

	u, err := h.db.CreateType(r.Context(), aws.ToString(in.KeyspaceName), aws.ToString(in.TypeName), fields)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, keyspaces.CreateTypeOutput{
		KeyspaceArn: aws.String(u.KeyspaceARN), TypeName: aws.String(u.Name),
	})
}

// getType builds the GetType response. LastModifiedTimestamp is omitted (AWS
// JSON 1.0 epoch-encoded timestamps can't be produced by encoding/json).
func (h *Handler) getType(w http.ResponseWriter, r *http.Request) {
	var in keyspaces.GetTypeInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	u, err := h.db.GetType(r.Context(), aws.ToString(in.KeyspaceName), aws.ToString(in.TypeName))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := keyspaces.GetTypeOutput{
		KeyspaceName:          aws.String(u.KeyspaceName),
		KeyspaceArn:           aws.String(u.KeyspaceARN),
		TypeName:              aws.String(u.Name),
		Status:                types.TypeStatus(u.Status),
		MaxNestingDepth:       int32(u.MaxNestingDepth), //nolint:gosec // mock nesting depth never overflows int32.
		DirectParentTypes:     u.DirectParentTypes,
		DirectReferringTables: u.DirectReferringTables,
	}
	for _, f := range u.FieldDefinitions {
		out.FieldDefinitions = append(out.FieldDefinitions,
			types.FieldDefinition{Name: aws.String(f.Name), Type: aws.String(f.Type)})
	}

	writeJSON(w, out)
}

func (h *Handler) listTypes(w http.ResponseWriter, r *http.Request) {
	var in keyspaces.ListTypesInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	all, err := h.db.ListTypes(r.Context(), aws.ToString(in.KeyspaceName))
	if err != nil {
		writeErr(w, err)
		return
	}

	page, next, err := paginate(all, in.MaxResults, in.NextToken)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := keyspaces.ListTypesOutput{NextToken: next}
	for i := range page {
		out.Types = append(out.Types, page[i].Name)
	}

	writeJSON(w, out)
}

func (h *Handler) deleteType(w http.ResponseWriter, r *http.Request) {
	var in keyspaces.DeleteTypeInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	u, err := h.db.DeleteType(r.Context(), aws.ToString(in.KeyspaceName), aws.ToString(in.TypeName))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, keyspaces.DeleteTypeOutput{
		KeyspaceArn: aws.String(u.KeyspaceARN), TypeName: aws.String(u.Name),
	})
}
