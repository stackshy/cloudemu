// Package cosmosaccount implements the Azure Cosmos DB account ARM control
// plane (Microsoft.DocumentDB/databaseAccounts) as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos
// DatabaseAccountsClient clients configured with a custom endpoint hit this
// handler the same way they hit management.azure.com.
//
// This is the management-plane counterpart to the cosmos SQL data-plane
// handler: an account name maps to a driver table, and the account's kind /
// offer-type / free-tier / capabilities cost attributes are stored via the
// driver's optional TableAttributes capability so a discovery + cost consumer
// can price it.
//
// Coverage:
//
//	PUT    .../providers/Microsoft.DocumentDB/databaseAccounts/{name} — create/update
//	GET    .../providers/Microsoft.DocumentDB/databaseAccounts/{name} — get
//	DELETE .../providers/Microsoft.DocumentDB/databaseAccounts/{name} — delete
//
// Create is a long-running operation in real Azure; the emulator completes it
// synchronously by returning 200 with the resource body inline so the SDK's LRO
// poller terminates on the first response.
package cosmosaccount

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

const (
	providerName    = "Microsoft.DocumentDB"
	resourceType    = "databaseAccounts"
	defaultLocation = "eastus"
)

// attrBackend is the optional Cosmos-account attribute capability. The Azure
// cosmos mock implements it; DynamoDB/Firestore don't.
type attrBackend interface {
	SetTableAttributes(table string, attrs dbdriver.AccountAttributes)
	TableAttributes(ctx context.Context, table string) (dbdriver.AccountAttributes, error)
}

// Handler serves Microsoft.DocumentDB/databaseAccounts ARM requests against a
// database driver.
type Handler struct {
	db    dbdriver.Database
	attrs attrBackend // nil when the driver doesn't expose account attributes
}

// New returns a Cosmos-account handler backed by db.
func New(db dbdriver.Database) *Handler {
	h := &Handler{db: db}
	if a, ok := db.(attrBackend); ok {
		h.attrs = a
	}

	return h
}

// Matches claims only the ARM management path for database accounts. It never
// claims the cosmos data-plane path (/dbs/... URLs), so data-plane routing is
// undisturbed. It is disjoint from managedcassandra (cassandraClusters) and
// cosmospostgresql (Microsoft.DBforPostgreSQL) too.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return strings.EqualFold(rp.Provider, providerName) &&
		strings.EqualFold(rp.ResourceType, resourceType)
}

// ServeHTTP routes the request based on path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if rp.ResourceName == "" {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "database account name required")
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdate(w, r, &rp)
	case http.MethodGet:
		h.get(w, r, &rp)
	case http.MethodDelete:
		h.deleteAccount(w, r, &rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armAccountCreate
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	name := rp.ResourceName

	// Upsert: an existing account (table) re-applies its cost attributes rather
	// than erroring, matching real Azure's create-or-update semantics.
	if err := h.db.CreateTable(r.Context(), dbdriver.TableConfig{Name: name}); err != nil &&
		!cerrors.IsAlreadyExists(err) {
		azurearm.WriteCErr(w, err)
		return
	}

	attrs := dbdriver.AccountAttributes{Kind: body.Kind}
	if body.Properties != nil {
		attrs.OfferType = body.Properties.DatabaseAccountOfferType
		attrs.EnableFreeTier = body.Properties.EnableFreeTier
		attrs.Capabilities = capabilityNames(body.Properties.Capabilities)
	}

	if h.attrs != nil {
		h.attrs.SetTableAttributes(name, attrs)
	}

	azurearm.WriteJSON(w, http.StatusOK, h.toARMAccount(r.Context(), rp, body.Location, body.Tags))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if _, err := h.db.DescribeTable(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, h.toARMAccount(r.Context(), rp, defaultLocation, nil))
}

func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.db.DeleteTable(r.Context(), rp.ResourceName); err != nil && !cerrors.IsNotFound(err) {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// toARMAccount renders the ARM databaseAccounts wire shape, reading the stored
// cost attributes (kind / offer type / free tier / capabilities) back through
// the driver.
func (h *Handler) toARMAccount(
	ctx context.Context, rp *azurearm.ResourcePath, location string, tags map[string]string,
) armAccount {
	attrs := dbdriver.AccountAttributes{Kind: "GlobalDocumentDB", OfferType: "Standard"}
	if h.attrs != nil {
		if a, err := h.attrs.TableAttributes(ctx, rp.ResourceName); err == nil {
			attrs = a
		}
	}

	if location == "" {
		location = defaultLocation
	}

	return armAccount{
		ID:       azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, rp.ResourceName),
		Name:     rp.ResourceName,
		Type:     providerName + "/" + resourceType,
		Location: location,
		Kind:     attrs.Kind,
		Tags:     tags,
		Properties: &armAccountProps{
			DatabaseAccountOfferType: attrs.OfferType,
			EnableFreeTier:           attrs.EnableFreeTier,
			Capabilities:             toCapabilities(attrs.Capabilities),
			ProvisioningState:        "Succeeded",
		},
	}
}

func capabilityNames(caps []armCapability) []string {
	if len(caps) == 0 {
		return nil
	}

	names := make([]string, 0, len(caps))

	for _, c := range caps {
		if c.Name != "" {
			names = append(names, c.Name)
		}
	}

	return names
}

func toCapabilities(names []string) []armCapability {
	if len(names) == 0 {
		return nil
	}

	caps := make([]armCapability, 0, len(names))

	for _, n := range names {
		caps = append(caps, armCapability{Name: n})
	}

	return caps
}
