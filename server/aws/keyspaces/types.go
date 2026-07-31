package keyspaces

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/keyspaces/types"

	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

func toWireSchema(s *ksdriver.SchemaDefinition) *types.SchemaDefinition {
	out := &types.SchemaDefinition{}

	for _, c := range s.AllColumns {
		out.AllColumns = append(out.AllColumns, types.ColumnDefinition{Name: aws.String(c.Name), Type: aws.String(c.Type)})
	}

	for _, p := range s.PartitionKeys {
		out.PartitionKeys = append(out.PartitionKeys, types.PartitionKey{Name: aws.String(p.Name)})
	}

	for _, c := range s.ClusteringKeys {
		out.ClusteringKeys = append(out.ClusteringKeys,
			types.ClusteringKey{Name: aws.String(c.Name), OrderBy: types.SortOrder(c.OrderBy)})
	}

	for _, sc := range s.StaticColumns {
		out.StaticColumns = append(out.StaticColumns, types.StaticColumn{Name: aws.String(sc.Name)})
	}

	return out
}

func toWireCapacity(c ksdriver.CapacitySpecification) *types.CapacitySpecificationSummary {
	return &types.CapacitySpecificationSummary{
		ThroughputMode:     types.ThroughputMode(c.ThroughputMode),
		ReadCapacityUnits:  aws.Int64(c.ReadCapacityUnits),
		WriteCapacityUnits: aws.Int64(c.WriteCapacityUnits),
	}
}

func toWireEncryption(e ksdriver.EncryptionSpecification) *types.EncryptionSpecification {
	out := &types.EncryptionSpecification{Type: types.EncryptionType(e.Type)}
	if e.KmsKeyIdentifier != "" {
		out.KmsKeyIdentifier = aws.String(e.KmsKeyIdentifier)
	}

	return out
}

func toWireAutoScalingSettings(s *ksdriver.AutoScalingSettings) *types.AutoScalingSettings {
	if s == nil {
		return nil
	}

	return &types.AutoScalingSettings{
		AutoScalingDisabled: s.AutoScalingDisabled,
		MinimumUnits:        aws.Int64(s.MinimumUnits),
		MaximumUnits:        aws.Int64(s.MaximumUnits),
		ScalingPolicy: &types.AutoScalingPolicy{
			TargetTrackingScalingPolicyConfiguration: &types.TargetTrackingScalingPolicyConfiguration{
				TargetValue:      s.TargetValue,
				DisableScaleIn:   s.DisableScaleIn,
				ScaleInCooldown:  int32(s.ScaleInCooldown),  //nolint:gosec // mock cooldown seconds never overflow int32.
				ScaleOutCooldown: int32(s.ScaleOutCooldown), //nolint:gosec // mock cooldown seconds never overflow int32.
			},
		},
	}
}

func toWireAutoScaling(a *ksdriver.AutoScalingSpecification) *types.AutoScalingSpecification {
	if a == nil {
		return nil
	}

	return &types.AutoScalingSpecification{
		ReadCapacityAutoScaling:  toWireAutoScalingSettings(a.Read),
		WriteCapacityAutoScaling: toWireAutoScalingSettings(a.Write),
	}
}

func toWireKeyspaceSummary(k *ksdriver.Keyspace) types.KeyspaceSummary {
	return types.KeyspaceSummary{
		KeyspaceName:        aws.String(k.Name),
		ResourceArn:         aws.String(k.ARN),
		ReplicationStrategy: types.Rs(k.ReplicationStrategy),
		ReplicationRegions:  append([]string(nil), k.ReplicationRegions...),
	}
}

func toWireTableSummary(t *ksdriver.Table) types.TableSummary {
	return types.TableSummary{
		KeyspaceName: aws.String(t.KeyspaceName),
		TableName:    aws.String(t.Name),
		ResourceArn:  aws.String(t.ARN),
	}
}

func toWireTags(tags []ksdriver.Tag) []types.Tag {
	out := make([]types.Tag, 0, len(tags))
	for i := range tags {
		out = append(out, types.Tag{Key: aws.String(tags[i].Key), Value: aws.String(tags[i].Value)})
	}

	return out
}

func fromWireSchema(s *types.SchemaDefinition) ksdriver.SchemaDefinition {
	out := ksdriver.SchemaDefinition{}
	if s == nil {
		return out
	}

	for _, c := range s.AllColumns {
		out.AllColumns = append(out.AllColumns,
			ksdriver.ColumnDefinition{Name: aws.ToString(c.Name), Type: aws.ToString(c.Type)})
	}

	for _, p := range s.PartitionKeys {
		out.PartitionKeys = append(out.PartitionKeys, ksdriver.PartitionKey{Name: aws.ToString(p.Name)})
	}

	for _, c := range s.ClusteringKeys {
		out.ClusteringKeys = append(out.ClusteringKeys,
			ksdriver.ClusteringKey{Name: aws.ToString(c.Name), OrderBy: string(c.OrderBy)})
	}

	for _, sc := range s.StaticColumns {
		out.StaticColumns = append(out.StaticColumns, ksdriver.StaticColumn{Name: aws.ToString(sc.Name)})
	}

	return out
}

func fromWireCapacity(c *types.CapacitySpecification) ksdriver.CapacitySpecification {
	if c == nil {
		return ksdriver.CapacitySpecification{}
	}

	return ksdriver.CapacitySpecification{
		ThroughputMode:     string(c.ThroughputMode),
		ReadCapacityUnits:  aws.ToInt64(c.ReadCapacityUnits),
		WriteCapacityUnits: aws.ToInt64(c.WriteCapacityUnits),
	}
}

func fromWireEncryption(e *types.EncryptionSpecification) ksdriver.EncryptionSpecification {
	if e == nil {
		return ksdriver.EncryptionSpecification{}
	}

	return ksdriver.EncryptionSpecification{Type: string(e.Type), KmsKeyIdentifier: aws.ToString(e.KmsKeyIdentifier)}
}

func fromWireAutoScalingSettings(s *types.AutoScalingSettings) *ksdriver.AutoScalingSettings {
	if s == nil {
		return nil
	}

	out := &ksdriver.AutoScalingSettings{
		AutoScalingDisabled: s.AutoScalingDisabled,
		MinimumUnits:        aws.ToInt64(s.MinimumUnits),
		MaximumUnits:        aws.ToInt64(s.MaximumUnits),
	}

	if s.ScalingPolicy != nil && s.ScalingPolicy.TargetTrackingScalingPolicyConfiguration != nil {
		cfg := s.ScalingPolicy.TargetTrackingScalingPolicyConfiguration
		out.TargetValue = cfg.TargetValue
		out.DisableScaleIn = cfg.DisableScaleIn
		out.ScaleInCooldown = int(cfg.ScaleInCooldown)
		out.ScaleOutCooldown = int(cfg.ScaleOutCooldown)
	}

	return out
}

func fromWireAutoScaling(a *types.AutoScalingSpecification) *ksdriver.AutoScalingSpecification {
	if a == nil {
		return nil
	}

	return &ksdriver.AutoScalingSpecification{
		Read:  fromWireAutoScalingSettings(a.ReadCapacityAutoScaling),
		Write: fromWireAutoScalingSettings(a.WriteCapacityAutoScaling),
	}
}

func replicaRegions(specs []types.ReplicaSpecification) []string {
	out := make([]string, 0, len(specs))
	for i := range specs {
		out = append(out, aws.ToString(specs[i].Region))
	}

	return out
}

func tagMap(tags []types.Tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	out := make(map[string]string, len(tags))
	for i := range tags {
		out[aws.ToString(tags[i].Key)] = aws.ToString(tags[i].Value)
	}

	return out
}
