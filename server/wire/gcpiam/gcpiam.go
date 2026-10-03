// Package gcpiam serves the google.iam.v1 resource verbs (getIamPolicy,
// setIamPolicy, testIamPermissions) for GCP handlers whose resources have no
// IAM model of their own. A handler resolves the resource, checks it exists,
// and hands the verb to Serve with the full resource name as the policy key.
package gcpiam

import (
	"errors"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/gcp/resourceiam"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// The google.iam.v1 resource verbs.
const (
	VerbGet  = "getIamPolicy"
	VerbSet  = "setIamPolicy"
	VerbTest = "testIamPermissions"
)

// Store keeps resource policies keyed by full resource name.
// providers/gcp/resourceiam.Mock implements it.
type Store interface {
	Get(name string) resourceiam.Policy
	Set(name string, p resourceiam.Policy, updateMask string) (resourceiam.Policy, error)
	Delete(name string)
}

// IsVerb reports whether s is one of the IAM resource verbs.
func IsVerb(s string) bool {
	return s == VerbGet || s == VerbSet || s == VerbTest
}

// SplitVerb splits an IAM verb off the last path segment, accepting both the
// colon form (".../tables/t:getIamPolicy") and compute's slash form
// (".../subnetworks/s/getIamPolicy"). A segment with no IAM verb returns
// (seg, "").
func SplitVerb(seg string) (resource, verb string) {
	if i := strings.LastIndex(seg, ":"); i >= 0 && IsVerb(seg[i+1:]) {
		return seg[:i], seg[i+1:]
	}

	if i := strings.LastIndex(seg, "/"); i >= 0 && IsVerb(seg[i+1:]) {
		return seg[:i], seg[i+1:]
	}

	return seg, ""
}

type setRequest struct {
	Policy     *resourceiam.Policy `json:"policy"`
	UpdateMask string              `json:"updateMask"`
	// Bindings and Etag are compute's legacy top-level request fields.
	Bindings []resourceiam.Binding `json:"bindings"`
	Etag     string                `json:"etag"`
}

type permissions struct {
	Permissions []string `json:"permissions,omitempty"`
}

// Serve answers one IAM verb on the resource keyed name. getIamPolicy accepts
// GET (query options) or POST (body options); the other verbs are POST only.
// The caller has already checked that the resource exists.
func Serve(w http.ResponseWriter, r *http.Request, verb, name string, s Store) {
	if r.Method != http.MethodPost && (verb != VerbGet || r.Method != http.MethodGet) {
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		return
	}

	switch verb {
	case VerbGet:
		gcprest.WriteJSON(w, http.StatusOK, s.Get(name))
	case VerbSet:
		setPolicy(w, r, name, s)
	case VerbTest:
		var req permissions
		if !gcprest.DecodeJSON(w, r, &req) {
			return
		}

		// IAM is not enforced, so the caller holds every permission it asks about.
		gcprest.WriteJSON(w, http.StatusOK, req)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unknown IAM verb "+verb)
	}
}

func setPolicy(w http.ResponseWriter, r *http.Request, name string, s Store) {
	var req setRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	pol := resourceiam.Policy{Bindings: req.Bindings, Etag: req.Etag}
	if req.Policy != nil {
		pol = *req.Policy
	}

	out, err := s.Set(name, pol, req.UpdateMask)

	switch {
	case errors.Is(err, resourceiam.ErrAborted):
		gcprest.WriteError(w, http.StatusConflict, "aborted", err.Error())
	case err != nil:
		gcprest.WriteCErr(w, err)
	default:
		gcprest.WriteJSON(w, http.StatusOK, out)
	}
}

// ComputeName returns the full resource name of the compute resource rp
// addresses ("projects/{p}/zones/{z}/disks/{d}", "projects/{p}/global/images/{i}"),
// the key its policy is stored under.
//
//nolint:gocritic // rp is a request-scoped value
func ComputeName(rp gcprest.ResourcePath) string {
	link := gcprest.SelfLink("", rp.Project, rp.Scope, rp.ScopeName, rp.ResourceType, rp.ResourceName)

	return strings.TrimPrefix(link, "/compute/v1/")
}

// ServeCompute answers the IAM verb in rp.Action on a compute resource once
// exists confirms the resource is there, so a missing resource is a 404.
//
//nolint:gocritic // rp is a request-scoped value
func ServeCompute(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, s Store, exists func() error) {
	if err := exists(); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	Serve(w, r, rp.Action, ComputeName(rp), s)
}
