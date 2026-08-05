package azuresql

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// ---- ARM JSON shapes ----

type armManagedInstance struct {
	ID         string                 `json:"id,omitempty"`
	Name       string                 `json:"name,omitempty"`
	Type       string                 `json:"type,omitempty"`
	Location   string                 `json:"location,omitempty"`
	SKU        *armSKU                `json:"sku,omitempty"`
	Tags       map[string]string      `json:"tags,omitempty"`
	Properties *armManagedInstanceCfg `json:"properties,omitempty"`
}

type armManagedInstanceCfg struct {
	AdministratorLogin string `json:"administratorLogin,omitempty"`
	VCores             int    `json:"vCores,omitempty"`
	StorageSizeInGB    int    `json:"storageSizeInGB,omitempty"`
	LicenseType        string `json:"licenseType,omitempty"`
	SubnetID           string `json:"subnetId,omitempty"`
	// RequestedBackupStorageRedundancy is the write field the armsql SDK sends
	// (Geo/GeoZone/Local/Zone). StorageAccountType is the CloudEmu read echo in
	// the driver's normalized form (…Redundant); CurrentBackupStorageRedundancy
	// echoes the enum form the SDK reads back.
	RequestedBackupStorageRedundancy string `json:"requestedBackupStorageRedundancy,omitempty"`
	StorageAccountType               string `json:"storageAccountType,omitempty"`
	CurrentBackupStorageRedundancy   string `json:"currentBackupStorageRedundancy,omitempty"`
	State                            string `json:"state,omitempty"`
	FullyQualifiedDomainName         string `json:"fullyQualifiedDomainName,omitempty"`
}

type armManagedDatabase struct {
	ID         string                 `json:"id,omitempty"`
	Name       string                 `json:"name,omitempty"`
	Type       string                 `json:"type,omitempty"`
	Properties *armManagedDatabaseCfg `json:"properties,omitempty"`
}

type armManagedDatabaseCfg struct {
	Collation string `json:"collation,omitempty"`
	Status    string `json:"status,omitempty"`
}

func (h *Handler) managedInstances() (rdsdriver.ManagedInstances, bool) {
	c, ok := h.db.(rdsdriver.ManagedInstances)
	return c, ok
}

func managedInstanceID(rp *azurearm.ResourcePath) string {
	return azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceManagedInstances, rp.ResourceName)
}

// serveManagedInstanceRoute dispatches /managedInstances[/{name}[/{sub}]].
func (h *Handler) serveManagedInstanceRoute(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	mi, ok := h.managedInstances()
	if !ok {
		writeUnsupported(w, "managedInstances")
		return
	}

	switch {
	case rp.SubResource == subResourceDatabases:
		h.serveManagedDatabase(w, r, rp, mi)
	case rp.SubResource == subMIStart || rp.SubResource == subMIStop || rp.SubResource == subMIFailover:
		h.serveManagedInstanceAction(w, r, rp, mi)
	case rp.SubResource != "":
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unsupported sub-resource: "+rp.SubResource)
	case rp.ResourceName == "":
		h.listManagedInstances(w, r, rp, mi)
	default:
		h.serveManagedInstance(w, r, rp, mi)
	}
}

func (h *Handler) serveManagedInstance(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, mi rdsdriver.ManagedInstances,
) {
	switch r.Method {
	case http.MethodPut:
		h.putManagedInstance(w, r, rp, mi)
	case http.MethodPatch:
		h.patchManagedInstance(w, r, rp, mi)
	case http.MethodGet:
		h.getManagedInstance(w, r, rp, mi)
	case http.MethodDelete:
		if err := mi.DeleteManagedInstance(r.Context(), rp.ResourceName); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		w.WriteHeader(http.StatusOK)
	default:
		writeMethodNotAllowed(w)
	}
}

func miCfgFromBody(body *armManagedInstance, rp *azurearm.ResourcePath) rdsdriver.ManagedInstanceConfig {
	cfg := rdsdriver.ManagedInstanceConfig{Name: rp.ResourceName, Location: body.Location, Tags: body.Tags}
	if body.SKU != nil {
		cfg.SKUName = body.SKU.Name
		cfg.SKUTier = body.SKU.Tier
	}

	if body.Properties != nil {
		cfg.AdminLogin = body.Properties.AdministratorLogin
		cfg.VCores = body.Properties.VCores
		cfg.StorageGB = body.Properties.StorageSizeInGB
		cfg.LicenseType = body.Properties.LicenseType
		cfg.SubnetID = body.Properties.SubnetID
		cfg.StorageAccountType = normalizeBackupRedundancy(body.Properties.RequestedBackupStorageRedundancy)
	}

	return cfg
}

// Backup storage redundancy: the armsql SDK enum (…) and the driver's stored
// read form (…Redundant).
const (
	backupGeo              = "Geo"
	backupGeoRedundant     = "GeoRedundant"
	backupGeoZone          = "GeoZone"
	backupGeoZoneRedundant = "GeoZoneRedundant"
	backupLocal            = "Local"
	backupLocalRedundant   = "LocalRedundant"
	backupZone             = "Zone"
	backupZoneRedundant    = "ZoneRedundant"
)

// normalizeBackupRedundancy maps the armsql requestedBackupStorageRedundancy
// enum (Geo/GeoZone/Local/Zone) to the driver's read form (…Redundant). An
// empty/unknown value returns "" so the provider applies its own default.
func normalizeBackupRedundancy(v string) string {
	switch v {
	case backupGeo:
		return backupGeoRedundant
	case backupGeoZone:
		return backupGeoZoneRedundant
	case backupLocal:
		return backupLocalRedundant
	case backupZone:
		return backupZoneRedundant
	default:
		return ""
	}
}

// backupRedundancyEnum reverses normalizeBackupRedundancy so the armsql SDK,
// which reads currentBackupStorageRedundancy, observes the stored redundancy.
func backupRedundancyEnum(v string) string {
	switch v {
	case backupGeoRedundant:
		return backupGeo
	case backupGeoZoneRedundant:
		return backupGeoZone
	case backupLocalRedundant:
		return backupLocal
	case backupZoneRedundant:
		return backupZone
	default:
		return ""
	}
}

func (*Handler) putManagedInstance(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, mi rdsdriver.ManagedInstances,
) {
	var body armManagedInstance
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := miCfgFromBody(&body, rp)

	out, err := mi.CreateManagedInstance(r.Context(), cfg)
	if err != nil {
		if !cerrors.IsAlreadyExists(err) {
			azurearm.WriteCErr(w, err)
			return
		}

		// Upsert: PUT on an existing managed instance applies the body.
		out, err = mi.UpdateManagedInstance(r.Context(), cfg)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMManagedInstance(out, rp))
}

func (*Handler) patchManagedInstance(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, mi rdsdriver.ManagedInstances,
) {
	var body armManagedInstance
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	out, err := mi.UpdateManagedInstance(r.Context(), miCfgFromBody(&body, rp))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMManagedInstance(out, rp))
}

func (*Handler) getManagedInstance(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, mi rdsdriver.ManagedInstances,
) {
	out, err := mi.GetManagedInstance(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMManagedInstance(out, rp))
}

func (*Handler) listManagedInstances(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, mi rdsdriver.ManagedInstances,
) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	items, err := mi.ListManagedInstances(r.Context())
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armManagedInstance, 0, len(items))
	for i := range items {
		out = append(out, toARMManagedInstance(&items[i], rp))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList[armManagedInstance]{Value: out})
}

func (h *Handler) serveManagedInstanceAction(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, mi rdsdriver.ManagedInstances,
) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	var err error

	switch rp.SubResource {
	case subMIStart:
		err = mi.StartManagedInstance(r.Context(), rp.ResourceName)
	case subMIStop:
		err = mi.StopManagedInstance(r.Context(), rp.ResourceName)
	case subMIFailover:
		err = mi.FailoverManagedInstance(r.Context(), rp.ResourceName)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.getManagedInstance(w, r, rp, mi)
}

func toARMManagedInstance(mi *rdsdriver.ManagedInstance, rp *azurearm.ResourcePath) armManagedInstance {
	return armManagedInstance{
		ID:       managedInstanceID(rp),
		Name:     mi.Name,
		Type:     providerName + "/" + resourceManagedInstances,
		Location: mi.Location,
		Tags:     mi.Tags,
		SKU:      &armSKU{Name: mi.SKUName, Tier: mi.SKUTier},
		Properties: &armManagedInstanceCfg{
			AdministratorLogin:               mi.AdminLogin,
			VCores:                           mi.VCores,
			StorageSizeInGB:                  mi.StorageGB,
			LicenseType:                      mi.LicenseType,
			SubnetID:                         mi.SubnetID,
			StorageAccountType:               mi.StorageAccountType,
			RequestedBackupStorageRedundancy: backupRedundancyEnum(mi.StorageAccountType),
			CurrentBackupStorageRedundancy:   backupRedundancyEnum(mi.StorageAccountType),
			State:                            mi.State,
			FullyQualifiedDomainName:         mi.FQDN,
		},
	}
}

// ---- Managed databases ----

//nolint:dupl // mirrors the sibling sub-resource handler by design.
func (h *Handler) serveManagedDatabase(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, mi rdsdriver.ManagedInstances,
) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		items, err := mi.ListManagedDatabases(r.Context(), rp.ResourceName)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		out := make([]armManagedDatabase, 0, len(items))
		for i := range items {
			out = append(out, toARMManagedDatabase(&items[i], rp))
		}

		azurearm.WriteJSON(w, http.StatusOK, armList[armManagedDatabase]{Value: out})

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putManagedDatabase(w, r, rp, mi)
	case http.MethodGet:
		out, err := mi.GetManagedDatabase(r.Context(), rp.ResourceName, rp.SubResourceName)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, toARMManagedDatabase(out, rp))
	case http.MethodDelete:
		if err := mi.DeleteManagedDatabase(r.Context(), rp.ResourceName, rp.SubResourceName); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		w.WriteHeader(http.StatusOK)
	default:
		writeMethodNotAllowed(w)
	}
}

func (*Handler) putManagedDatabase(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, mi rdsdriver.ManagedInstances,
) {
	var body armManagedDatabase
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := rdsdriver.ManagedDatabaseConfig{Instance: rp.ResourceName, Name: rp.SubResourceName}
	if body.Properties != nil {
		cfg.Collation = body.Properties.Collation
	}

	out, err := mi.CreateManagedDatabase(r.Context(), cfg)
	if err != nil {
		existing, getErr := mi.GetManagedDatabase(r.Context(), rp.ResourceName, rp.SubResourceName)
		if getErr != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		out = existing
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMManagedDatabase(out, rp))
}

func toARMManagedDatabase(mdb *rdsdriver.ManagedDatabase, rp *azurearm.ResourcePath) armManagedDatabase {
	return armManagedDatabase{
		ID:         managedInstanceID(rp) + "/databases/" + mdb.Name,
		Name:       mdb.Name,
		Type:       providerName + "/" + resourceManagedInstances + "/databases",
		Properties: &armManagedDatabaseCfg{Collation: mdb.Collation, Status: mdb.Status},
	}
}
