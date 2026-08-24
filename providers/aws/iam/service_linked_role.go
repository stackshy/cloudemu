package iam

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// serviceLinkedRolePrefix is the fixed prefix AWS gives every service-linked
// role name (AWSServiceRoleFor<Service>).
const serviceLinkedRolePrefix = "AWSServiceRoleFor"

// CreateServiceLinkedRole creates an IAM role linked to an AWS service (IAM
// CreateServiceLinkedRole). It is AWS-only, so the wire layer reaches it via a
// type assertion on the Mock rather than the portable driver.
func (m *Mock) CreateServiceLinkedRole(
	_ context.Context, awsServiceName, customSuffix, description string,
) (*driver.RoleInfo, error) {
	if awsServiceName == "" {
		return nil, errors.Newf(errors.InvalidArgument, "AWSServiceName is required")
	}

	name := serviceLinkedRoleName(awsServiceName, customSuffix)

	if m.roles.Has(name) {
		return nil, errors.Newf(errors.AlreadyExists,
			"service-linked role %q already exists (supply a different CustomSuffix)", name)
	}

	r := &roleData{
		Name:                name,
		ID:                  idgen.GenerateID("AROA"),
		ARN:                 idgen.AWSARN("iam", "", m.opts.AccountID, "role/aws-service-role/"+awsServiceName+"/"+name),
		Path:                "/aws-service-role/" + awsServiceName + "/",
		Description:         description,
		AssumeRolePolicyDoc: serviceLinkedTrustPolicy(awsServiceName),
		MaxSessionDuration:  defaultMaxSessionDuration,
		CreatedAt:           m.opts.Clock.Now().UTC().Format(timeFormat),
		Tags:                make(map[string]string),
	}
	m.roles.Set(name, r)

	info := toRoleInfo(r)

	return &info, nil
}

// serviceLinkedRoleName derives the AWSServiceRoleFor<Service>[_suffix] name
// from a service principal such as "elasticbeanstalk.amazonaws.com".
func serviceLinkedRoleName(awsServiceName, customSuffix string) string {
	label := awsServiceName
	if idx := strings.IndexByte(label, '.'); idx >= 0 {
		label = label[:idx]
	}

	name := serviceLinkedRolePrefix + capitalize(label)

	if customSuffix != "" {
		name += "_" + customSuffix
	}

	return name
}

// capitalize upper-cases the first byte of an ASCII service label.
func capitalize(s string) string {
	if s == "" {
		return s
	}

	return strings.ToUpper(s[:1]) + s[1:]
}

// serviceLinkedTrustPolicy builds the trust policy that lets the target service
// principal assume the role.
func serviceLinkedTrustPolicy(awsServiceName string) string {
	return `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"` +
		awsServiceName + `"},"Action":"sts:AssumeRole"}]}`
}
