package iam

import (
	"context"
	"strings"
	"testing"
)

func TestCreateServiceLinkedRole(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	role, err := m.CreateServiceLinkedRole(ctx, "elasticbeanstalk.amazonaws.com", "", "beanstalk")
	requireNoError(t, err)

	assertEqual(t, "AWSServiceRoleForElasticbeanstalk", role.Name)
	assertEqual(t, "/aws-service-role/elasticbeanstalk.amazonaws.com/", role.Path)

	if !strings.Contains(role.AssumeRolePolicyDoc, "elasticbeanstalk.amazonaws.com") {
		t.Fatalf("trust policy missing service principal: %s", role.AssumeRolePolicyDoc)
	}

	// Retrievable as a normal role.
	got, err := m.GetRole(ctx, role.Name)
	requireNoError(t, err)
	assertEqual(t, role.Name, got.Name)

	// Duplicate without a suffix conflicts.
	_, err = m.CreateServiceLinkedRole(ctx, "elasticbeanstalk.amazonaws.com", "", "")
	assertError(t, err, true)

	// CustomSuffix yields a distinct name.
	suffixed, err := m.CreateServiceLinkedRole(ctx, "elasticbeanstalk.amazonaws.com", "debug", "")
	requireNoError(t, err)
	assertEqual(t, "AWSServiceRoleForElasticbeanstalk_debug", suffixed.Name)

	// Empty service name is rejected.
	_, err = m.CreateServiceLinkedRole(ctx, "", "", "")
	assertError(t, err, true)
}
