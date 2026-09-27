package cloudformation

import (
	"slices"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// DeletionPolicy and UpdateReplacePolicy values.
const (
	PolicyValueDelete               = "Delete"
	PolicyValueRetain               = "Retain"
	PolicyValueRetainExceptOnCreate = "RetainExceptOnCreate"
	PolicyValueSnapshot             = "Snapshot"
)

// MaxOutputs is the number of outputs a template may declare.
const MaxOutputs = 200

// snapshotTypes are the resource types CloudFormation can snapshot, per the
// DeletionPolicy reference page. Snapshot on any other type is Delete.
var snapshotTypes = map[string]bool{ //nolint:gochecknoglobals // static lookup table
	"AWS::DocDB::DBCluster": true, "AWS::EC2::Volume": true,
	"AWS::ElastiCache::CacheCluster": true, "AWS::ElastiCache::ReplicationGroup": true,
	"AWS::Neptune::DBCluster": true, "AWS::RDS::DBCluster": true,
	"AWS::RDS::DBInstance": true, "AWS::Redshift::Cluster": true,
}

// deletionPolicies and replacePolicies are the values each attribute takes.
var (
	deletionPolicies = []string{ //nolint:gochecknoglobals // static lookup table
		PolicyValueDelete, PolicyValueRetain, PolicyValueRetainExceptOnCreate, PolicyValueSnapshot,
	}
	replacePolicies = []string{PolicyValueDelete, PolicyValueRetain, PolicyValueSnapshot} //nolint:gochecknoglobals // static lookup table
)

// effectivePolicy is the policy CloudFormation applies. An unset policy is
// Delete, and so is Snapshot on a type that has no snapshots.
func effectivePolicy(policy, rtype string) string {
	if policy == "" || (policy == PolicyValueSnapshot && !snapshotTypes[rtype]) {
		return PolicyValueDelete
	}

	return policy
}

// EffectiveDeletionPolicy is the DeletionPolicy CloudFormation applies to r.
func (r *ResourceDef) EffectiveDeletionPolicy() string {
	return effectivePolicy(r.DeletionPolicy, r.Type)
}

// EffectiveReplacePolicy is the UpdateReplacePolicy CloudFormation applies
// to the old resource when r is replaced.
func (r *ResourceDef) EffectiveReplacePolicy() string {
	return effectivePolicy(r.UpdateReplacePolicy, r.Type)
}

// KeepsOnDelete reports whether a resource whose DeletionPolicy is policy
// stays when CloudFormation deletes it. rollbackOfCreate marks the rollback
// of the operation that created the resource. retainExceptOnCreate is the
// stack operation's RetainExceptOnCreate flag, which also deletes a new
// Retain resource on that rollback.
func KeepsOnDelete(policy string, rollbackOfCreate, retainExceptOnCreate bool) bool {
	switch policy {
	case PolicyValueRetain:
		return !rollbackOfCreate || !retainExceptOnCreate
	case PolicyValueRetainExceptOnCreate:
		return !rollbackOfCreate
	default:
		return false
	}
}

// removePolicyAction is the PolicyAction of a Remove change.
func removePolicyAction(r *ResourceDef) string {
	switch r.EffectiveDeletionPolicy() {
	case PolicyValueRetain, PolicyValueRetainExceptOnCreate:
		return PolicyRetain
	case PolicyValueSnapshot:
		return PolicySnapshot
	default:
		return PolicyDelete
	}
}

// replacePolicyAction is the PolicyAction of a replacing Modify change.
func replacePolicyAction(r *ResourceDef) string {
	switch r.EffectiveReplacePolicy() {
	case PolicyValueRetain:
		return PolicyReplaceAndRetain
	case PolicyValueSnapshot:
		return PolicyReplaceAndSnapshot
	default:
		return PolicyReplaceAndDelete
	}
}

// checkPolicy checks a DeletionPolicy or UpdateReplacePolicy value. The
// "Unrecognized" text is the best known form and is not measured.
func checkPolicy(attr, value, id string, allowed []string) error {
	if value == "" || slices.Contains(allowed, value) {
		return nil
	}

	return formatErr("Unrecognized %s %s for resource %s", attr, value, id)
}

// ImportNames resolves the export names t imports with Fn::ImportValue,
// sorted and without duplicates. Call it after Prepare.
func (r *Resolver) ImportNames(t *Template) ([]string, error) {
	seen := map[string]bool{}

	var firstErr error

	for _, node := range valueNodes(t) {
		walkIntrinsics(node, func(fn string, arg any) {
			if fn != fnImportValue || firstErr != nil {
				return
			}

			name, err := r.ResolveString(arg)
			if err != nil {
				firstErr = err
				return
			}

			seen[name] = true
		})
	}

	if firstErr != nil {
		return nil, firstErr
	}

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}

	sort.Strings(out)

	return out, nil
}

// importValue resolves Fn::ImportValue from the exports the stack can see.
func (r *Resolver) importValue(arg any) (any, error) {
	name, err := r.ResolveString(arg)
	if err != nil {
		return nil, err
	}

	v, ok := r.Exports[name]
	if !ok {
		return nil, NoExportError(name)
	}

	return v, nil
}

// NoExportError is the error for an Fn::ImportValue of a missing export.
func NoExportError(name string) error {
	return cerrors.Newf(cerrors.InvalidArgument, "No export named %s found.", name)
}

// checkImportValues rejects an Fn::ImportValue whose name depends on a
// resource, another import or Fn::GetAZs, which CloudFormation cannot know
// before it creates anything.
func checkImportValues(t *Template) error {
	bad := false

	for _, node := range valueNodes(t) {
		walkIntrinsics(node, func(fn string, arg any) {
			if fn != fnImportValue || bad {
				return
			}

			refs, atts := map[string]bool{}, map[string]bool{}
			collectNames(arg, refs, atts)

			for name := range refs {
				if _, isRes := t.Resources[name]; isRes {
					bad = true
				}
			}

			walkIntrinsics(arg, func(inner string, _ any) {
				if inner == fnImportValue || inner == fnGetAZs {
					bad = true
				}
			})

			bad = bad || len(atts) > 0
		})
	}

	if bad {
		return templateErr("the attribute in Fn::ImportValue must not depend on any resources, imported values, or Fn::GetAZs")
	}

	return nil
}
