package iam

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
)

func TestCreateServiceLinkedRole(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	role, err := m.CreateServiceLinkedRole(ctx, "elasticbeanstalk.amazonaws.com", "", "beanstalk")
	requireNoError(t, err)

	// AWS's canonical name is AWSServiceRoleForElasticBeanstalk, not a
	// first-byte-capitalized "Elasticbeanstalk".
	assertEqual(t, "AWSServiceRoleForElasticBeanstalk", role.Name)
	assertEqual(t, "/aws-service-role/elasticbeanstalk.amazonaws.com/", role.Path)

	if !strings.Contains(role.AssumeRolePolicyDoc, "elasticbeanstalk.amazonaws.com") {
		t.Fatalf("trust policy missing service principal: %s", role.AssumeRolePolicyDoc)
	}

	// Retrievable as a normal role.
	got, err := m.GetRole(ctx, role.Name)
	requireNoError(t, err)
	assertEqual(t, role.Name, got.Name)

	// Duplicate without a suffix is rejected as InvalidArgument (AWS: InvalidInput),
	// never AlreadyExists — EntityAlreadyExists is not in this action's error set.
	_, err = m.CreateServiceLinkedRole(ctx, "elasticbeanstalk.amazonaws.com", "", "")
	assertError(t, err, true)
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("duplicate error = %v, want InvalidArgument", err)
	}
	if errors.IsAlreadyExists(err) {
		t.Fatalf("duplicate error must not be AlreadyExists: %v", err)
	}

	// CustomSuffix yields a distinct name.
	suffixed, err := m.CreateServiceLinkedRole(ctx, "elasticbeanstalk.amazonaws.com", "debug", "")
	requireNoError(t, err)
	assertEqual(t, "AWSServiceRoleForElasticBeanstalk_debug", suffixed.Name)

	// Empty service name is rejected.
	_, err = m.CreateServiceLinkedRole(ctx, "", "", "")
	assertError(t, err, true)
}
