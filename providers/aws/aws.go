// Package aws provides AWS mock provider factories.
package aws

import (
	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/providers/aws/awsiam"
	"github.com/stackshy/cloudemu/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/providers/aws/cloudwatchlogs"
	"github.com/stackshy/cloudemu/providers/aws/dynamodb"
	"github.com/stackshy/cloudemu/providers/aws/ec2"
	"github.com/stackshy/cloudemu/providers/aws/elasticache"
	"github.com/stackshy/cloudemu/providers/aws/elb"
	"github.com/stackshy/cloudemu/providers/aws/lambda"
	"github.com/stackshy/cloudemu/providers/aws/route53"
	"github.com/stackshy/cloudemu/providers/aws/s3"
	"github.com/stackshy/cloudemu/providers/aws/secretsmanager"
	"github.com/stackshy/cloudemu/providers/aws/sns"
	"github.com/stackshy/cloudemu/providers/aws/sqs"
	"github.com/stackshy/cloudemu/providers/aws/vpc"
)

// Provider holds all AWS mock services.
type Provider struct {
	S3             *s3.Mock
	EC2            *ec2.Mock
	DynamoDB       *dynamodb.Mock
	Lambda         *lambda.Mock
	VPC            *vpc.Mock
	CloudWatch     *cloudwatch.Mock
	IAM            *awsiam.Mock
	Route53        *route53.Mock
	ELB            *elb.Mock
	SQS            *sqs.Mock
	ElastiCache    *elasticache.Mock
	SecretsManager *secretsmanager.Mock
	CloudWatchLogs *cloudwatchlogs.Mock
	SNS            *sns.Mock
}

// New creates a new AWS provider with all mock services.
func New(opts ...config.Option) *Provider {
	o := config.NewOptions(opts...)
	p := &Provider{
		S3:             s3.New(o),
		EC2:            ec2.New(o),
		DynamoDB:       dynamodb.New(o),
		Lambda:         lambda.New(o),
		VPC:            vpc.New(o),
		CloudWatch:     cloudwatch.New(o),
		IAM:            awsiam.New(o),
		Route53:        route53.New(o),
		ELB:            elb.New(o),
		SQS:            sqs.New(o),
		ElastiCache:    elasticache.New(o),
		SecretsManager: secretsmanager.New(o),
		CloudWatchLogs: cloudwatchlogs.New(o),
		SNS:            sns.New(o),
	}
	p.EC2.SetMonitoring(p.CloudWatch)

	return p
}
