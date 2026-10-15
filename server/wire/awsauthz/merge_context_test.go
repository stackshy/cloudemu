package awsauthz

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeContext(t *testing.T) {
	dst := map[string]string{"aws:SourceIp": "10.0.0.1", "aws:PrincipalArn": "arn:aws:iam::1:user/u"}

	MergeContext(dst, map[string]string{
		"lambda:FunctionUrlAuthType": "NONE",
		"aws:RequestTag/team":        "a",
		"aws:ResourceTag/env":        "dev",
		"aws:TagKeys":                "team",
		"aws:SourceIp":               "1.2.3.4",
		"aws:PrincipalArn":           "arn:aws:iam::1:root",
		"aws:SecureTransport":        "true",
		"s3:prefix":                  "x",
	}, "lambda")

	assert.Equal(t, map[string]string{
		"aws:SourceIp":               "10.0.0.1",
		"aws:PrincipalArn":           "arn:aws:iam::1:user/u",
		"lambda:FunctionUrlAuthType": "NONE",
		"aws:RequestTag/team":        "a",
		"aws:ResourceTag/env":        "dev",
		"aws:TagKeys":                "team",
	}, dst)
}

func TestMergeContextNoService(t *testing.T) {
	dst := map[string]string{}
	MergeContext(dst, map[string]string{":x": "1", "lambda:Principal": "s3.amazonaws.com"}, "")
	assert.Empty(t, dst)
}
