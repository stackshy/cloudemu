package cloudformation

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

const envTemplate = `Parameters:
  Env:
    Type: String
    AllowedValues: [prod, dev]
Conditions:
  IsProd: !Equals [!Ref Env, prod]
Resources:
  Data:
    Type: Test::Bucket
    Properties:
      Name: !Sub "${AWS::StackName}-data"
  Backup:
    Type: Test::Bucket
    Condition: IsProd
    Properties:
      Name: !Sub "${AWS::StackName}-backup"
  Events:
    Type: Test::Topic
    Properties:
      Name: !If [IsProd, !Sub "${Backup}-events", !Sub "${Data}-events"]
Outputs:
  BackupName:
    Condition: IsProd
    Value: !Ref Backup
`

func logicalIDs(t *testing.T, m *Mock, stack string) []string {
	t.Helper()

	res, err := m.DescribeStackResources(context.Background(), stack)
	requireNoError(t, err)

	ids := make([]string, 0, len(res))
	for _, r := range res {
		ids = append(ids, r.LogicalID)
	}

	sort.Strings(ids)

	return ids
}

func TestConditionalResourcesPerStack(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	prod, err := m.CreateStack(ctx, &cfn.CreateStackInput{
		StackName: "prod", TemplateBody: envTemplate, Parameters: []cfn.Parameter{{Key: "Env", Value: "prod"}},
	})
	requireNoError(t, err)
	assertEqual(t, prod.Status, cfn.StatusCreateComplete, "prod status")

	dev, err := m.CreateStack(ctx, &cfn.CreateStackInput{
		StackName: "dev", TemplateBody: envTemplate, Parameters: []cfn.Parameter{{Key: "Env", Value: "dev"}},
	})
	requireNoError(t, err)
	assertEqual(t, dev.Status, cfn.StatusCreateComplete, "dev status")

	assertEqual(t, strings.Join(logicalIDs(t, m, "prod"), ","), "Backup,Data,Events", "prod resources")
	assertEqual(t, strings.Join(logicalIDs(t, m, "dev"), ","), "Data,Events", "dev resources")

	if !store.items["prod-backup-events"] || !store.items["dev-data-events"] || store.items["dev-backup"] {
		t.Fatalf("backing items = %v", store.items)
	}

	assertEqual(t, len(prod.Outputs), 1, "prod outputs")
	assertEqual(t, len(dev.Outputs), 0, "dev outputs")
}

func TestUpdateStackFlipsCondition(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{
		StackName: "s", TemplateBody: envTemplate, Parameters: []cfn.Parameter{{Key: "Env", Value: "dev"}},
	})
	requireNoError(t, err)

	up, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{
		StackName: "s", TemplateBody: envTemplate, Parameters: []cfn.Parameter{{Key: "Env", Value: "prod"}},
	})
	requireNoError(t, err)
	assertEqual(t, up.Status, cfn.StatusUpdateComplete, "update to prod")
	assertEqual(t, strings.Join(logicalIDs(t, m, "s"), ","), "Backup,Data,Events", "prod resources")

	if !store.items["s-backup"] || !store.items["s-backup-events"] || store.items["s-data-events"] {
		t.Fatalf("after prod update: %v", store.items)
	}

	up, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{
		StackName: "s", TemplateBody: envTemplate, Parameters: []cfn.Parameter{{Key: "Env", Value: "dev"}},
	})
	requireNoError(t, err)
	assertEqual(t, up.Status, cfn.StatusUpdateComplete, "update to dev")
	assertEqual(t, strings.Join(logicalIDs(t, m, "s"), ","), "Data,Events", "dev resources")

	if store.items["s-backup"] || !store.items["s-data-events"] {
		t.Fatalf("after dev update: %v", store.items)
	}
}

// TestUpdateRollbackRestoresConditionalResource fails an update that turns a
// condition off. The rollback must rebuild the old template with the old
// parameter values, so the removed resource comes back.
func TestUpdateRollbackRestoresConditionalResource(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{
		StackName: "s", TemplateBody: envTemplate, Parameters: []cfn.Parameter{{Key: "Env", Value: "prod"}},
	})
	requireNoError(t, err)

	failing := strings.Replace(envTemplate, "Outputs:", "  Boom:\n    Type: Test::Boom\nOutputs:", 1)

	up, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{
		StackName: "s", TemplateBody: failing, Parameters: []cfn.Parameter{{Key: "Env", Value: "dev"}},
	})
	requireNoError(t, err)
	assertEqual(t, up.Status, cfn.StatusUpdateRollbackComplete, "status")
	assertEqual(t, strings.Join(logicalIDs(t, m, "s"), ","), "Backup,Data,Events", "restored resources")

	if !store.items["s-backup"] {
		t.Fatalf("backup not restored: %v", store.items)
	}

	assertEqual(t, up.Parameters[0].Value, "prod", "parameters reverted")
}

func TestParameterErrorsCreateNoStack(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name   string
		params []cfn.Parameter
		msg    string
	}{
		{"not allowed", []cfn.Parameter{{Key: "Env", Value: "test"}}, "Parameter 'Env' must be one of AllowedValues"},
		{"undeclared", []cfn.Parameter{{Key: "Env", Value: "dev"}, {Key: "Zone", Value: "a"}, {Key: "Area", Value: "b"}},
			"Parameters: [Area, Zone] do not exist in the template"},
		{"missing", nil, "Parameters: [Env] must have values"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newBacking()
			m := newTestMock(store)

			_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: envTemplate, Parameters: tc.params})
			if !cerrors.IsInvalidArgument(err) || cerrors.Message(err) != tc.msg {
				t.Fatalf("err = %v, want %q", err, tc.msg)
			}

			if _, derr := m.DescribeStacks(ctx, "s"); !cerrors.IsNotFound(derr) {
				t.Fatalf("stack must not exist: %v", derr)
			}

			if len(store.items) != 0 {
				t.Fatalf("nothing may be created: %v", store.items)
			}
		})
	}
}

func TestUpdateStackBadParameterKeepsStack(t *testing.T) {
	ctx := context.Background()
	m := newTestMock(newBacking())

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{
		StackName: "s", TemplateBody: envTemplate, Parameters: []cfn.Parameter{{Key: "Env", Value: "dev"}},
	})
	requireNoError(t, err)

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{
		StackName: "s", TemplateBody: envTemplate, Parameters: []cfn.Parameter{{Key: "Env", Value: "qa"}},
	})
	if cerrors.Message(err) != "Parameter 'Env' must be one of AllowedValues" {
		t.Fatalf("err = %v", err)
	}

	st, err := m.DescribeStacks(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, st[0].Status, cfn.StatusCreateComplete, "stack untouched")
}

const ssmTemplate = `Parameters:
  Ami:
    Type: AWS::SSM::Parameter::Value<AWS::EC2::Image::Id>
    Default: /app/ami
  Zones:
    Type: AWS::SSM::Parameter::Value<List<String>>
    Default: /app/zones
  Secret:
    Type: AWS::SSM::Parameter::Value<String>
    NoEcho: true
    Default: /app/secret
Resources:
  B:
    Type: Test::Bucket
    Properties:
      Name: !Join ["-", [!Ref Ami, !Select [1, !Ref Zones], !Ref Secret]]
`

func TestSSMParameterTypes(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	params := map[string][2]string{
		"/app/ami":    {"ami-123", "String"},
		"/app/zones":  {"za,zb", "StringList"},
		"/app/secret": {"s3cr3t", "String"},
		"/app/secure": {"x", "SecureString"},
	}

	m.SetParameterReader(func(_ context.Context, name string) (value, typ string, err error) {
		p, ok := params[name]
		if !ok {
			return "", "", errors.New("not found")
		}

		return p[0], p[1], nil
	})

	st, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: ssmTemplate})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusCreateComplete, "status")

	if !store.items["ami-123-zb-s3cr3t"] {
		t.Fatalf("resolved values not used: %v", store.items)
	}

	got := map[string]cfn.Parameter{}
	for _, p := range st.Parameters {
		got[p.Key] = p
	}

	assertEqual(t, got["Ami"].Value, "/app/ami", "value is the name")
	assertEqual(t, got["Ami"].ResolvedValue, "ami-123", "resolved value")
	assertEqual(t, got["Secret"].ResolvedValue, "****", "NoEcho masks the resolved value")

	cases := map[string]string{
		"/app/missing": "Unable to fetch parameters [/app/missing] from parameter store for this account.",
		"/app/secure":  "Parameters [/app/secure] referenced by template have types not supported by CloudFormation.",
	}

	for name, msg := range cases {
		_, err := m.CreateStack(ctx, &cfn.CreateStackInput{
			StackName: "bad", TemplateBody: ssmTemplate, Parameters: []cfn.Parameter{{Key: "Ami", Value: name}},
		})
		if cerrors.Message(err) != msg {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestNotificationARNsPseudoParameter(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	body := `Resources:
  B:
    Type: Test::Bucket
    Properties:
      Name: !Join ["+", !Ref "AWS::NotificationARNs"]
`
	st, err := m.CreateStack(ctx, &cfn.CreateStackInput{
		StackName: "s", TemplateBody: body, NotificationARNs: []string{"arn:a", "arn:b"},
	})
	requireNoError(t, err)

	if !store.items["arn:a+arn:b"] {
		t.Fatalf("items = %v", store.items)
	}

	assertEqual(t, strings.Join(st.NotificationARNs, ","), "arn:a,arn:b", "stored topics")

	up, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: body})
	requireNoError(t, err)
	assertEqual(t, strings.Join(up.NotificationARNs, ","), "arn:a,arn:b", "nil keeps the topics")

	up, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: body, NotificationARNs: []string{}})
	requireNoError(t, err)
	assertEqual(t, len(up.NotificationARNs), 0, "an empty list removes them")
}

func TestSnapshotKeepsNewStackFields(t *testing.T) {
	ctx := context.Background()
	m := newTestMock(newBacking())
	m.SetParameterReader(func(context.Context, string) (value, typ string, err error) { return "v", "String", nil })

	body := "Parameters:\n  P: {Type: 'AWS::SSM::Parameter::Value<String>', Default: /p}\nResources:\n  B: {Type: Test::Bucket}\n"

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: body, NotificationARNs: []string{"arn:t"}})
	requireNoError(t, err)

	snap, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newTestMock(newBacking())
	requireNoError(t, restored.Restore(ctx, snap))

	st, err := restored.DescribeStacks(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, st[0].Parameters[0].ResolvedValue, "v", "resolved value")
	assertEqual(t, strings.Join(st[0].NotificationARNs, ","), "arn:t", "topics")
}
