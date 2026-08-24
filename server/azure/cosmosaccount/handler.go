// Package cosmosaccount implements the Azure Cosmos DB account ARM control
// plane (Microsoft.DocumentDB/databaseAccounts) as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos
// DatabaseAccountsClient clients configured with a custom endpoint hit this
// handler the same way they hit management.azure.com.
//
// This is the management-plane counterpart to the cosmos SQL data-plane
// handler: an account name maps to a driver table, and the account's kind /
// offer-type / free-tier / capabilities / location / tags are stored via the
// driver's optional TableAttributes capability so a discovery + cost consumer
// can price it.
//
// Coverage:
//
//	PUT    .../databaseAccounts/{name}                        — create/update
//	GET    .../databaseAccounts/{name}                        — get
//	DELETE .../databaseAccounts/{name}                        — delete
//	GET    .../databaseAccounts                               — list (byRG/subscription)
//	POST   .../databaseAccounts/{name}/listKeys              — read-write + read-only keys
//	POST   .../databaseAccounts/{name}/readonlykeys         — read-only keys
//	POST   .../databaseAccounts/{name}/listConnectionStrings — connection strings
//	POST   .../databaseAccounts/{name}/regenerateKey        — rotate a key
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
	// AccountTables lists the tables registered as Cosmos accounts, so List
	// returns only accounts and never the data-plane SQL containers that share
	// this driver.
	AccountTables() []string
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

	// Collection path (no account name): List / ListByResourceGroup.
	if rp.ResourceName == "" {
		h.list(w, r, &rp)
		return
	}

	// POST action sub-resources: listKeys, listConnectionStrings, regenerateKey…
	if rp.SubResource != "" {
		h.serveAction(w, r, &rp)
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

// serveAction dispatches the POST action sub-resources hung off an account.
func (h *Handler) serveAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	if _, err := h.db.DescribeTable(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	switch strings.ToLower(rp.SubResource) {
	case "listkeys":
		azurearm.WriteJSON(w, http.StatusOK, listKeysResult(rp.ResourceName))
	case "readonlykeys":
		azurearm.WriteJSON(w, http.StatusOK, readOnlyKeysResult(rp.ResourceName))
	case "listconnectionstrings":
		azurearm.WriteJSON(w, http.StatusOK, connectionStringsResult(rp.ResourceName))
	case "regeneratekey":
		var body armRegenerateKey
		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}
		// Regeneration is a long-running op in real Azure; complete it
		// synchronously with an empty 200 so the SDK's poller terminates. Keys
		// are deterministic per account, so there is nothing to persist.
		w.WriteHeader(http.StatusOK)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unsupported action: "+rp.SubResource)
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

	location := body.Location
	if location == "" {
		location = accountRegion(body.Properties)
	}

	attrs := dbdriver.AccountAttributes{
		Kind:          body.Kind,
		Location:      location,
		ResourceGroup: rp.ResourceGroup,
		Tags:          body.Tags,
	}
	if body.Properties != nil {
		attrs.OfferType = body.Properties.DatabaseAccountOfferType
		attrs.EnableFreeTier = body.Properties.EnableFreeTier
		attrs.Capabilities = capabilityNames(body.Properties.Capabilities)
	}

	if h.attrs != nil {
		h.attrs.SetTableAttributes(name, attrs)
	}

	azurearm.WriteJSON(w, http.StatusOK, h.toARMAccount(r.Context(), rp))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if _, err := h.db.DescribeTable(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, h.toARMAccount(r.Context(), rp))
}

// list serves the collection paths: GET .../databaseAccounts (subscription) and
// GET .../resourceGroups/{rg}/.../databaseAccounts (resource group).
func (h *Handler) list(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	out := armAccountList{Value: []armAccount{}}

	if h.attrs == nil {
		azurearm.WriteJSON(w, http.StatusOK, out)
		return
	}

	for _, name := range h.attrs.AccountTables() {
		attrs, err := h.attrs.TableAttributes(r.Context(), name)
		if err != nil {
			continue
		}

		// ListByResourceGroup filters to the requested group.
		if rp.ResourceGroup != "" && !strings.EqualFold(attrs.ResourceGroup, rp.ResourceGroup) {
			continue
		}

		out.Value = append(out.Value, renderAccount(rp.Subscription, name, attrs))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.db.DeleteTable(r.Context(), rp.ResourceName); err != nil && !cerrors.IsNotFound(err) {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// toARMAccount renders the ARM databaseAccounts wire shape, reading the stored
// attributes (kind / offer type / free tier / capabilities / location / tags)
// back through the driver.
func (h *Handler) toARMAccount(ctx context.Context, rp *azurearm.ResourcePath) armAccount {
	attrs := dbdriver.AccountAttributes{Kind: "GlobalDocumentDB", OfferType: "Standard"}
	if h.attrs != nil {
		if a, err := h.attrs.TableAttributes(ctx, rp.ResourceName); err == nil {
			attrs = a
		}
	}

	// The resource group comes from the request path (a create/get always
	// carries it); the stored copy is used only for byRG list filtering.
	if attrs.ResourceGroup == "" {
		attrs.ResourceGroup = rp.ResourceGroup
	}

	return renderAccount(rp.Subscription, rp.ResourceName, attrs)
}

// renderAccount builds the ARM account body from stored attributes. Used by
// get/create (path-derived subscription) and by list.
//
//nolint:gocritic // attrs mirrors the driver value type.
func renderAccount(subscription, name string, attrs dbdriver.AccountAttributes) armAccount {
	location := attrs.Location
	if location == "" {
		location = defaultLocation
	}

	return armAccount{
		ID:       azurearm.BuildResourceID(subscription, attrs.ResourceGroup, providerName, resourceType, name),
		Name:     name,
		Type:     providerName + "/" + resourceType,
		Location: location,
		Kind:     kindOrDefault(attrs.Kind),
		Tags:     attrs.Tags,
		Properties: &armAccountProps{
			DatabaseAccountOfferType: offerOrDefault(attrs.OfferType),
			EnableFreeTier:           attrs.EnableFreeTier,
			Capabilities:             toCapabilities(attrs.Capabilities),
			ProvisioningState:        "Succeeded",
			DocumentEndpoint:         documentEndpoint(name),
			Locations:                []armLocation{regionEntry(name, location)},
			ReadLocations:            []armLocation{regionEntry(name, location)},
			WriteLocations:           []armLocation{regionEntry(name, location)},
			FailoverPolicies:         []armFailover{failoverEntry(name, location)},
		},
	}
}

func kindOrDefault(kind string) string {
	if kind == "" {
		return "GlobalDocumentDB"
	}

	return kind
}

func offerOrDefault(offer string) string {
	if offer == "" {
		return "Standard"
	}

	return offer
}

// accountRegion returns the first declared location's name, if any.
func accountRegion(props *armAccountCreateProps) string {
	if props == nil {
		return ""
	}

	for _, l := range props.Locations {
		if l.LocationName != "" {
			return l.LocationName
		}
	}

	return ""
}

// documentEndpoint is the account's global connection endpoint.
func documentEndpoint(name string) string {
	return "https://" + name + ".documents.azure.com:443/"
}

// regionEntry builds a single-region Location record for the account's
// read/write/location arrays.
func regionEntry(name, location string) armLocation {
	return armLocation{
		ID:                name + "-" + location,
		LocationName:      location,
		DocumentEndpoint:  "https://" + name + "-" + location + ".documents.azure.com:443/",
		ProvisioningState: "Succeeded",
		FailoverPriority:  0,
	}
}

func failoverEntry(name, location string) armFailover {
	return armFailover{
		ID:               name + "-" + location,
		LocationName:     location,
		FailoverPriority: 0,
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
