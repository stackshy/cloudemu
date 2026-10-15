package cloudfront

import (
	"net/http"
	"sort"

	"github.com/stackshy/cloudemu/v2/server/wire"
	cfdriver "github.com/stackshy/cloudemu/v2/services/cloudfront/driver"
)

// serveTagging runs a tagging operation on the distribution its Resource ARN
// names. An ARN that is not a distribution ARN of this account names no
// resource here: the driver resolves a distribution by the id at the end of
// any ARN, so without this check an ARN of another account would act on this
// account's distribution of the same id.
func (h *Handler) serveTagging(w http.ResponseWriter, r *http.Request, op opID, a *opArgs) {
	if a.id == "" || (h.accountID != "" && a.resourceAccount != h.accountID) {
		writeErr(w, cfdriver.ErrNoSuchResource)
		return
	}

	switch op {
	case opTagResource:
		h.tagResource(w, r, a.resource)
	case opUntagResource:
		h.untagResource(w, r, a.resource)
	default:
		h.listTagsForResource(w, r, a.resource)
	}
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request, arn string) {
	tags, err := h.cf.ListTagsForResource(r.Context(), arn)
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := tagsResponse{Xmlns: xmlns, Items: make([]tagXML, 0, len(tags))}

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		resp.Items = append(resp.Items, tagXML{Key: k, Value: tags[k]})
	}

	wire.WriteXML(w, http.StatusOK, resp)
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request, arn string) {
	var req tagsXML
	if !decodeXML(w, r, &req) {
		return
	}

	if err := h.cf.TagResource(r.Context(), arn, req.toMap()); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request, arn string) {
	var req tagKeysRequest
	if !decodeXML(w, r, &req) {
		return
	}

	if err := h.cf.UntagResource(r.Context(), arn, req.Items); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
