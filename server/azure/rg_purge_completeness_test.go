package azure

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	azureprovider "github.com/stackshy/cloudemu/v2/providers/azure"
	"github.com/stackshy/cloudemu/v2/server"
	"github.com/stackshy/cloudemu/v2/server/azure/resourcegroups"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

const modPath = "github.com/stackshy/cloudemu/v2/"

// exemption records why a registered handler is not a resource-group purger.
// PurgedBy names the purger that tears its state down instead.
type exemption struct {
	Reason   string
	PurgedBy string
}

const (
	vmPurger     = "server/azure/virtualmachines.Handler"
	cosmosPurger = "server/azure/cosmosaccount.Handler"
	cosmosChild  = "SQL and Mongo databases are children of the account"
	noARMState   = "serves no resource-group-scoped ARM state"
	dataPlane    = "data plane of a resource whose control plane owns the purge"
	databricksDP = "Databricks workspace data plane; no resource-group-scoped ARM state"
)

// rgPurgeExempt lists every registered handler that is not a purger, keyed by
// package path (relative to the module) and type name. Short %T names would
// collide across the databricks sub-handlers.
//
//nolint:gochecknoglobals // test fixture table
var rgPurgeExempt = map[string]exemption{
	"server/azure/subscriptions.Handler":                {Reason: "subscription scope, not inside a resource group"},
	"server/azure/tenants.Handler":                      {Reason: "tenant scope, not inside a resource group"},
	"server/azure/providers.Handler":                    {Reason: "resource-provider registration is subscription-scoped"},
	"server/azure/aad.MetadataHandler":                  {Reason: noARMState},
	"server/azure/aad.TokenHandler":                     {Reason: noARMState},
	"server/azure/locks.Handler":                        {Reason: "the lock gate blocks a group delete while any lock exists at or below it"},
	"server/azure/resourcegroups.Handler":               {Reason: "owns the cascade itself"},
	"server/azure/monitor.MetricsHandler":               {Reason: "read-only metrics over other resources"},
	"server/azure/monitor.ActivityLogHandler":           {Reason: "read-only activity log"},
	"server/azure/disks.Handler":                        {Reason: "shares the compute driver", PurgedBy: vmPurger},
	"server/azure/snapshots.Handler":                    {Reason: "shares the compute driver", PurgedBy: vmPurger},
	"server/azure/images.Handler":                       {Reason: "shares the compute driver", PurgedBy: vmPurger},
	"server/azure/sshpublickeys.Handler":                {Reason: "shares the compute driver", PurgedBy: vmPurger},
	"server/azure/cosmosdb.Handler":                     {Reason: dataPlane},
	"server/azure/cosmosdb.ARMHandler":                  {Reason: cosmosChild, PurgedBy: cosmosPurger},
	"server/azure/cosmosdb.MongoARMHandler":             {Reason: cosmosChild, PurgedBy: cosmosPurger},
	"server/azure/eventgrid.PublishHandler":             {Reason: "event publish data plane"},
	"server/azure/notificationhubs.RegistrationHandler": {Reason: "device registration data plane"},
	"server/azure/kusto.DataPlaneHandler":               {Reason: "query data plane is a separate instance (deferred, plan A.9)"},
	"server/azure/databricks.DataPlaneHandler":          {Reason: databricksDP},
	"server/azure/databricks/hostmeta.Handler":          {Reason: databricksDP},
	"server/azure/databricks/secrets.Handler":           {Reason: databricksDP},
	"server/azure/databricks/token.Handler":             {Reason: databricksDP},
	"server/azure/databricks/gitcredentials.Handler":    {Reason: databricksDP},
	"server/azure/databricks/repos.Handler":             {Reason: databricksDP},
	"server/azure/databricks/dbfs.Handler":              {Reason: databricksDP},
	"server/azure/databricks/wsfs.Handler":              {Reason: databricksDP},
	"server/azure/databricks/sqlwarehouses.Handler":     {Reason: databricksDP},
	"server/azure/databricks/queryhistory.Handler":      {Reason: databricksDP},
	"server/azure/databricks/pipelines.Handler":         {Reason: databricksDP},
	"server/azure/databricks/serving.Handler":           {Reason: databricksDP},
	"server/azure/databricks/unitycatalog.Handler":      {Reason: databricksDP},
	"server/azure/databricks/ucstorage.Handler":         {Reason: databricksDP},
	"server/azure/databricks/scim.Handler":              {Reason: databricksDP},
	"server/azure/ai.DataPlaneHandler":                  {Reason: "inference data plane"},
	"server/azure/search.DataPlaneHandler":              {Reason: "search data plane"},
	"services/kubernetes.APIServer":                     {Reason: "AKS Kubernetes data plane, torn down by the cluster delete"},
	"server/azure/resourcegraph.Handler":                {Reason: "read-only inventory query"},
	"server/azure/resourcegraph.ResourcesHandler":       {Reason: "read-only inventory listing"},
	"server/azure/costmanagement.Handler":               {Reason: "read-only cost query"},
	"server/azure/acr.Handler":                          {Reason: "registry data plane", PurgedBy: "server/azure/acr.ARMHandler"},
	"server/azure/keyvault.Handler":                     {Reason: dataPlane},
	"server/azure/keyvault.KeysHandler":                 {Reason: dataPlane},
	"server/azure/keyvault.CertsHandler":                {Reason: dataPlane},
	"server/azure/tablestorage.Handler":                 {Reason: "storage data plane, shared namespace"},
	"server/azure/queue.Handler":                        {Reason: "storage data plane, shared namespace"},
	"server/azure/blobstorage.Handler":                  {Reason: "storage data plane", PurgedBy: "server/azure/storageaccount.Handler"},
}

// pendingPurger lists the resource-group-scoped families whose purge lands in a
// later PR, with the tracker row it closes. A type here that already purges
// fails the guard, so the entry has to be removed when the purge lands.
//
//nolint:gochecknoglobals // test fixture table
var pendingPurger = map[string]string{}

// driversWithEverything returns a Drivers bundle with every driver field set,
// so every handler New can register is registered.
func driversWithEverything(t *testing.T) Drivers {
	t.Helper()

	d := DriversFrom(azureprovider.New())
	d.K8sAPI = kubernetes.NewAPIServer()

	v := reflect.ValueOf(d)
	for i := range v.NumField() {
		f := v.Field(i)
		switch f.Kind() { //nolint:exhaustive // only nillable kinds can hide a handler
		case reflect.Interface, reflect.Pointer:
			if f.IsNil() {
				t.Fatalf("Drivers.%s is nil: driversWithEverything must set it so its handler is guarded",
					v.Type().Field(i).Name)
			}
		}
	}

	return d
}

// assembledServer returns the dispatcher New wraps in the property overlay.
func assembledServer(t *testing.T, d Drivers) *server.Server {
	t.Helper()

	oh, ok := New(d).(*overlayHandler)
	if !ok {
		t.Fatal("New no longer returns the overlay handler")
	}

	srv, ok := oh.next.(*server.Server)
	if !ok {
		t.Fatalf("overlay wraps %T, want *server.Server", oh.next)
	}

	return srv
}

func handlerKey(h server.Handler) string {
	ty := reflect.TypeOf(h)
	if ty.Kind() == reflect.Pointer {
		ty = ty.Elem()
	}

	return strings.TrimPrefix(ty.PkgPath(), modPath) + "." + ty.Name()
}

func purgerKeys(handlers []server.Handler) map[string]bool {
	out := map[string]bool{}

	for _, h := range handlers {
		if _, ok := h.(resourcegroups.ResourceGroupPurger); ok {
			out[handlerKey(h)] = true
		}
	}

	return out
}

// TestEveryHandlerPurgesOrIsExempt fails when a registered handler neither
// joins the resource-group cascade nor has a recorded reason not to, so a new
// resource-group-scoped service cannot be forgotten.
func TestEveryHandlerPurgesOrIsExempt(t *testing.T) {
	srv := assembledServer(t, driversWithEverything(t))
	handlers := srv.Handlers()
	purgers := purgerKeys(handlers)
	seen := map[string]bool{}

	for _, h := range handlers {
		key := handlerKey(h)
		seen[key] = true

		_, exempt := rgPurgeExempt[key]
		_, pending := pendingPurger[key]

		switch {
		case purgers[key] && exempt:
			t.Errorf("%s purges and is also exempt: drop the exemption", key)
		case purgers[key] && pending:
			t.Errorf("%s purges now: drop it from pendingPurger", key)
		case !purgers[key] && !exempt && !pending:
			t.Errorf("%s is registered but neither implements ResourceGroupPurger nor is exempt", key)
		}
	}

	for key, ex := range rgPurgeExempt {
		if !seen[key] {
			t.Errorf("stale exemption %s: no registered handler has that type", key)
		}

		if ex.PurgedBy != "" && !purgers[ex.PurgedBy] {
			t.Errorf("exemption %s names PurgedBy %s, which is not a registered purger", key, ex.PurgedBy)
		}
	}

	for key := range pendingPurger {
		if !seen[key] {
			t.Errorf("stale pendingPurger entry %s: no registered handler has that type", key)
		}
	}
}

// TestPurgeDriversImplementNarrowInterfaces checks the provider side of every
// handler that reaches its purge through a type assertion: a production driver
// that lost the method would turn the handler's purge into an error.
func TestPurgeDriversImplementNarrowInterfaces(t *testing.T) {
	d := driversWithEverything(t)

	if _, ok := d.VirtualMachines.(computedriver.AzureResourceGroupPurger); !ok {
		t.Errorf("Drivers.VirtualMachines (%T) does not implement AzureResourceGroupPurger", d.VirtualMachines)
	}

	if _, ok := d.VirtualMachines.(computedriver.AzureVMDeleter); !ok {
		t.Errorf("Drivers.VirtualMachines (%T) does not implement AzureVMDeleter", d.VirtualMachines)
	}

	for name, drv := range map[string]any{
		"DNS": d.DNS, "ACR": d.ACR, "AKS": d.AKS, "ContainerInstances": d.ContainerInstances,
		"KeyVault": d.KeyVault, "SQL": d.SQL, "Cache": d.Cache, "Functions": d.Functions,
		"MySQLFlex": d.MySQLFlex, "PostgresFlex": d.PostgresFlex, "SearchControl": d.SearchControl,
		"EventGrid": d.EventGrid, "NotificationHubs": d.NotificationHubs, "Databricks": d.Databricks,
		"CognitiveServices": d.CognitiveServices, "MachineLearning": d.MachineLearning,
	} {
		if _, ok := drv.(resourcegroups.ResourceGroupPurger); !ok {
			t.Errorf("Drivers.%s (%T) has no PurgeResourceGroup(ctx, sub, rg) error method", name, drv)
		}
	}
}

// TestPurgerSetSupersetOfLegacy pins the purgers the hand-maintained list held
// before auto-collection, and the dependency order between compute, network
// consumers and the network itself.
func TestPurgerSetSupersetOfLegacy(t *testing.T) {
	srv := assembledServer(t, driversWithEverything(t))
	purgers := purgerKeys(srv.Handlers())

	legacy := []string{
		"virtualmachines", "vnet", "loadbalancer", "applicationgateway", "firewall", "bastion", "frontdoor",
		"privatedns", "storageaccount", "managedidentity", "loadtesting", "signalr", "webpubsub",
		"communication", "digitaltwins", "managedgrafana", "devcenter", "purview", "chaosstudio",
		"elasticsan", "managedlustre", "appconfiguration", "redisenterprise", "healthcareapis",
		"mongocluster", "batch", "streamanalytics", "recoveryservices", "iothub", "apimanagement", "logic",
		"sqlvirtualmachine", "containerapps", "synapse", "appinsights", "datafactory",
	}

	for _, pkg := range legacy {
		if key := "server/azure/" + pkg + ".Handler"; !purgers[key] {
			t.Errorf("legacy purger %s is no longer collected", key)
		}
	}

	order := map[string]int{}
	for i, p := range resourcegroups.CollectPurgers(srv.Handlers()) {
		order[strings.TrimSuffix(strings.TrimPrefix(handlerKey(p.(server.Handler)), "server/azure/"), ".Handler")] = i
	}

	consumers := []string{"loadbalancer", "applicationgateway", "firewall", "bastion", "frontdoor"}
	network := []string{"vnet", "privatedns"}

	for _, c := range consumers {
		if order["virtualmachines"] >= order[c] {
			t.Errorf("virtualmachines must purge before %s", c)
		}

		for _, n := range network {
			if order[c] >= order[n] {
				t.Errorf("%s must purge before %s", c, n)
			}
		}
	}
}

// TestRGScopedTypesRouteToPurger catches an exempt handler that has quietly
// taken over a resource-group-scoped ARM type: each type's PUT must be served
// by a purger, by an exempt handler with PurgedBy, or by a pending family.
func TestRGScopedTypesRouteToPurger(t *testing.T) {
	srv := assembledServer(t, driversWithEverything(t))
	purgers := purgerKeys(srv.Handlers())

	types := []string{
		"Microsoft.Compute/virtualMachines", "Microsoft.Compute/virtualMachineScaleSets",
		"Microsoft.Compute/disks", "Microsoft.Compute/snapshots", "Microsoft.Compute/images",
		"Microsoft.Compute/sshPublicKeys", "Microsoft.Network/dnsZones",
		"Microsoft.ContainerRegistry/registries", "Microsoft.ContainerService/managedClusters",
		"Microsoft.ContainerInstance/containerGroups", "Microsoft.Network/virtualNetworks",
		"Microsoft.Network/publicIPAddresses", "Microsoft.Network/networkInterfaces",
		"Microsoft.Network/networkSecurityGroups", "Microsoft.Network/loadBalancers",
		"Microsoft.Network/applicationGateways", "Microsoft.Network/azureFirewalls",
		"Microsoft.Network/bastionHosts", "Microsoft.Network/privateDnsZones", "Microsoft.Cdn/profiles",
		"Microsoft.Storage/storageAccounts", "Microsoft.ManagedIdentity/userAssignedIdentities",
		"Microsoft.KeyVault/vaults", "Microsoft.DocumentDB/databaseAccounts",
		"Microsoft.ServiceBus/namespaces", "Microsoft.EventHub/namespaces", "Microsoft.Sql/servers",
		"Microsoft.Cache/redis", "Microsoft.OperationalInsights/workspaces",
		"Microsoft.DBforMySQL/flexibleServers", "Microsoft.DBforPostgreSQL/flexibleServers",
		"Microsoft.Search/searchServices", "Microsoft.Web/sites", "Microsoft.Web/serverfarms",
		"Microsoft.Insights/actionGroups", "Microsoft.EventGrid/topics",
		"Microsoft.NotificationHubs/namespaces", "Microsoft.Databricks/workspaces",
		"Microsoft.Kusto/clusters", "Microsoft.CognitiveServices/accounts",
		"Microsoft.App/containerApps", "Microsoft.Synapse/workspaces", "Microsoft.Insights/components",
		"Microsoft.DataFactory/factories", "Microsoft.ApiManagement/service", "Microsoft.Logic/workflows",
	}

	for _, typ := range types {
		path := "/subscriptions/s/resourceGroups/g/providers/" + typ + "/x"
		req := httptest.NewRequest(http.MethodPut, path+"?api-version=2023-01-01", http.NoBody)

		h := srv.Match(req)
		if h == nil {
			t.Errorf("%s: no handler matches", typ)
			continue
		}

		key := handlerKey(h)
		_, pending := pendingPurger[key]

		if !purgers[key] && rgPurgeExempt[key].PurgedBy == "" && !pending {
			t.Errorf("%s is served by %s, which does not purge resource groups", typ, key)
		}
	}
}
