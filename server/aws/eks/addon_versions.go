package eks

import (
	"net/http"

	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

// Add-on catalog JSON shapes, as the aws-sdk-go-v2 EKS client reads them.

type compatibilityJSON struct {
	ClusterVersion   string   `json:"clusterVersion"`
	PlatformVersions []string `json:"platformVersions"`
	DefaultVersion   bool     `json:"defaultVersion"`
}

type addonVersionInfoJSON struct {
	AddonVersion           string              `json:"addonVersion"`
	Architecture           []string            `json:"architecture"`
	ComputeTypes           []string            `json:"computeTypes"`
	Compatibilities        []compatibilityJSON `json:"compatibilities"`
	RequiresConfiguration  bool                `json:"requiresConfiguration"`
	RequiresIamPermissions bool                `json:"requiresIamPermissions"`
}

type addonInfoJSON struct {
	AddonName        string                 `json:"addonName"`
	Type             string                 `json:"type"`
	Owner            string                 `json:"owner"`
	Publisher        string                 `json:"publisher"`
	DefaultNamespace string                 `json:"defaultNamespace"`
	AddonVersions    []addonVersionInfoJSON `json:"addonVersions"`
}

type describeAddonVersionsResponse struct {
	Addons    []addonInfoJSON `json:"addons"`
	NextToken string          `json:"nextToken,omitempty"`
}

type podIdentityConfigurationJSON struct {
	ServiceAccount             string   `json:"serviceAccount"`
	RecommendedManagedPolicies []string `json:"recommendedManagedPolicies"`
}

type describeAddonConfigurationResponse struct {
	AddonName                string                         `json:"addonName"`
	AddonVersion             string                         `json:"addonVersion"`
	ConfigurationSchema      string                         `json:"configurationSchema"`
	PodIdentityConfiguration []podIdentityConfigurationJSON `json:"podIdentityConfiguration"`
}

// describeAddonVersions serves GET /addons/supported-versions.
func (h *Handler) describeAddonVersions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	q := r.URL.Query()

	addons, err := h.eks.DescribeAddonVersions(r.Context(), eksdriver.AddonVersionFilter{
		AddonName:         q.Get("addonName"),
		KubernetesVersion: q.Get("kubernetesVersion"),
		Types:             q["types"],
		Publishers:        q["publishers"],
		Owners:            q["owners"],
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	page, next, ok := paginateItems(w, r, addons, func(a *eksdriver.AddonInfo) string { return a.AddonName })
	if !ok {
		return
	}

	out := make([]addonInfoJSON, 0, len(page))
	for i := range page {
		out = append(out, toAddonInfoJSON(&page[i]))
	}

	writeJSON(w, describeAddonVersionsResponse{Addons: out, NextToken: next})
}

// describeAddonConfiguration serves GET /addons/configuration-schemas.
func (h *Handler) describeAddonConfiguration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	q := r.URL.Query()

	cfg, err := h.eks.DescribeAddonConfiguration(r.Context(), q.Get("addonName"), q.Get("addonVersion"))
	if err != nil {
		writeErr(w, err)

		return
	}

	pods := make([]podIdentityConfigurationJSON, 0, len(cfg.PodIdentityConfiguration))
	for _, p := range cfg.PodIdentityConfiguration {
		pods = append(pods, podIdentityConfigurationJSON{
			ServiceAccount:             p.ServiceAccount,
			RecommendedManagedPolicies: p.RecommendedManagedPolicies,
		})
	}

	writeJSON(w, describeAddonConfigurationResponse{
		AddonName:                cfg.AddonName,
		AddonVersion:             cfg.AddonVersion,
		ConfigurationSchema:      cfg.ConfigurationSchema,
		PodIdentityConfiguration: pods,
	})
}

func toAddonInfoJSON(a *eksdriver.AddonInfo) addonInfoJSON {
	out := addonInfoJSON{
		AddonName:        a.AddonName,
		Type:             a.Type,
		Owner:            a.Owner,
		Publisher:        a.Publisher,
		DefaultNamespace: a.DefaultNamespace,
		AddonVersions:    make([]addonVersionInfoJSON, 0, len(a.AddonVersions)),
	}

	for _, v := range a.AddonVersions {
		compat := make([]compatibilityJSON, 0, len(v.Compatibilities))
		for _, c := range v.Compatibilities {
			compat = append(compat, compatibilityJSON{
				ClusterVersion:   c.ClusterVersion,
				PlatformVersions: c.PlatformVersions,
				DefaultVersion:   c.DefaultVersion,
			})
		}

		out.AddonVersions = append(out.AddonVersions, addonVersionInfoJSON{
			AddonVersion:           v.AddonVersion,
			Architecture:           v.Architecture,
			ComputeTypes:           v.ComputeTypes,
			Compatibilities:        compat,
			RequiresConfiguration:  v.RequiresConfiguration,
			RequiresIamPermissions: v.RequiresIamPermissions,
		})
	}

	return out
}
