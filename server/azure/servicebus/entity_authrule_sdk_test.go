package servicebus_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicebus/armservicebus/v2"
)

func rights(rs ...armservicebus.AccessRights) armservicebus.SBAuthorizationRule {
	out := make([]*armservicebus.AccessRights, 0, len(rs))
	for _, r := range rs {
		out = append(out, to.Ptr(r))
	}

	return armservicebus.SBAuthorizationRule{
		Properties: &armservicebus.SBAuthorizationRuleProperties{Rights: out},
	}
}

func wantStatus(t *testing.T, err error, status int) {
	t.Helper()

	var re *azcore.ResponseError
	if !errors.As(err, &re) || re.StatusCode != status {
		t.Fatalf("want HTTP %d, got %v", status, err)
	}
}

// TestSDKQueueAuthorizationRules drives the queue-scoped authorization rule
// operations azurerm_servicebus_queue_authorization_rule calls.
func TestSDKQueueAuthorizationRules(t *testing.T) {
	ts := pubsubServer(t)
	cf := newClientFactory(t, ts)
	ctx := context.Background()

	createNS(t, cf.NewNamespacesClient(), rgName, nsName, nil)

	qc := cf.NewQueuesClient()

	if _, err := qc.CreateOrUpdate(ctx, rgName, nsName, "q", armservicebus.SBQueue{
		Properties: &armservicebus.SBQueueProperties{MaxDeliveryCount: to.Ptr[int32](7)},
	}, nil); err != nil {
		t.Fatalf("create queue: %v", err)
	}

	before, err := qc.Get(ctx, rgName, nsName, "q", nil)
	if err != nil {
		t.Fatalf("get queue: %v", err)
	}

	created, err := qc.CreateOrUpdateAuthorizationRule(ctx, rgName, nsName, "q", "send",
		rights(armservicebus.AccessRightsSend), nil)
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}

	wantID := "/subscriptions/" + subID + "/resourceGroups/" + rgName +
		"/providers/Microsoft.ServiceBus/namespaces/" + nsName + "/queues/q/authorizationRules/send"
	if *created.ID != wantID || *created.Type != "Microsoft.ServiceBus/Namespaces/Queues/AuthorizationRules" {
		t.Fatalf("rule id/type = %s %s", *created.ID, *created.Type)
	}

	if _, err := qc.CreateOrUpdateAuthorizationRule(ctx, rgName, nsName, "q", "send",
		rights(armservicebus.AccessRightsSend, armservicebus.AccessRightsListen), nil); err != nil {
		t.Fatalf("update rule: %v", err)
	}

	got, err := qc.GetAuthorizationRule(ctx, rgName, nsName, "q", "send", nil)
	if err != nil || len(got.Properties.Rights) != 2 {
		t.Fatalf("get rule: %v %+v", err, got)
	}

	pager := qc.NewListAuthorizationRulesPager(rgName, nsName, "q", nil)

	page, err := pager.NextPage(ctx)
	if err != nil || len(page.Value) != 1 || *page.Value[0].Name != "send" {
		t.Fatalf("list must hold only the queue rule, got %v %+v", err, page.Value)
	}

	keys, err := qc.ListKeys(ctx, rgName, nsName, "q", "send", nil)
	if err != nil {
		t.Fatalf("listKeys: %v", err)
	}

	if !strings.HasSuffix(*keys.PrimaryConnectionString, ";EntityPath=q") {
		t.Fatalf("connection string lacks EntityPath: %s", *keys.PrimaryConnectionString)
	}

	regen, err := qc.RegenerateKeys(ctx, rgName, nsName, "q", "send", armservicebus.RegenerateAccessKeyParameters{
		KeyType: to.Ptr(armservicebus.KeyTypePrimaryKey),
	}, nil)
	if err != nil || *regen.PrimaryKey == *keys.PrimaryKey || *regen.SecondaryKey != *keys.SecondaryKey {
		t.Fatalf("regenerate primary: %v", err)
	}

	explicit, err := qc.RegenerateKeys(ctx, rgName, nsName, "q", "send", armservicebus.RegenerateAccessKeyParameters{
		KeyType: to.Ptr(armservicebus.KeyTypeSecondaryKey), Key: to.Ptr("c2VjcmV0"),
	}, nil)
	if err != nil || *explicit.SecondaryKey != "c2VjcmV0" {
		t.Fatalf("explicit key not used verbatim: %v", err)
	}

	_, err = qc.CreateOrUpdateAuthorizationRule(ctx, rgName, nsName, "q", "bad",
		rights(armservicebus.AccessRightsManage, armservicebus.AccessRightsListen), nil)
	wantStatus(t, err, http.StatusBadRequest)

	_, err = qc.CreateOrUpdateAuthorizationRule(ctx, rgName, nsName, "missing", "r",
		rights(armservicebus.AccessRightsSend), nil)
	wantStatus(t, err, http.StatusNotFound)

	after, err := qc.Get(ctx, rgName, nsName, "q", nil)
	if err != nil {
		t.Fatalf("get queue: %v", err)
	}

	b1, _ := json.Marshal(before.Properties)
	b2, _ := json.Marshal(after.Properties)

	if string(b1) != string(b2) {
		t.Fatalf("queue properties changed by rule writes:\n%s\n%s", b1, b2)
	}

	if _, err := qc.DeleteAuthorizationRule(ctx, rgName, nsName, "q", "send", nil); err != nil {
		t.Fatalf("delete rule: %v", err)
	}

	_, err = qc.GetAuthorizationRule(ctx, rgName, nsName, "q", "send", nil)
	wantStatus(t, err, http.StatusNotFound)

	if _, err := qc.Get(ctx, rgName, nsName, "q", nil); err != nil {
		t.Fatalf("queue must survive rule delete: %v", err)
	}

	if _, err := qc.CreateOrUpdateAuthorizationRule(ctx, rgName, nsName, "q", "listen",
		rights(armservicebus.AccessRightsListen), nil); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	if _, err := qc.Delete(ctx, rgName, nsName, "q", nil); err != nil {
		t.Fatalf("delete queue: %v", err)
	}

	_, err = qc.GetAuthorizationRule(ctx, rgName, nsName, "q", "listen", nil)
	wantStatus(t, err, http.StatusNotFound)
}

// TestSDKTopicAuthorizationRules covers the topic-scoped rule routes, which
// sit beside the subscriptions subtree.
func TestSDKTopicAuthorizationRules(t *testing.T) {
	ts := pubsubServer(t)
	cf := newClientFactory(t, ts)
	ctx := context.Background()

	createNS(t, cf.NewNamespacesClient(), rgName, nsName, nil)

	tc := cf.NewTopicsClient()

	if _, err := tc.CreateOrUpdate(ctx, rgName, nsName, "t", armservicebus.SBTopic{}, nil); err != nil {
		t.Fatalf("create topic: %v", err)
	}

	rule, err := tc.CreateOrUpdateAuthorizationRule(ctx, rgName, nsName, "t", "all",
		rights(armservicebus.AccessRightsManage, armservicebus.AccessRightsSend, armservicebus.AccessRightsListen), nil)
	if err != nil || *rule.Type != "Microsoft.ServiceBus/Namespaces/Topics/AuthorizationRules" {
		t.Fatalf("create topic rule: %v", err)
	}

	keys, err := tc.ListKeys(ctx, rgName, nsName, "t", "all", nil)
	if err != nil || !strings.HasSuffix(*keys.SecondaryConnectionString, ";EntityPath=t") {
		t.Fatalf("topic listKeys: %v", err)
	}

	nsPager := cf.NewNamespacesClient().NewListAuthorizationRulesPager(rgName, nsName, nil)

	page, err := nsPager.NextPage(ctx)
	if err != nil || len(page.Value) != 1 || *page.Value[0].Name != "RootManageSharedAccessKey" {
		t.Fatalf("namespace rules must not include topic rules: %v %+v", err, page.Value)
	}

	_, err = tc.CreateOrUpdateAuthorizationRule(ctx, rgName, nsName, "t", "none", rights(), nil)
	wantStatus(t, err, http.StatusBadRequest)
}
