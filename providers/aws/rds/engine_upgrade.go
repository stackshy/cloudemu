package rds

import (
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// errGroupFamilyMismatch is the InvalidParameterCombination RDS returns when a
// DB parameter group of one family is put on an instance whose engine version
// needs another (an explicit postgres15 group on a 16.2 upgrade).
func errGroupFamilyMismatch(group, groupFamily, want string) error {
	return cerrors.Newf(cerrors.InvalidArgument,
		"The parameter group %s with DBParameterGroupFamily %s can't be used for this instance. "+
			"Use a parameter group with DBParameterGroupFamily %s.", group, groupFamily, want)
}

// errCustomGroupNeedsReplacement is the InvalidParameterCombination RDS
// returns for a major version upgrade of an instance on a custom group of the
// old family when the request names no new group.
func errCustomGroupNeedsReplacement(group string) error {
	return cerrors.Newf(cerrors.InvalidArgument,
		"The current DB instance parameter group %s is custom. You must explicitly specify a new DB instance "+
			"parameter group, either default or custom, for the engine version upgrade.", group)
}

// errInstanceGroupNeedsMajorUpgrade rejects ModifyDBCluster
// DBInstanceParameterGroupName outside a major version upgrade, the only case
// RDS accepts it in.
func errInstanceGroupNeedsMajorUpgrade() error {
	return cerrors.New(cerrors.InvalidArgument,
		"DBInstanceParameterGroupName is valid in combination with AllowMajorVersionUpgrade for a major version upgrade only.")
}

// checkGroupForVersion checks that an instance of engine moving to version
// ends on a DB parameter group of that version's family. explicit is the group
// the request names ("" for none) and must match the family; with none, a
// default group follows the upgrade (upgradedDefaultGroup) while a custom
// group of another family is rejected, as RDS does. Groups of unknown family
// and engines with no known family are not checked. The caller holds m.mu.
func (m *Mock) checkGroupForVersion(engine, version, current, explicit string) error {
	want := parameterGroupFamily(engine, version)
	if want == "" {
		return nil
	}

	if explicit != "" {
		if family, ok := m.dbParameterGroupFamily(explicit); ok && family != want {
			return errGroupFamilyMismatch(explicit, family, want)
		}

		return nil
	}

	if _, isDefault := defaultGroupFamily(current); isDefault {
		return nil
	}

	if family, ok := m.dbParameterGroupFamily(current); ok && family != want {
		return errCustomGroupNeedsReplacement(current)
	}

	return nil
}

// checkInstanceModifyGroup validates the parameter group a ModifyDBInstance
// request leaves the instance on against the engine version it asks for (or
// the current version when it asks for none). The caller holds m.mu.
func (m *Mock) checkInstanceModifyGroup(inst *rdsdriver.Instance, input *rdsdriver.ModifyInstanceInput) error {
	if input.EngineVersion == "" && input.DBParameterGroupName == "" {
		return nil
	}

	version := input.EngineVersion
	if version == "" {
		version = inst.EngineVersion
	}

	return m.checkGroupForVersion(inst.Engine, version, inst.DBParameterGroupName, input.DBParameterGroupName)
}

// isMajorUpgrade reports whether moving engine from version from to version
// to changes the parameter group family, which is how RDS tells a major
// version upgrade from a minor one.
func isMajorUpgrade(engine, from, to string) bool {
	if to == "" {
		return false
	}

	next := parameterGroupFamily(engine, to)

	return next != "" && next != parameterGroupFamily(engine, from)
}

// clusterMemberUpgrades returns the member instances of cluster as they stand
// after the ModifyDBCluster request: in Aurora every instance runs the cluster
// version, so an engine version change moves each member to it, and on a major
// upgrade each member moves to DBInstanceParameterGroupName when given, or
// from a default group to the new version's default group. A custom member
// group of the old family with no DBInstanceParameterGroupName fails the
// request, as in RDS. Nothing is stored; the caller holds m.mu.
func (m *Mock) clusterMemberUpgrades(
	cluster *rdsdriver.Cluster, input *rdsdriver.ModifyInstanceInput,
) ([]rdsdriver.Instance, error) {
	major := isMajorUpgrade(cluster.Engine, cluster.EngineVersion, input.EngineVersion)
	if input.DBInstanceParameterGroupName != "" && !major {
		return nil, errInstanceGroupNeedsMajorUpgrade()
	}

	if input.EngineVersion == "" {
		return nil, nil
	}

	out := make([]rdsdriver.Instance, 0, len(cluster.Members))

	for _, id := range cluster.Members {
		inst, ok := m.instances.Get(id)
		if !ok {
			continue
		}

		if major {
			if err := m.upgradeMemberGroup(&inst, input); err != nil {
				return nil, err
			}
		}

		inst.EngineVersion = input.EngineVersion
		out = append(out, inst)
	}

	return out, nil
}

// upgradeMemberGroup moves one cluster member's DB parameter group for a major
// version upgrade (see clusterMemberUpgrades). The caller holds m.mu.
func (m *Mock) upgradeMemberGroup(inst *rdsdriver.Instance, input *rdsdriver.ModifyInstanceInput) error {
	explicit := input.DBInstanceParameterGroupName
	if err := m.checkGroupForVersion(inst.Engine, input.EngineVersion, inst.DBParameterGroupName, explicit); err != nil {
		return err
	}

	if explicit != "" {
		inst.DBParameterGroupName = explicit
		return nil
	}

	inst.DBParameterGroupName = upgradedDefaultGroup(inst.DBParameterGroupName, inst.Engine, input.EngineVersion)

	return nil
}
