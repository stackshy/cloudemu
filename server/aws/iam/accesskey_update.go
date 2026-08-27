package iam

import (
	"context"
	"encoding/xml"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// accessKeyUpdater is the AWS-specific surface for changing an access key's
// status (UpdateAccessKey). It's not part of the portable driver, so the
// handler type-asserts for it.
type accessKeyUpdater interface {
	UpdateAccessKey(ctx context.Context, userName, accessKeyID, status string) error
}

type updateAccessKeyResponse struct {
	XMLName  xml.Name         `xml:"UpdateAccessKeyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

func (h *Handler) updateAccessKey(w http.ResponseWriter, r *http.Request) {
	upd, ok := h.iam.(accessKeyUpdater)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "access key updates not supported"))
		return
	}

	if err := upd.UpdateAccessKey(r.Context(),
		r.Form.Get("UserName"), r.Form.Get("AccessKeyId"), r.Form.Get("Status")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, updateAccessKeyResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
