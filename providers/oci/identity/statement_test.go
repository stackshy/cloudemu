package identity

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

func TestParseStatement(t *testing.T) {
	tests := []struct {
		name string
		text string
		want statement
	}{
		{
			name: "group in compartment",
			text: "Allow group Admins to manage all-resources in compartment dev",
			want: statement{
				Effect: effectAllow, SubjectKind: subjectGroup, Subjects: []string{"Admins"},
				Verb: verbManage, ResourceType: allResources,
				LocationKind: locationCompartment, Location: devName,
			},
		},
		{
			name: "several groups in one statement",
			text: "Allow group Admins,Auditors to read buckets in tenancy",
			want: statement{
				Effect: effectAllow, SubjectKind: subjectGroup, Subjects: []string{"Admins", "Auditors"},
				Verb: verbRead, ResourceType: "buckets", LocationKind: locationTenancy,
			},
		},
		{
			name: "group by id",
			text: "Allow group id ocid1.group.oc1..aaa to use object-family in compartment id ocid1.compartment.oc1..bbb",
			want: statement{
				Effect: effectAllow, SubjectKind: subjectGroup, Subjects: []string{"ocid1.group.oc1..aaa"},
				SubjectByID: true, Verb: verbUse, ResourceType: "object-family",
				LocationKind: locationCompartment, Location: "ocid1.compartment.oc1..bbb", LocationByID: true,
			},
		},
		{
			name: "any-user with a condition",
			text: "Allow any-user to inspect buckets in tenancy where request.region = 'iad'",
			want: statement{
				Effect: effectAllow, SubjectKind: subjectAnyUser, Verb: verbInspect,
				ResourceType: "buckets", LocationKind: locationTenancy,
				Condition: "request.region = 'iad'",
			},
		},
		{
			name: "dynamic group",
			text: "allow dynamic-group fleet to manage instances in compartment dev",
			want: statement{
				Effect: effectAllow, SubjectKind: subjectDynamicGroup, Subjects: []string{"fleet"},
				Verb: verbManage, ResourceType: "instances",
				LocationKind: locationCompartment, Location: devName,
			},
		},
		{
			name: "nested compartment path",
			text: "Allow group Admins to manage buckets in compartment dev:team",
			want: statement{
				Effect: effectAllow, SubjectKind: subjectGroup, Subjects: []string{"Admins"},
				Verb: verbManage, ResourceType: "buckets",
				LocationKind: locationCompartment, Location: "dev:team",
			},
		},
		{
			name: "cross-tenancy effects parse but do not grant",
			text: "Endorse group Admins to manage buckets in any-tenancy",
			want: statement{Effect: effectEndorse},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseStatement(tc.text)
			require.NoError(t, err)

			tc.want.Text = strings.Join(strings.Fields(tc.text), " ")
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseStatementRejects(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "empty", text: "   "},
		{name: "unknown effect", text: "Permit group Admins to manage buckets in tenancy"},
		{name: "no subject", text: "Allow to manage buckets in tenancy"},
		{name: "unknown subject", text: "Allow team Admins to manage buckets in tenancy"},
		{name: "subject names nothing", text: "Allow group to manage buckets in tenancy"},
		{name: "no verb", text: "Allow group Admins to"},
		{name: "unknown verb", text: "Allow group Admins to destroy buckets in tenancy"},
		{name: "no resource type", text: "Allow group Admins to manage in tenancy"},
		{name: "no location keyword", text: "Allow group Admins to manage buckets"},
		{name: "unknown location", text: "Allow group Admins to manage buckets in region iad"},
		{name: "compartment names nothing", text: "Allow group Admins to manage buckets in compartment"},
		{name: "nested path spaced around the colon", text: "Allow group Admins to manage buckets in compartment dev : team"},
		{name: "nested path with a leading space", text: "Allow group Admins to manage buckets in compartment dev :team"},
		{name: "nested path with a trailing space", text: "Allow group Admins to manage buckets in compartment dev: team"},
		{name: "trailing token after the compartment", text: "Allow group Admins to manage buckets in compartment dev team"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseStatement(tc.text)
			require.Error(t, err)
			assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
		})
	}
}

func TestStatementGrantsAccess(t *testing.T) {
	tests := []struct {
		name string
		text string
		req  driver.AccessRequest
		want coverage
	}{
		{
			name: "exact match",
			text: "Allow group Admins to manage buckets in tenancy",
			req:  driver.AccessRequest{Groups: []string{adminName}, Verb: verbManage, ResourceType: "buckets"},
			want: coverGranted,
		},
		{
			name: "stronger verb covers weaker",
			text: "Allow group Admins to manage buckets in tenancy",
			req:  driver.AccessRequest{Groups: []string{adminName}, Verb: verbRead, ResourceType: "buckets"},
			want: coverGranted,
		},
		{
			name: "weaker verb does not cover stronger",
			text: "Allow group Admins to read buckets in tenancy",
			req:  driver.AccessRequest{Groups: []string{adminName}, Verb: verbManage, ResourceType: "buckets"},
		},
		{
			name: "all-resources covers any type",
			text: "Allow group Admins to manage all-resources in tenancy",
			req:  driver.AccessRequest{Groups: []string{adminName}, Verb: verbUse, ResourceType: "vcns"},
			want: coverGranted,
		},
		{
			name: "family covers its members",
			text: "Allow group Admins to manage object-family in tenancy",
			req:  driver.AccessRequest{Groups: []string{adminName}, Verb: verbManage, ResourceType: "buckets"},
			want: coverGranted,
		},
		{
			name: "family does not cover another family's members",
			text: "Allow group Admins to manage object-family in tenancy",
			req:  driver.AccessRequest{Groups: []string{adminName}, Verb: verbManage, ResourceType: "instances"},
		},
		{
			name: "a family beyond the original seven covers its members",
			text: "Allow group Admins to manage functions-family in tenancy",
			req:  driver.AccessRequest{Groups: []string{adminName}, Verb: verbManage, ResourceType: "fn-function"},
			want: coverGranted,
		},
		{
			name: "an unmodeled family is undecided, not denied",
			text: "Allow group Admins to manage data-science-family in tenancy",
			req:  driver.AccessRequest{Groups: []string{adminName}, Verb: verbManage, ResourceType: "data-science-models"},
			want: coverUnknownFamily,
		},
		{
			name: "an unmodeled family still denies another subject",
			text: "Allow group Admins to manage data-science-family in tenancy",
			req:  driver.AccessRequest{Groups: []string{"Auditors"}, Verb: verbManage, ResourceType: "buckets"},
		},
		{
			name: "a resource type that is not a family denies",
			text: "Allow group Admins to manage widgets in tenancy",
			req:  driver.AccessRequest{Groups: []string{adminName}, Verb: verbManage, ResourceType: "buckets"},
		},
		{
			name: "group name matching is case-insensitive",
			text: "Allow group admins to manage buckets in tenancy",
			req:  driver.AccessRequest{Groups: []string{adminName}, Verb: verbManage, ResourceType: "buckets"},
			want: coverGranted,
		},
		{
			name: "another group does not match",
			text: "Allow group Admins to manage buckets in tenancy",
			req:  driver.AccessRequest{Groups: []string{"Auditors"}, Verb: verbManage, ResourceType: "buckets"},
		},
		{
			name: "any-user grants an authenticated user",
			text: "Allow any-user to inspect buckets in tenancy",
			req:  driver.AccessRequest{AnyUser: true, Verb: verbInspect, ResourceType: "buckets"},
			want: coverGranted,
		},
		{
			name: "dynamic group subject needs a dynamic group",
			text: "Allow dynamic-group fleet to manage instances in tenancy",
			req:  driver.AccessRequest{Groups: []string{"fleet"}, Verb: verbManage, ResourceType: "instances"},
		},
		{
			name: "dynamic group matches",
			text: "Allow dynamic-group fleet to manage instances in tenancy",
			req: driver.AccessRequest{
				DynamicGroups: []string{"fleet"}, Verb: verbManage, ResourceType: "instances",
			},
			want: coverGranted,
		},
		{
			name: "service subject never matches a user",
			text: "Allow service objectstorage to manage buckets in tenancy",
			req:  driver.AccessRequest{AnyUser: true, Verb: verbManage, ResourceType: "buckets"},
		},
		{
			name: "endorse never grants",
			text: "Endorse group Admins to manage buckets in any-tenancy",
			req:  driver.AccessRequest{Groups: []string{adminName}, Verb: verbManage, ResourceType: "buckets"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, err := parseStatement(tc.text)
			require.NoError(t, err)

			assert.Equal(t, tc.want, st.grantsAccess(&tc.req))
		})
	}
}
