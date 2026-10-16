package networkfirewall

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

func TestCreateFirewall_Validation(t *testing.T) {
	m := newMock()
	pol := seedPolicy(t, m, "pol-1")

	base := func() nfdriver.CreateFirewallConfig {
		return nfdriver.CreateFirewallConfig{Name: "fw", PolicyARN: pol.ARN, VPCID: "vpc-1a"}
	}

	tags := map[string]string{}
	for i := 0; i < 201; i++ {
		tags[strings.Repeat("k", i+1)] = "v"
	}

	cases := map[string]func(*nfdriver.CreateFirewallConfig){
		"no vpc id":        func(c *nfdriver.CreateFirewallConfig) { c.VPCID = "" },
		"bad vpc id":       func(c *nfdriver.CreateFirewallConfig) { c.VPCID = "vpc-XYZ" },
		"bad name":         func(c *nfdriver.CreateFirewallConfig) { c.Name = "fw_1" },
		"long name":        func(c *nfdriver.CreateFirewallConfig) { c.Name = strings.Repeat("a", 129) },
		"long description": func(c *nfdriver.CreateFirewallConfig) { c.Description = strings.Repeat("d", 513) },
		"too many tags":    func(c *nfdriver.CreateFirewallConfig) { c.Tags = tags },
		"no policy":        func(c *nfdriver.CreateFirewallConfig) { c.PolicyARN = "" },
		"unknown policy": func(c *nfdriver.CreateFirewallConfig) {
			c.PolicyARN = "arn:aws:network-firewall:us-east-1:000000000000:firewall-policy/x"
		},
		"malformed policy":  func(c *nfdriver.CreateFirewallConfig) { c.PolicyARN = "not-an-arn" },
		"bad analysis type": func(c *nfdriver.CreateFirewallConfig) { c.EnabledAnalysisTypes = []string{"BOGUS"} },
		"vpc with az":       func(c *nfdriver.CreateFirewallConfig) { c.AvailabilityZones = []string{"us-east-1a"} },
		"tgw with vpc": func(c *nfdriver.CreateFirewallConfig) {
			c.TransitGatewayID = "tgw-1"
			c.AvailabilityZones = []string{"a"}
		},
		"tgw without azs": func(c *nfdriver.CreateFirewallConfig) { c.TransitGatewayID = "tgw-1"; c.VPCID = "" },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := base()
			mutate(&cfg)

			_, err := m.CreateFirewall(bg, cfg)
			assert.True(t, cerrors.IsInvalidArgument(err), "want InvalidArgument, got %v", err)
		})
	}

	_, err := m.CreateFirewall(bg, base())
	require.NoError(t, err)
}

func TestCreateFirewall_TGWWithoutVPC_OK(t *testing.T) {
	m := newMock()

	fw := tgwFirewall(t, m, "fw-tgw", false)
	assert.Empty(t, fw.VPCID)
	assert.Equal(t, "tgw-0abc", fw.TransitGatewayID)
}

func TestLogging_Validation(t *testing.T) {
	m := newMock()
	seedStack(t, m)

	bad := map[string][]nfdriver.LogDestinationConfig{
		"bogus log type":       {s3Dest("BOGUS", "b")},
		"bogus dest type":      {logDest("FLOW", "Kafka", map[string]string{"x": "y"})},
		"missing bucket":       {logDest("FLOW", "S3", map[string]string{"prefix": "p"})},
		"missing log group":    {logDest("FLOW", "CloudWatchLogs", map[string]string{})},
		"missing stream":       {logDest("FLOW", "KinesisDataFirehose", nil)},
		"unknown key":          {logDest("FLOW", "S3", map[string]string{"bucketName": "b", "oops": "1"})},
		"duplicate log type":   {s3Dest("FLOW", "a"), s3Dest("FLOW", "b")},
		"more than three":      {s3Dest("FLOW", "b"), s3Dest("ALERT", "b"), s3Dest("TLS", "b"), s3Dest("TLS", "c")},
		"prefix on cloudwatch": {logDest("FLOW", "CloudWatchLogs", map[string]string{"logGroup": "g", "prefix": "p"})},
	}

	for name, dests := range bad {
		t.Run(name, func(t *testing.T) {
			err := m.UpdateLoggingConfiguration(bg, "fw-1", &nfdriver.LoggingConfiguration{Destinations: dests})
			assert.True(t, cerrors.IsInvalidArgument(err), "want InvalidArgument, got %v", err)
		})
	}

	logs, err := m.DescribeLoggingConfiguration(bg, "fw-1")
	require.NoError(t, err)
	assert.Empty(t, logs.Destinations, "rejected updates store nothing")
}

func TestLogging_OneChangePerCall(t *testing.T) {
	m := newMock()
	seedStack(t, m)

	two := &nfdriver.LoggingConfiguration{Destinations: []nfdriver.LogDestinationConfig{s3Dest("FLOW", "b"), s3Dest("ALERT", "b")}}
	err := m.UpdateLoggingConfiguration(bg, "fw-1", two)
	assert.True(t, cerrors.IsInvalidArgument(err), "adding two destinations at once: %v", err)

	one := &nfdriver.LoggingConfiguration{Destinations: []nfdriver.LogDestinationConfig{s3Dest("FLOW", "b")}}
	require.NoError(t, m.UpdateLoggingConfiguration(bg, "fw-1", one))

	// Changing the destination TYPE of an existing entry is a remove plus an add.
	typeChange := &nfdriver.LoggingConfiguration{Destinations: []nfdriver.LogDestinationConfig{
		logDest("FLOW", "CloudWatchLogs", map[string]string{"logGroup": "g"}),
	}}
	err = m.UpdateLoggingConfiguration(bg, "fw-1", typeChange)
	assert.True(t, cerrors.IsInvalidArgument(err), "type change: %v", err)

	// Replacing one entry with another log type is also two changes.
	swap := &nfdriver.LoggingConfiguration{Destinations: []nfdriver.LogDestinationConfig{s3Dest("ALERT", "b")}}
	err = m.UpdateLoggingConfiguration(bg, "fw-1", swap)
	assert.True(t, cerrors.IsInvalidArgument(err), "swap: %v", err)
}

func TestLogging_Add_Remove_ChangeDestination_OmitDisables(t *testing.T) {
	m := newMock()
	seedStack(t, m)

	update := func(dests ...nfdriver.LogDestinationConfig) error {
		return m.UpdateLoggingConfiguration(bg, "fw-1", &nfdriver.LoggingConfiguration{Destinations: dests})
	}

	require.NoError(t, update(s3Dest("FLOW", "b1")))
	require.NoError(t, update(s3Dest("FLOW", "b1"), s3Dest("ALERT", "b1")))
	require.NoError(t, update(s3Dest("FLOW", "b2"), s3Dest("ALERT", "b1")), "change one destination")
	require.NoError(t, update(s3Dest("FLOW", "b2")), "remove one")

	logs, _ := m.DescribeLoggingConfiguration(bg, "fw-1")
	require.Len(t, logs.Destinations, 1)
	assert.Equal(t, "b2", logs.Destinations[0].LogDestination["bucketName"])

	dash := true
	require.NoError(t, m.UpdateLoggingConfiguration(bg, "fw-1", &nfdriver.LoggingConfiguration{
		Destinations: []nfdriver.LogDestinationConfig{s3Dest("FLOW", "b2")}, EnableMonitoringDashboard: &dash,
	}))

	logs, _ = m.DescribeLoggingConfiguration(bg, "fw-1")
	require.NotNil(t, logs.EnableMonitoringDashboard)
	assert.True(t, *logs.EnableMonitoringDashboard)

	// An omitted configuration disables logging (the dashboard flag is kept).
	require.NoError(t, m.UpdateLoggingConfiguration(bg, "fw-1", nil))

	logs, _ = m.DescribeLoggingConfiguration(bg, "fw-1")
	assert.Empty(t, logs.Destinations)

	err := m.UpdateLoggingConfiguration(bg, "ghost", nil)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestCreateRuleGroup_Validation(t *testing.T) {
	m := newMock()

	rules := rulesString(`alert tcp any any -> any any (msg:"x"; sid:1;)`)
	both := map[string]nfdriver.CreateRuleGroupConfig{
		"capacity 0":        {Name: "rg", Type: "STATEFUL", Capacity: 0},
		"capacity negative": {Name: "rg", Type: "STATEFUL", Capacity: -3},
		"bad type":          {Name: "rg", Type: "NOPE", Capacity: 5},
		"bad name":          {Name: "rg 1", Type: "STATEFUL", Capacity: 5},
		"no name":           {Type: "STATEFUL", Capacity: 5},
		"long description":  {Name: "rg", Type: "STATEFUL", Capacity: 5, Description: strings.Repeat("d", 513)},
		"bad summary opt":   {Name: "rg", Type: "STATEFUL", Capacity: 5, SummaryRuleOptions: []string{"BOGUS"}},
		"malformed rules":   {Name: "rg", Type: "STATEFUL", Capacity: 5, Rules: []byte(`{not json`)},
		"over capacity":     {Name: "rg", Type: "STATEFUL", Capacity: 1, Rules: rulesString("a", "b")},
	}

	for name, cfg := range both {
		t.Run(name, func(t *testing.T) {
			_, err := m.CreateRuleGroup(bg, cfg)
			assert.True(t, cerrors.IsInvalidArgument(err), "want InvalidArgument, got %v", err)
		})
	}

	_, err := m.CreateRuleGroup(bg, nfdriver.CreateRuleGroupConfig{Name: "rg", Type: "STATEFUL", Capacity: 1, Rules: rules})
	require.NoError(t, err)
}

func TestCreateRuleGroup_StatefulDomainType(t *testing.T) {
	m := newMock()

	rg, err := m.CreateRuleGroup(bg, nfdriver.CreateRuleGroupConfig{Name: "domains", Type: "STATEFUL_DOMAIN", Capacity: 5})
	require.NoError(t, err)
	assert.Equal(t, "STATEFUL_DOMAIN", rg.Type)
}

func TestRuleGroup_ConsumedExceedsCapacity(t *testing.T) {
	m := newMock()

	rg, err := m.CreateRuleGroup(bg, nfdriver.CreateRuleGroupConfig{
		Name: "rg", Type: "STATEFUL", Capacity: 2, Rules: rulesString("r1", "r2"),
	})
	require.NoError(t, err)
	assert.Equal(t, 2, rg.ConsumedCapacity)

	_, err = m.UpdateRuleGroup(bg, "rg", "", "STATEFUL", nfdriver.UpdateRuleGroupConfig{
		UpdateToken: rg.UpdateToken, Rules: rulesString("r1", "r2", "r3"),
	})
	assert.True(t, cerrors.IsInvalidArgument(err), "update past capacity: %v", err)

	got, _ := m.DescribeRuleGroup(bg, "rg", "", "STATEFUL")
	assert.Equal(t, 2, got.Capacity, "capacity is fixed at creation")
	assert.Equal(t, rg.UpdateToken, got.UpdateToken, "a rejected update changes nothing")
}

func TestPolicy_DefaultActionValidation(t *testing.T) {
	m := newMock()

	strict := []byte(`{"RuleOrder":"STRICT_ORDER"}`)
	custom := []byte(`[{"ActionName":"mine","ActionDefinition":{}}]`)

	cases := map[string]nfdriver.CreateFirewallPolicyConfig{
		"no actions":                 {Name: "p"},
		"no fragment actions":        {Name: "p", StatelessDefaultActions: standardActions},
		"unknown action":             {Name: "p", StatelessDefaultActions: []string{"aws:nope"}, StatelessFragmentDefaultActions: standardActions},
		"two standard actions":       {Name: "p", StatelessDefaultActions: []string{"aws:pass", "aws:drop"}, StatelessFragmentDefaultActions: standardActions},
		"only a custom action":       {Name: "p", StatelessDefaultActions: []string{"mine"}, StatelessFragmentDefaultActions: standardActions, Extra: nfdriver.PolicyExtra{StatelessCustomActions: custom}},
		"undefined custom action":    {Name: "p", StatelessDefaultActions: []string{"aws:pass", "ghost"}, StatelessFragmentDefaultActions: standardActions},
		"stateful default no strict": {Name: "p", StatelessDefaultActions: standardActions, StatelessFragmentDefaultActions: standardActions, StatefulDefaultActions: []string{"aws:drop_strict"}},
		"bad stateful default":       {Name: "p", StatelessDefaultActions: standardActions, StatelessFragmentDefaultActions: standardActions, StatefulDefaultActions: []string{"aws:what"}, Extra: nfdriver.PolicyExtra{StatefulEngineOptions: strict}},
		"bad rule order":             {Name: "p", StatelessDefaultActions: standardActions, StatelessFragmentDefaultActions: standardActions, Extra: nfdriver.PolicyExtra{StatefulEngineOptions: []byte(`{"RuleOrder":"RANDOM"}`)}},
		"tls holding without tls":    {Name: "p", StatelessDefaultActions: standardActions, StatelessFragmentDefaultActions: standardActions, Extra: nfdriver.PolicyExtra{EnableTLSSessionHolding: ptr(true)}},
		"bad name":                   {Name: "p_1", StatelessDefaultActions: standardActions, StatelessFragmentDefaultActions: standardActions},
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := m.CreateFirewallPolicy(bg, cfg)
			assert.True(t, cerrors.IsInvalidArgument(err), "want InvalidArgument, got %v", err)
		})
	}

	ok := nfdriver.CreateFirewallPolicyConfig{
		Name: "good", StatelessDefaultActions: []string{"aws:pass", "mine"}, StatelessFragmentDefaultActions: standardActions,
		StatefulDefaultActions: []string{"aws:drop_strict"},
		Extra:                  nfdriver.PolicyExtra{StatelessCustomActions: custom, StatefulEngineOptions: strict},
	}
	_, err := m.CreateFirewallPolicy(bg, ok)
	require.NoError(t, err)
}

func ptr[T any](v T) *T { return &v }

func TestPolicy_ReferenceValidation(t *testing.T) {
	m := newMock()
	stateless := seedRuleGroup(t, m, "rg-sl", "STATELESS", 5)
	stateful := seedRuleGroup(t, m, "rg-sf", "STATEFUL", 5)

	policy := func(sl, sf []nfdriver.RuleGroupReference, extra nfdriver.PolicyExtra) nfdriver.CreateFirewallPolicyConfig {
		return nfdriver.CreateFirewallPolicyConfig{
			Name: "p", StatelessDefaultActions: standardActions, StatelessFragmentDefaultActions: standardActions,
			StatelessRuleGroupReferences: sl, StatefulRuleGroupReferences: sf, Extra: extra,
		}
	}
	ref := func(arn string, prio int) nfdriver.RuleGroupReference {
		return nfdriver.RuleGroupReference{ResourceARN: arn, Priority: prio}
	}
	strict := nfdriver.PolicyExtra{StatefulEngineOptions: []byte(`{"RuleOrder":"STRICT_ORDER"}`)}
	ghost := "arn:aws:network-firewall:us-east-1:000000000000:stateless-rulegroup/ghost"

	cases := map[string]nfdriver.CreateFirewallPolicyConfig{
		"stateless without priority":  policy([]nfdriver.RuleGroupReference{ref(stateless.ARN, 0)}, nil, nfdriver.PolicyExtra{}),
		"duplicate priority":          policy([]nfdriver.RuleGroupReference{ref(stateless.ARN, 1), ref(stateless.ARN, 1)}, nil, nfdriver.PolicyExtra{}),
		"missing rule group":          policy([]nfdriver.RuleGroupReference{ref(ghost, 1)}, nil, nfdriver.PolicyExtra{}),
		"stateful in stateless list":  policy([]nfdriver.RuleGroupReference{ref(stateful.ARN, 1)}, nil, nfdriver.PolicyExtra{}),
		"stateless in stateful list":  policy(nil, []nfdriver.RuleGroupReference{ref(stateless.ARN, 1)}, nfdriver.PolicyExtra{}),
		"strict stateful no priority": policy(nil, []nfdriver.RuleGroupReference{ref(stateful.ARN, 0)}, strict),
		"malformed arn":               policy([]nfdriver.RuleGroupReference{ref("nope", 1)}, nil, nfdriver.PolicyExtra{}),
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := m.CreateFirewallPolicy(bg, cfg)
			assert.True(t, cerrors.IsInvalidArgument(err), "want InvalidArgument, got %v", err)
		})
	}

	_, err := m.CreateFirewallPolicy(bg, policy(
		[]nfdriver.RuleGroupReference{ref(stateless.ARN, 1)}, []nfdriver.RuleGroupReference{ref(stateful.ARN, 0)}, nfdriver.PolicyExtra{}))
	require.NoError(t, err, "default-order stateful references need no priority")
}

func TestPolicy_ExtrasRoundTrip(t *testing.T) {
	m := newMock()

	tlsCfg, err := m.CreateTLSInspectionConfiguration(bg, nfdriver.CreateTLSInspectionConfig{
		Name: "tls-1", Configuration: tlsConfigJSON("arn:aws:acm:us-east-1:000000000000:certificate/c1"),
	})
	require.NoError(t, err)

	extra := nfdriver.PolicyExtra{
		StatelessCustomActions:  []byte(`[{"ActionName":"mine","ActionDefinition":{"PublishMetricAction":{"Dimensions":[{"Value":"v"}]}}}]`),
		PolicyVariables:         []byte(`{"RuleVariables":{"HOME_NET":{"Definition":["10.0.0.0/8"]}}}`),
		StatefulEngineOptions:   []byte(`{"RuleOrder":"STRICT_ORDER","StreamExceptionPolicy":"DROP"}`),
		EnableTLSSessionHolding: ptr(true),
	}

	created, err := m.CreateFirewallPolicy(bg, nfdriver.CreateFirewallPolicyConfig{
		Name: "full", StatelessDefaultActions: []string{"aws:pass", "mine"}, StatelessFragmentDefaultActions: standardActions,
		StatefulDefaultActions: []string{"aws:alert_established"}, TLSInspectionConfigurationARN: tlsCfg.ARN, Extra: extra,
	})
	require.NoError(t, err)

	got, err := m.DescribeFirewallPolicy(bg, "full", "")
	require.NoError(t, err)
	assert.JSONEq(t, string(extra.StatelessCustomActions), string(got.Extra.StatelessCustomActions))
	assert.JSONEq(t, string(extra.PolicyVariables), string(got.Extra.PolicyVariables))
	assert.JSONEq(t, string(extra.StatefulEngineOptions), string(got.Extra.StatefulEngineOptions))
	assert.True(t, *got.Extra.EnableTLSSessionHolding)
	assert.Equal(t, tlsCfg.ARN, got.TLSInspectionConfigurationARN)
	assert.Equal(t, []string{"aws:alert_established"}, got.StatefulDefaultActions)

	// A TLS configuration can be replaced but not removed after create.
	_, err = m.UpdateFirewallPolicy(bg, "full", "", nfdriver.UpdateFirewallPolicyConfig{
		UpdateToken: created.UpdateToken, StatelessDefaultActions: standardActions, StatelessFragmentDefaultActions: standardActions,
	})
	assert.True(t, cerrors.IsInvalidArgument(err), "removing the TLS configuration: %v", err)
}
