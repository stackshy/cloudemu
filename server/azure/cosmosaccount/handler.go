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
	"sort"
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

// AccountPurger tears down a deleted account's full footprint in one step: its
// driver tables (the account's own table plus every container table in its
// namespace), its discovery attributes, and the cosmos data-plane handler's
// in-memory database/offer/attrs bookkeeping. The account control plane
// delegates DELETE to it so a deleted account leaves no ghost in List and a
// same-name recreate starts from an empty namespace. The cosmos data-plane
// handler implements it.
type AccountPurger interface {
	PurgeAccount(ctx context.Context, account string)
}

// Handler serves Microsoft.DocumentDB/databaseAccounts ARM requests against a
// database driver.
type Handler struct {
	db     dbdriver.Database
	attrs  attrBackend   // nil when the driver doesn't expose account attributes
	purger AccountPurger // tears down an account's data-plane footprint on delete
}

// New returns a Cosmos-account handler backed by db. purger is the cosmos
// data-plane handler, to which account DELETE is delegated so an account is torn
// down fully (tables, attributes and data-plane bookkeeping), not shallowly.
func New(db dbdriver.Database, purger AccountPurger) *Handler {
	h := &Handler{db: db, purger: purger}
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
		azurearm.WriteJSON(w, http.StatusOK, connectionStringsResult(requestBaseURL(r), rp.ResourceName))
	case "regeneratekey":
		var body armRegenerateKey
		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}
		// Regeneration is a long-running op in real Azure; complete it
		// synchronously with an empty 200 so the SDK's poller terminates. Keys
		// are deterministic per account, so there is nothing to persist.
		w.WriteHeader(http.StatusOK)
	case "failoverprioritychange":
		h.failoverPriorityChange(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unsupported action: "+rp.SubResource)
	}
}

// requestBaseURL reconstructs the scheme://host the client reached this handler
// on, so the account's documentEndpoint (and per-region and connection-string
// endpoints) point back at the live emulator rather than a public-DNS
// *.documents.azure.com host that never resolves to it.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host
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
		attrs.Locations = toAccountLocations(body.Properties.Locations)
		attrs.EnableMultipleWriteLocations = body.Properties.EnableMultipleWriteLocations
	}

	if h.attrs != nil {
		h.attrs.SetTableAttributes(name, attrs)
	}

	azurearm.WriteJSON(w, http.StatusOK, h.toARMAccount(r.Context(), requestBaseURL(r), rp))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if _, err := h.db.DescribeTable(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, h.toARMAccount(r.Context(), requestBaseURL(r), rp))
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

		out.Value = append(out.Value, renderAccount(rp.Subscription, requestBaseURL(r), name, attrs))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	// A shallow DeleteTable(account) would leave the account visible in List (its
	// discovery attributes linger) and its databases/containers live for a
	// same-name recreate to inherit. Delegate to the data-plane purger, which
	// drops the account's tables, attributes and data-plane bookkeeping in one
	// idempotent step.
	//
	// Delete is a long-running op in real Azure; complete it synchronously with
	// an empty 204 so the SDK's poller terminates (armcosmos BeginDelete accepts
	// only 202/204 — a 200 fails its client-side response validation).
	if h.purger != nil {
		h.purger.PurgeAccount(r.Context(), rp.ResourceName)
		w.WriteHeader(http.StatusNoContent)

		return
	}

	if err := h.db.DeleteTable(r.Context(), rp.ResourceName); err != nil && !cerrors.IsNotFound(err) {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// toARMAccount renders the ARM databaseAccounts wire shape, reading the stored
// attributes (kind / offer type / free tier / capabilities / location / tags)
// back through the driver.
func (h *Handler) toARMAccount(ctx context.Context, base string, rp *azurearm.ResourcePath) armAccount {
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

	return renderAccount(rp.Subscription, base, rp.ResourceName, attrs)
}

// renderAccount builds the ARM account body from stored attributes. Used by
// get/create (path-derived subscription) and by list. base is the emulator's
// scheme://host, so every endpoint it emits resolves back to the emulator.
//
//nolint:gocritic // attrs mirrors the driver value type.
func renderAccount(subscription, base, name string, attrs dbdriver.AccountAttributes) armAccount {
	location := attrs.Location
	if location == "" {
		location = defaultLocation
	}

	locations := attrs.Locations
	if len(locations) == 0 {
		// Single-region account (or one created before multi-region support):
		// synthesize the one entry from Location so every array is still populated.
		locations = []dbdriver.AccountLocation{{Name: location, FailoverPriority: 0}}
	}

	all := toArmLocations(base, name, locations)
	writeLocs := writeLocations(base, name, locations, attrs.EnableMultipleWriteLocations)

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
			DocumentEndpoint:         documentEndpoint(base, name),
			Locations:                all,
			ReadLocations:            all,
			WriteLocations:           writeLocs,
			FailoverPolicies:         toFailoverPolicies(name, locations),
		},
	}
}

// toAccountLocations converts the ARM create-request locations into the
// driver's provider-agnostic AccountLocation list. Real clients always send
// an explicit failoverPriority per entry (see the armcosmos Location docs),
// so no ordering inference is needed here.
func toAccountLocations(locs []armLocation) []dbdriver.AccountLocation {
	if len(locs) == 0 {
		return nil
	}

	out := make([]dbdriver.AccountLocation, 0, len(locs))

	for _, l := range locs {
		if l.LocationName == "" {
			continue
		}

		out = append(out, dbdriver.AccountLocation{
			Name:             l.LocationName,
			FailoverPriority: l.FailoverPriority,
			IsZoneRedundant:  l.IsZoneRedundant,
		})
	}

	return out
}

// sortedLocations returns locs ordered by ascending failover priority
// (priority 0 — the write region — first), matching how Azure orders every
// location array it returns.
func sortedLocations(locs []dbdriver.AccountLocation) []dbdriver.AccountLocation {
	out := make([]dbdriver.AccountLocation, len(locs))
	copy(out, locs)
	sort.SliceStable(out, func(i, j int) bool { return out[i].FailoverPriority < out[j].FailoverPriority })

	return out
}

// toArmLocations renders every declared region as an armLocation entry,
// ordered by failover priority — the shape shared by properties.locations
// and properties.readLocations (every region is readable). Every region's
// endpoint points at the same emulator host (which serves all regions), so the
// per-region DocumentEndpoint resolves rather than pointing at a public-DNS
// *.documents.azure.com host.
func toArmLocations(base, name string, locs []dbdriver.AccountLocation) []armLocation {
	sorted := sortedLocations(locs)
	out := make([]armLocation, 0, len(sorted))

	for _, l := range sorted {
		out = append(out, armLocation{
			ID:                name + "-" + l.Name,
			LocationName:      l.Name,
			DocumentEndpoint:  documentEndpoint(base, name),
			ProvisioningState: "Succeeded",
			FailoverPriority:  l.FailoverPriority,
			IsZoneRedundant:   l.IsZoneRedundant,
		})
	}

	return out
}

// writeLocations returns the regions that accept writes: every region when
// multi-write is enabled, otherwise only the single failoverPriority-0 region.
func writeLocations(base, name string, locs []dbdriver.AccountLocation, multiWrite bool) []armLocation {
	if multiWrite {
		return toArmLocations(base, name, locs)
	}

	sorted := sortedLocations(locs)
	if len(sorted) == 0 {
		return nil
	}

	return toArmLocations(base, name, sorted[:1])
}

// toFailoverPolicies renders properties.failoverPolicies: every region,
// ordered by failover priority.
func toFailoverPolicies(name string, locs []dbdriver.AccountLocation) []armFailover {
	sorted := sortedLocations(locs)
	out := make([]armFailover, 0, len(sorted))

	for _, l := range sorted {
		out = append(out, armFailover{
			ID:               name + "-" + l.Name,
			LocationName:     l.Name,
			FailoverPriority: l.FailoverPriority,
		})
	}

	return out
}

// failoverPriorityChange reorders an account's failover priorities. Real
// Azure runs this as a long-running operation and only accepts a 202 or 204
// response (200 fails client-side response validation); the emulator
// completes it synchronously with an empty 204 so the SDK's poller
// terminates on the first response.
func (h *Handler) failoverPriorityChange(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if h.attrs == nil {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "account attributes unavailable")
		return
	}

	var body armFailoverPolicies
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	attrs, err := h.attrs.TableAttributes(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	reordered := make([]dbdriver.AccountLocation, 0, len(body.FailoverPolicies))

	for _, fp := range body.FailoverPolicies {
		if fp.LocationName == "" {
			continue
		}

		loc := dbdriver.AccountLocation{Name: fp.LocationName, FailoverPriority: fp.FailoverPriority}

		for _, existing := range attrs.Locations {
			if strings.EqualFold(existing.Name, fp.LocationName) {
				loc.IsZoneRedundant = existing.IsZoneRedundant
				break
			}
		}

		reordered = append(reordered, loc)
	}

	attrs.Locations = reordered
	if len(reordered) > 0 {
		attrs.Location = sortedLocations(reordered)[0].Name
	}

	h.attrs.SetTableAttributes(rp.ResourceName, attrs)

	w.WriteHeader(http.StatusNoContent)
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

// documentEndpoint is the account's global connection endpoint. It is derived
// from the emulator's base URL (scheme://host) with the account name as a path
// segment, so a client that connects to the returned endpoint reaches the
// emulator and the data plane resolves the account from that /{account} prefix
// (see the cosmosdb data-plane splitAccount). Real Azure returns
// https://{name}.documents.azure.com:443/, which never resolves to the emulator.
func documentEndpoint(base, name string) string {
	return base + "/" + name + "/"
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
