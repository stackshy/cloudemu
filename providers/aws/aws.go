// Package aws provides AWS mock provider factories.
package aws

import (
	"github.com/NitinKumar004/cloudemu/config"
	"github.com/NitinKumar004/cloudemu/providers/aws/awsiam"
	"github.com/NitinKumar004/cloudemu/providers/aws/cloudwatch"
	"github.com/NitinKumar004/cloudemu/providers/aws/dynamodb"
	"github.com/NitinKumar004/cloudemu/providers/aws/ec2"
	"github.com/NitinKumar004/cloudemu/providers/aws/elb"
	"github.com/NitinKumar004/cloudemu/providers/aws/lambda"
	"github.com/NitinKumar004/cloudemu/providers/aws/route53"
	"github.com/NitinKumar004/cloudemu/providers/aws/s3"
	"github.com/NitinKumar004/cloudemu/providers/aws/sqs"
	"github.com/NitinKumar004/cloudemu/providers/aws/vpc"
)

// Provider holds all AWS mock services.
type Provider struct {
	S3         *s3.Mock
	EC2        *ec2.Mock
	DynamoDB   *dynamodb.Mock
	Lambda     *lambda.Mock
	VPC        *vpc.Mock
	CloudWatch *cloudwatch.Mock
	IAM        *awsiam.Mock
	Route53    *route53.Mock
	ELB        *elb.Mock
	SQS        *sqs.Mock
}

// New creates a new AWS provider with all mock services.
func New(opts ...config.Option) *Provider {
	o := config.NewOptions(opts...)
	return &Provider{
		S3:         s3.New(o),
		EC2:        ec2.New(o),
		DynamoDB:   dynamodb.New(o),
		Lambda:     lambda.New(o),
		VPC:        vpc.New(o),
		CloudWatch: cloudwatch.New(o),
		IAM:        awsiam.New(o),
		Route53:    route53.New(o),
		ELB:        elb.New(o),
		SQS:        sqs.New(o),
	}
}
