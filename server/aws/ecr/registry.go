package ecr

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// The registry-scoped surfaces below are AWS-specific (Azure ACR and GCP
// Artifact Registry have no registry-level replication / pull-through cache /
// registry policy / registry scanning APIs), so they are asserted against the
// provider rather than widening the shared ContainerRegistry driver.
type (
	registryPolicyManager interface {
		PutRegistryPolicy(ctx context.Context, policyText string) (registryID, policy string, err error)
		GetRegistryPolicy(ctx context.Context) (registryID, policy string, err error)
		DeleteRegistryPolicy(ctx context.Context) (registryID, policy string, err error)
	}

	replicationManager interface {
		PutReplicationConfiguration(
			ctx context.Context, cfg crdriver.ReplicationConfiguration,
		) (crdriver.ReplicationConfiguration, error)
		DescribeRegistry(ctx context.Context) (registryID string, cfg crdriver.ReplicationConfiguration, err error)
	}

	pullThroughManager interface {
		CreatePullThroughCacheRule(
			ctx context.Context, rule *crdriver.PullThroughCacheRule,
		) (crdriver.PullThroughCacheRule, error)
		UpdatePullThroughCacheRule(
			ctx context.Context, prefix, credentialARN string,
		) (crdriver.PullThroughCacheRule, error)
		DescribePullThroughCacheRules(
			ctx context.Context, prefixes []string,
		) (rules []crdriver.PullThroughCacheRule, registryID string, err error)
		DeletePullThroughCacheRule(ctx context.Context, prefix string) (crdriver.PullThroughCacheRule, error)
	}

	registryScanManager interface {
		PutRegistryScanningConfiguration(
			ctx context.Context, cfg crdriver.RegistryScanningConfiguration,
		) (stored crdriver.RegistryScanningConfiguration, registryID string, err error)
		GetRegistryScanningConfiguration(
			ctx context.Context,
		) (registryID string, cfg crdriver.RegistryScanningConfiguration, err error)
	}

	accountSettingManager interface {
		PutAccountSetting(ctx context.Context, name, value string) (settingName, settingValue string, err error)
		GetAccountSetting(ctx context.Context, name string) (settingName, settingValue string, err error)
	}
)

// assertMgr resolves the provider to the AWS-specific manager interface T,
// writing an Unimplemented error and returning ok=false when the backing
// provider does not implement it. It centralizes the type-assertion guard the
// registry-scoped handlers share.
func assertMgr[T any](h *Handler, w http.ResponseWriter, unsupported string) (T, bool) {
	mgr, ok := any(h.registry).(T)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, unsupported))
	}

	return mgr, ok
}

// routeRegistryPolicy dispatches the registry-level permissions-policy operations.
func (h *Handler) routeRegistryPolicy(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "PutRegistryPolicy":
		h.putRegistryPolicy(w, r)
	case "GetRegistryPolicy":
		h.runRegistryPolicyOp(w, r, registryPolicyManager.GetRegistryPolicy)
	case "DeleteRegistryPolicy":
		h.runRegistryPolicyOp(w, r, registryPolicyManager.DeleteRegistryPolicy)
	default:
		return false
	}

	return true
}

// routeReplication dispatches the registry replication-configuration operations.
func (h *Handler) routeReplication(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "PutReplicationConfiguration":
		h.putReplicationConfiguration(w, r)
	case "DescribeRegistry":
		h.describeRegistry(w, r)
	default:
		return false
	}

	return true
}

// routePullThrough dispatches the pull-through cache-rule operations.
func (h *Handler) routePullThrough(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreatePullThroughCacheRule":
		h.createPullThroughCacheRule(w, r)
	case "UpdatePullThroughCacheRule":
		h.updatePullThroughCacheRule(w, r)
	case "DescribePullThroughCacheRules":
		h.describePullThroughCacheRules(w, r)
	case "DeletePullThroughCacheRule":
		h.deletePullThroughCacheRule(w, r)
	default:
		return false
	}

	return true
}

// routeRegistryScanning dispatches the registry scanning-configuration operations.
func (h *Handler) routeRegistryScanning(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "PutRegistryScanningConfiguration":
		h.putRegistryScanningConfiguration(w, r)
	case "GetRegistryScanningConfiguration":
		h.getRegistryScanningConfiguration(w, r)
	default:
		return false
	}

	return true
}

// routeAccountSetting dispatches the registry account-setting operations.
func (h *Handler) routeAccountSetting(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "PutAccountSetting":
		h.putAccountSetting(w, r)
	case "GetAccountSetting":
		h.getAccountSetting(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) putRegistryPolicy(w http.ResponseWriter, r *http.Request) {
	mgr, ok := assertMgr[registryPolicyManager](h, w, "registry policy not supported")
	if !ok {
		return
	}

	var req struct {
		PolicyText string `json:"policyText"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	registryID, policy, err := mgr.PutRegistryPolicy(r.Context(), req.PolicyText)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"registryId": registryID, "policyText": policy})
}

// runRegistryPolicyOp runs a body-less registry-policy read/delete and writes
// the resulting registryId/policyText, sharing the guard and error mapping. The
// provider tags a missing policy with RegistryPolicyNotFoundException.
func (h *Handler) runRegistryPolicyOp(
	w http.ResponseWriter, r *http.Request,
	op func(registryPolicyManager, context.Context) (registryID, policy string, err error),
) {
	mgr, ok := assertMgr[registryPolicyManager](h, w, "registry policy not supported")
	if !ok {
		return
	}

	registryID, policy, err := op(mgr, r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"registryId": registryID, "policyText": policy})
}

func (h *Handler) putReplicationConfiguration(w http.ResponseWriter, r *http.Request) {
	mgr, ok := assertMgr[replicationManager](h, w, "replication configuration not supported")
	if !ok {
		return
	}

	var req struct {
		ReplicationConfiguration replicationConfigJSON `json:"replicationConfiguration"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	stored, err := mgr.PutReplicationConfiguration(r.Context(), replicationToDriver(req.ReplicationConfiguration))
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"replicationConfiguration": replicationToJSON(stored)})
}

func (h *Handler) describeRegistry(w http.ResponseWriter, r *http.Request) {
	mgr, ok := assertMgr[replicationManager](h, w, "replication configuration not supported")
	if !ok {
		return
	}

	registryID, cfg, err := mgr.DescribeRegistry(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"registryId":               registryID,
		"replicationConfiguration": replicationToJSON(cfg),
	})
}

func (h *Handler) putRegistryScanningConfiguration(w http.ResponseWriter, r *http.Request) {
	mgr, ok := assertMgr[registryScanManager](h, w, "registry scanning configuration not supported")
	if !ok {
		return
	}

	var req scanningConfigJSON
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	stored, _, err := mgr.PutRegistryScanningConfiguration(r.Context(), scanningToDriver(req))
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"registryScanningConfiguration": scanningToJSON(stored)})
}

func (h *Handler) getRegistryScanningConfiguration(w http.ResponseWriter, r *http.Request) {
	mgr, ok := assertMgr[registryScanManager](h, w, "registry scanning configuration not supported")
	if !ok {
		return
	}

	registryID, cfg, err := mgr.GetRegistryScanningConfiguration(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"registryId":            registryID,
		"scanningConfiguration": scanningToJSON(cfg),
	})
}

func (h *Handler) putAccountSetting(w http.ResponseWriter, r *http.Request) {
	mgr, ok := assertMgr[accountSettingManager](h, w, "account settings not supported")
	if !ok {
		return
	}

	var req struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	name, value, err := mgr.PutAccountSetting(r.Context(), req.Name, req.Value)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"name": name, "value": value})
}

func (h *Handler) getAccountSetting(w http.ResponseWriter, r *http.Request) {
	mgr, ok := assertMgr[accountSettingManager](h, w, "account settings not supported")
	if !ok {
		return
	}

	var req struct {
		Name string `json:"name"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	name, value, err := mgr.GetAccountSetting(r.Context(), req.Name)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"name": name, "value": value})
}
