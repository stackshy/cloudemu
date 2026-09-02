package quota

import "github.com/stackshy/cloudemu/v2/config"

// awsDefaultQuotas is a representative set of well-known AWS service quotas with
// their real service-code / quota-code identifiers and default values. It is not
// exhaustive — real AWS exposes thousands of quotas — but covers the common
// services an application is likely to probe (EC2, VPC, Lambda, S3, DynamoDB,
// IAM, KMS).
//
//nolint:gochecknoglobals // static seed table for NewAWSDefaults.
var awsDefaultQuotas = []Quota{
	{
		ServiceCode: "ec2", ServiceName: "Amazon Elastic Compute Cloud (Amazon EC2)",
		QuotaCode: "L-1216C47A", QuotaName: "Running On-Demand Standard (A, C, D, H, I, M, R, T, Z) instances",
		Unit: "None", DefaultValue: 5, Adjustable: true,
	},
	{
		ServiceCode: "ec2", ServiceName: "Amazon Elastic Compute Cloud (Amazon EC2)",
		QuotaCode: "L-0263D0A3", QuotaName: "EC2-VPC Elastic IPs",
		Unit: "None", DefaultValue: 5, Adjustable: true,
	},
	{
		ServiceCode: "vpc", ServiceName: "Amazon Virtual Private Cloud (Amazon VPC)",
		QuotaCode: "L-F678F1CE", QuotaName: "VPCs per Region",
		Unit: "None", DefaultValue: 5, Adjustable: true,
	},
	{
		ServiceCode: "vpc", ServiceName: "Amazon Virtual Private Cloud (Amazon VPC)",
		QuotaCode: "L-407747CB", QuotaName: "Subnets per VPC",
		Unit: "None", DefaultValue: 200, Adjustable: true,
	},
	{
		ServiceCode: "vpc", ServiceName: "Amazon Virtual Private Cloud (Amazon VPC)",
		QuotaCode: "L-E79EC296", QuotaName: "VPC security groups per Region",
		Unit: "None", DefaultValue: 2500, Adjustable: true,
	},
	{
		ServiceCode: "lambda", ServiceName: "AWS Lambda",
		QuotaCode: "L-B99A9384", QuotaName: "Concurrent executions",
		Unit: "None", DefaultValue: 1000, Adjustable: true,
	},
	{
		ServiceCode: "s3", ServiceName: "Amazon Simple Storage Service (Amazon S3)",
		QuotaCode: "L-DC2B2D3D", QuotaName: "General purpose buckets",
		Unit: "None", DefaultValue: 100, Adjustable: true,
	},
	{
		ServiceCode: "dynamodb", ServiceName: "Amazon DynamoDB",
		QuotaCode: "L-F98FE922", QuotaName: "Maximum number of tables",
		Unit: "None", DefaultValue: 2500, Adjustable: true,
	},
	{
		ServiceCode: "iam", ServiceName: "AWS Identity and Access Management (IAM)",
		QuotaCode: "L-FE177D64", QuotaName: "Roles per account",
		Unit: "None", DefaultValue: 1000, Adjustable: true, GlobalQuota: true,
	},
	{
		ServiceCode: "kms", ServiceName: "AWS Key Management Service (AWS KMS)",
		QuotaCode: "L-C2F1777E", QuotaName: "Customer managed keys",
		Unit: "None", DefaultValue: 100000, Adjustable: true,
	},
}

// NewAWSDefaults returns a registry seeded with the representative AWS default
// quota set. Callers can further Set or SetOverride entries afterwards.
func NewAWSDefaults(clock config.Clock) *Registry {
	r := New(clock)
	for i := range awsDefaultQuotas {
		r.Set(&awsDefaultQuotas[i])
	}

	return r
}
