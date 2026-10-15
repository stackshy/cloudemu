package blobstorage

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// serviceOp serves the account-level Blob service operations: Get/Set Blob
// Service Properties (?restype=service&comp=properties) and Get Account
// Information (?restype=account&comp=properties). The delete retention policy
// and CORS rules share their state with the ARM blobServices/default resource,
// so a value set on either surface reads back on the other.
func (h *Handler) serviceOp(w http.ResponseWriter, r *http.Request, account string) {
	cfg, ok := h.bucket.(storagedriver.BlobServiceConfig)
	if !ok || !azurearm.IsStorageServicePropertiesOp(r.URL.Query()) {
		azurearm.ServeStorageServiceOp(w, r)
		return
	}

	if account == "" {
		account = storagedriver.AzureDefaultStorageAccount
	}

	current, err := cfg.BlobServiceProperties(r.Context(), account)
	if err != nil {
		writeErr(w, err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		props := toWireServiceProperties(&current)
		azurearm.WriteStorageServiceProperties(w, &props)
	case http.MethodPut:
		in, ok := azurearm.DecodeStorageServiceProperties(w, r)
		if !ok {
			return
		}

		mergeServiceProperties(&current, &in)

		if err := cfg.SetBlobServiceProperties(r.Context(), account, current); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusAccepted)
	default:
		writeError(w, http.StatusMethodNotAllowed, "UnsupportedHttpVerb", "method not allowed")
	}
}

// toWireServiceProperties renders the stored Blob service properties as the
// data-plane document, with the defaults for what cloudemu does not store.
func toWireServiceProperties(p *storagedriver.BlobServiceProperties) azurearm.StorageServiceProperties {
	out := azurearm.DefaultStorageServiceProperties()
	out.DeleteRetentionPolicy = &azurearm.StorageRetentionPolicy{
		Enabled: p.DeleteRetentionEnabled,
		Days:    p.DeleteRetentionDays,
	}

	for _, c := range p.CORS {
		out.Cors.Rules = append(out.Cors.Rules, azurearm.StorageCorsRule{
			AllowedOrigins:  strings.Join(c.AllowedOrigins, ","),
			AllowedMethods:  strings.Join(c.AllowedMethods, ","),
			AllowedHeaders:  strings.Join(c.AllowedHeaders, ","),
			ExposedHeaders:  strings.Join(c.ExposeHeaders, ","),
			MaxAgeInSeconds: c.MaxAgeSeconds,
		})
	}

	return out
}

// mergeServiceProperties applies a Set Blob Service Properties body. As in
// real Azure, an element the request omits keeps its current value.
func mergeServiceProperties(p *storagedriver.BlobServiceProperties, in *azurearm.StorageServiceProperties) {
	if in.DeleteRetentionPolicy != nil {
		p.DeleteRetentionEnabled = in.DeleteRetentionPolicy.Enabled
		p.DeleteRetentionDays = 0

		if in.DeleteRetentionPolicy.Enabled {
			p.DeleteRetentionDays = in.DeleteRetentionPolicy.Days
		}
	}

	if in.Cors != nil {
		p.CORS = make([]storagedriver.CORSRule, 0, len(in.Cors.Rules))

		for _, c := range in.Cors.Rules {
			p.CORS = append(p.CORS, storagedriver.CORSRule{
				AllowedOrigins: splitList(c.AllowedOrigins),
				AllowedMethods: splitList(c.AllowedMethods),
				AllowedHeaders: splitList(c.AllowedHeaders),
				ExposeHeaders:  splitList(c.ExposedHeaders),
				MaxAgeSeconds:  c.MaxAgeInSeconds,
			})
		}
	}
}

// splitList splits a comma-separated CORS list, dropping empty entries.
func splitList(s string) []string {
	var out []string

	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}

	return out
}
