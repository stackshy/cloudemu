package storageaccount

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// serveBlobService serves the storage-account Blob service properties
// sub-resource: GET/PUT …/storageAccounts/{name}/blobServices/default
// (BlobServicesClient GetServiceProperties/SetServiceProperties). This is a
// distinct account-level resource from the storage account itself — a PUT
// here persists+echoes only the blob service properties and never touches
// the account's SKU/kind/tags/encryption.
func (h *Handler) serveBlobService(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	if h.blobSvc == nil {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "blob service properties not supported")
		return
	}

	if !h.bucketExists(r.Context(), rp.ResourceName) {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
			"storage account "+rp.ResourceName+" not found")
		return
	}

	if r.Method == http.MethodPut {
		var body armBlobServiceProperties
		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		if err := h.blobSvc.SetBlobServiceProperties(r.Context(), rp.ResourceName, fromARMBlobServiceProperties(&body)); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}
	}

	azurearm.WriteJSON(w, http.StatusOK, h.toARMBlobServiceProperties(r.Context(), rp))
}

// toARMBlobServiceProperties reads the stored blob service properties back
// through the driver and renders the ARM wire shape.
func (h *Handler) toARMBlobServiceProperties(ctx context.Context, rp *azurearm.ResourcePath) armBlobServiceProperties {
	props, _ := h.blobSvc.BlobServiceProperties(ctx, rp.ResourceName)

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, rp.ResourceName) +
		"/blobServices/default"

	return armBlobServiceProperties{
		ID:         id,
		Name:       "default",
		Type:       providerName + "/" + resourceType + "/blobServices",
		Properties: toARMBlobServicePropertiesProps(&props),
	}
}

func toARMBlobServicePropertiesProps(props *storagedriver.BlobServiceProperties) *armBlobServicePropertiesProps {
	versioning := props.IsVersioningEnabled
	changeFeedEnabled := props.ChangeFeedEnabled
	deleteRetentionEnabled := props.DeleteRetentionEnabled

	out := &armBlobServicePropertiesProps{
		IsVersioningEnabled:   &versioning,
		ChangeFeed:            &armChangeFeed{Enabled: &changeFeedEnabled},
		DeleteRetentionPolicy: &armDeleteRetention{Enabled: &deleteRetentionEnabled},
		Cors:                  &armCorsRules{},
	}

	if props.ChangeFeedRetentionDays > 0 {
		days := props.ChangeFeedRetentionDays
		out.ChangeFeed.RetentionInDays = &days
	}

	if props.DeleteRetentionDays > 0 {
		days := props.DeleteRetentionDays
		out.DeleteRetentionPolicy.Days = &days
	}

	for _, rule := range props.CORS {
		out.Cors.CorsRules = append(out.Cors.CorsRules, armCorsRule{
			AllowedOrigins:  rule.AllowedOrigins,
			AllowedMethods:  rule.AllowedMethods,
			AllowedHeaders:  rule.AllowedHeaders,
			ExposedHeaders:  rule.ExposeHeaders,
			MaxAgeInSeconds: rule.MaxAgeSeconds,
		})
	}

	return out
}

// fromARMBlobServiceProperties maps a PUT request body to the driver's
// BlobServiceProperties. A missing element in the request clears the
// corresponding setting (Azure's Set Blob Service Properties takes a
// complete document each call, not a merge patch).
func fromARMBlobServiceProperties(body *armBlobServiceProperties) storagedriver.BlobServiceProperties {
	var props storagedriver.BlobServiceProperties
	if body.Properties == nil {
		return props
	}

	p := body.Properties

	if p.IsVersioningEnabled != nil {
		props.IsVersioningEnabled = *p.IsVersioningEnabled
	}

	applyARMChangeFeed(&props, p.ChangeFeed)
	applyARMDeleteRetention(&props, p.DeleteRetentionPolicy)
	applyARMCors(&props, p.Cors)

	return props
}

func applyARMChangeFeed(props *storagedriver.BlobServiceProperties, cf *armChangeFeed) {
	if cf == nil {
		return
	}

	if cf.Enabled != nil {
		props.ChangeFeedEnabled = *cf.Enabled
	}

	if cf.RetentionInDays != nil {
		props.ChangeFeedRetentionDays = *cf.RetentionInDays
	}
}

func applyARMDeleteRetention(props *storagedriver.BlobServiceProperties, dr *armDeleteRetention) {
	if dr == nil {
		return
	}

	if dr.Enabled != nil {
		props.DeleteRetentionEnabled = *dr.Enabled
	}

	if dr.Days != nil {
		props.DeleteRetentionDays = *dr.Days
	}
}

func applyARMCors(props *storagedriver.BlobServiceProperties, cors *armCorsRules) {
	if cors == nil {
		return
	}

	for _, rule := range cors.CorsRules {
		props.CORS = append(props.CORS, storagedriver.CORSRule{
			AllowedOrigins: rule.AllowedOrigins,
			AllowedMethods: rule.AllowedMethods,
			AllowedHeaders: rule.AllowedHeaders,
			ExposeHeaders:  rule.ExposedHeaders,
			MaxAgeSeconds:  rule.MaxAgeInSeconds,
		})
	}
}
