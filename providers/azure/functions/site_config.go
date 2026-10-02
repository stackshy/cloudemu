package functions

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// ConfigKindWeb names the config/web document, stored in SiteMeta.SiteConfig
// rather than in SiteMeta.Configs.
const ConfigKindWeb = "web"

// SetSiteConfigBlob replaces one config/{kind} document of a site, scoped like
// GetSiteMeta. kind is matched case-insensitively. A nil raw removes it.
func (m *Mock) SetSiteConfigBlob(
	_ context.Context, subscription, resourceGroup, name, kind string, raw json.RawMessage,
) (*SiteMeta, error) {
	return m.mutateSite(subscription, resourceGroup, name, func(meta *SiteMeta) {
		kind = strings.ToLower(kind)
		if kind == ConfigKindWeb {
			meta.SiteConfig = slices.Clone(raw)
			return
		}

		if raw == nil {
			delete(meta.Configs, kind)
			return
		}

		if meta.Configs == nil {
			meta.Configs = map[string]json.RawMessage{}
		}

		meta.Configs[kind] = slices.Clone(raw)
	})
}

// SetPublishingPolicy records whether basic-auth publishing is allowed for the
// "ftp" or "scm" endpoint of a site.
func (m *Mock) SetPublishingPolicy(
	_ context.Context, subscription, resourceGroup, name, kind string, allow bool,
) (*SiteMeta, error) {
	return m.mutateSite(subscription, resourceGroup, name, func(meta *SiteMeta) {
		if meta.PublishingPolicies == nil {
			meta.PublishingPolicies = map[string]bool{}
		}

		meta.PublishingPolicies[strings.ToLower(kind)] = allow
	})
}

// IsSiteNameTaken reports whether any site in any subscription or resource
// group already uses name. Web app names are global (they form the
// <name>.azurewebsites.net host) and compare case-insensitively.
func (m *Mock) IsSiteNameTaken(_ context.Context, name string) bool {
	m.sitesMu.RLock()
	defer m.sitesMu.RUnlock()

	for _, meta := range m.sites.All() {
		if strings.EqualFold(meta.Name, name) {
			return true
		}
	}

	return false
}

// mutateSite applies fn to the stored site under the write lock, scoped like
// GetSiteMeta, and returns a copy of the result.
func (m *Mock) mutateSite(subscription, resourceGroup, name string, fn func(*SiteMeta)) (*SiteMeta, error) {
	m.sitesMu.Lock()
	defer m.sitesMu.Unlock()

	meta, ok := m.sites.Get(name)
	if !ok || meta.Subscription != subscription || !strings.EqualFold(meta.ResourceGroup, resourceGroup) {
		return nil, cerrors.Newf(cerrors.NotFound, "site %s not found", name)
	}

	fn(meta)
	m.sites.Set(name, meta)

	return meta.clone(), nil
}
