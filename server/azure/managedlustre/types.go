package managedlustre

import (
	"github.com/stackshy/cloudemu/v2/providers/azure/managedlustre"
)

// fsRequest is the ARM PUT/PATCH body. location, tags, sku, identity and zones
// are top-level; the remaining configuration lives under properties.
type fsRequest struct {
	Location   string             `json:"location,omitempty"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Sku        *skuWire           `json:"sku,omitempty"`
	Identity   *identityWire      `json:"identity,omitempty"`
	Zones      []string           `json:"zones,omitempty"`
	Properties *propertiesRequest `json:"properties,omitempty"`
}

// skuWire is the amlFilesystem sku block, top-level on both request and response.
type skuWire struct {
	Name string `json:"name"`
}

// identityWire is the managed-identity block. userAssignedIdentities is a map of
// resource id -> minted {principalId, clientId} (empty object on request).
type identityWire struct {
	Type         string                     `json:"type"`
	TenantID     string                     `json:"tenantId,omitempty"`
	PrincipalID  string                     `json:"principalId,omitempty"`
	UserAssigned map[string]userAssignedVal `json:"userAssignedIdentities,omitempty"`
}

// userAssignedVal is the value side of userAssignedIdentities. On a request it is
// an empty object; on a response it carries the minted ids.
type userAssignedVal struct {
	PrincipalID string `json:"principalId,omitempty"`
	ClientID    string `json:"clientId,omitempty"`
}

// maintenanceWindowWire is the weekly maintenance-window block.
type maintenanceWindowWire struct {
	DayOfWeek    string `json:"dayOfWeek,omitempty"`
	TimeOfDayUTC string `json:"timeOfDayUTC,omitempty"`
}

// hsmWire is the HSM settings/status block.
type hsmWire struct {
	Settings      *hsmSettingsWire `json:"settings,omitempty"`
	ArchiveStatus []archiveWire    `json:"archiveStatus,omitempty"`
}

// hsmSettingsWire is the writable HSM settings.
type hsmSettingsWire struct {
	Container             string   `json:"container,omitempty"`
	ImportPrefix          string   `json:"importPrefix,omitempty"`
	LoggingContainer      string   `json:"loggingContainer,omitempty"`
	ImportPrefixesInitial []string `json:"importPrefixesInitial,omitempty"`
}

// archiveWire is one archive-status entry.
type archiveWire struct {
	FilesystemPath string            `json:"filesystemPath,omitempty"`
	Status         archiveStatusWire `json:"status"`
}

// archiveStatusWire is the status of one archive operation.
type archiveStatusWire struct {
	State              string `json:"state,omitempty"`
	LastStartedTime    string `json:"lastStartedTime,omitempty"`
	LastCompletionTime string `json:"lastCompletionTime,omitempty"`
	PercentComplete    int    `json:"percentComplete,omitempty"`
}

// keyVaultKeyRefWire locates a customer-managed key in Key Vault.
type keyVaultKeyRefWire struct {
	KeyURL      string           `json:"keyUrl,omitempty"`
	SourceVault *sourceVaultWire `json:"sourceVault,omitempty"`
}

// sourceVaultWire is the Key Vault resource id wrapper.
type sourceVaultWire struct {
	ID string `json:"id,omitempty"`
}

// encryptionWire is the encryption-settings block.
type encryptionWire struct {
	KeyEncryptionKey *keyVaultKeyRefWire `json:"keyEncryptionKey,omitempty"`
}

// propertiesRequest is the writable subset of properties. Pointer fields let a
// PATCH overlay only what it names.
type propertiesRequest struct {
	StorageCapacityTiB *float64               `json:"storageCapacityTiB,omitempty"`
	FilesystemSubnet   *string                `json:"filesystemSubnet,omitempty"`
	MaintenanceWindow  *maintenanceWindowWire `json:"maintenanceWindow,omitempty"`
	Hsm                *hsmWire               `json:"hsm,omitempty"`
	EncryptionSettings *encryptionWire        `json:"encryptionSettings,omitempty"`
}

// archiveRequest is the POST /archive body.
type archiveRequest struct {
	FilesystemPath string `json:"filesystemPath,omitempty"`
}

// fsResponse is the ARM representation of an amlFilesystem resource.
type fsResponse struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Location   string             `json:"location"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Sku        *skuWire           `json:"sku,omitempty"`
	Identity   *identityWire      `json:"identity,omitempty"`
	Zones      []string           `json:"zones,omitempty"`
	Properties propertiesResponse `json:"properties"`
}

// clientInfoResponse is the computed client mount information.
type clientInfoResponse struct {
	MgsAddress    string `json:"mgsAddress"`
	LustreVersion string `json:"lustreVersion"`
	MountCommand  string `json:"mountCommand"`
}

// healthResponse is the computed health block.
type healthResponse struct {
	State             string `json:"state"`
	StatusDescription string `json:"statusDescription,omitempty"`
}

// propertiesResponse is the properties block. The computed fields (clientInfo,
// health, provisioningState, throughput) are stable across reads.
type propertiesResponse struct {
	StorageCapacityTiB        float64                `json:"storageCapacityTiB"`
	FilesystemSubnet          string                 `json:"filesystemSubnet,omitempty"`
	MaintenanceWindow         *maintenanceWindowWire `json:"maintenanceWindow,omitempty"`
	Hsm                       *hsmWire               `json:"hsm,omitempty"`
	EncryptionSettings        *encryptionWire        `json:"encryptionSettings,omitempty"`
	ClientInfo                clientInfoResponse     `json:"clientInfo"`
	Health                    healthResponse         `json:"health"`
	ProvisioningState         string                 `json:"provisioningState"`
	ThroughputProvisionedMBps int                    `json:"throughputProvisionedMBps"`
	ClusterUUID               string                 `json:"clusterUuid,omitempty"`
}

// listResponse is the ARM list envelope. nextLink is omitted — the emulator
// returns a single page.
type listResponse struct {
	Value []fsResponse `json:"value"`
}

// toResponse projects a stored resource onto its ARM wire representation.
func toResponse(s *managedlustre.AmlFilesystem) fsResponse {
	out := fsResponse{
		ID:       s.ARMID(),
		Name:     s.Name,
		Type:     armType,
		Location: s.Location,
		Tags:     s.Tags,
		Zones:    s.Zones,
		Properties: propertiesResponse{
			StorageCapacityTiB: s.StorageCapacityTiB,
			FilesystemSubnet:   s.FilesystemSubnet,
			MaintenanceWindow:  maintenanceWindowToWire(s.MaintenanceWindow),
			Hsm:                hsmToWire(s),
			EncryptionSettings: encryptionToWire(s.Encryption),
			ClientInfo: clientInfoResponse{
				MgsAddress:    s.MgsAddress,
				LustreVersion: s.LustreVersion(),
				MountCommand:  s.MountCommand(),
			},
			Health: healthResponse{
				State:             healthStateAvailable,
				StatusDescription: healthDescriptionOK,
			},
			ProvisioningState:         s.ProvisioningState,
			ThroughputProvisionedMBps: s.ThroughputProvisionedMBps,
			ClusterUUID:               s.ClusterUUID,
		},
	}

	if s.Sku != nil {
		out.Sku = &skuWire{Name: s.Sku.Name}
	}

	out.Identity = identityToWire(s.Identity)

	return out
}

// maintenanceWindowToWire projects the stored maintenance window.
func maintenanceWindowToWire(mw *managedlustre.MaintenanceWindow) *maintenanceWindowWire {
	if mw == nil {
		return nil
	}

	return &maintenanceWindowWire{DayOfWeek: mw.DayOfWeek, TimeOfDayUTC: mw.TimeOfDayUTC}
}

// hsmToWire projects the stored HSM settings and archive status.
func hsmToWire(s *managedlustre.AmlFilesystem) *hsmWire {
	if s.Hsm == nil && len(s.ArchiveStatus) == 0 {
		return nil
	}

	out := &hsmWire{}

	if s.Hsm != nil {
		out.Settings = &hsmSettingsWire{
			Container:             s.Hsm.Container,
			ImportPrefix:          s.Hsm.ImportPrefix,
			LoggingContainer:      s.Hsm.LoggingContainer,
			ImportPrefixesInitial: s.Hsm.ImportPrefixesInitial,
		}
	}

	for i := range s.ArchiveStatus {
		a := s.ArchiveStatus[i]
		out.ArchiveStatus = append(out.ArchiveStatus, archiveWire{
			FilesystemPath: a.FilesystemPath,
			Status: archiveStatusWire{
				State:              a.Status.State,
				LastStartedTime:    a.Status.LastStartedTime,
				LastCompletionTime: a.Status.LastCompletionTime,
				PercentComplete:    a.Status.PercentComplete,
			},
		})
	}

	return out
}

// encryptionToWire projects the stored encryption settings.
func encryptionToWire(e *managedlustre.EncryptionSettings) *encryptionWire {
	if e == nil || e.KeyEncryptionKey == nil {
		return nil
	}

	kek := &keyVaultKeyRefWire{KeyURL: e.KeyEncryptionKey.KeyURL}
	if e.KeyEncryptionKey.SourceVault != "" {
		kek.SourceVault = &sourceVaultWire{ID: e.KeyEncryptionKey.SourceVault}
	}

	return &encryptionWire{KeyEncryptionKey: kek}
}

// identityToWire projects the stored managed identity.
func identityToWire(id *managedlustre.Identity) *identityWire {
	if id == nil {
		return nil
	}

	out := &identityWire{Type: id.Type, TenantID: id.TenantID}

	if len(id.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]userAssignedVal, len(id.UserAssigned))
		for k, v := range id.UserAssigned {
			out.UserAssigned[k] = userAssignedVal{PrincipalID: v.PrincipalID, ClientID: v.ClientID}
		}
	}

	return out
}

// inputFromRequest builds a create/update Input from a request body. Pointer and
// slice fields are carried through verbatim so an absent field falls back to the
// stored (or default) value in the driver — which makes a PATCH body, where every
// field is optional, merge correctly on its own.
func inputFromRequest(req *fsRequest) managedlustre.Input {
	in := managedlustre.Input{Tags: req.Tags, Zones: req.Zones}

	if req.Sku != nil {
		in.Sku = &managedlustre.Sku{Name: req.Sku.Name}
	}

	if req.Identity != nil {
		in.Identity = identityFromWire(req.Identity)
	}

	if req.Properties != nil {
		applyProperties(&in, req.Properties)
	}

	return in
}

// applyProperties copies the properties sub-block onto the driver Input.
func applyProperties(in *managedlustre.Input, p *propertiesRequest) {
	in.StorageCapacityTiB = p.StorageCapacityTiB
	in.FilesystemSubnet = p.FilesystemSubnet

	if p.MaintenanceWindow != nil {
		in.MaintenanceWindow = &managedlustre.MaintenanceWindow{
			DayOfWeek:    p.MaintenanceWindow.DayOfWeek,
			TimeOfDayUTC: p.MaintenanceWindow.TimeOfDayUTC,
		}
	}

	if p.Hsm != nil && p.Hsm.Settings != nil {
		in.Hsm = &managedlustre.HsmSettings{
			Container:             p.Hsm.Settings.Container,
			ImportPrefix:          p.Hsm.Settings.ImportPrefix,
			LoggingContainer:      p.Hsm.Settings.LoggingContainer,
			ImportPrefixesInitial: p.Hsm.Settings.ImportPrefixesInitial,
		}
	}

	if p.EncryptionSettings != nil && p.EncryptionSettings.KeyEncryptionKey != nil {
		in.Encryption = encryptionFromWire(p.EncryptionSettings)
	}
}

// identityFromWire maps a request identity block to the driver identity.
func identityFromWire(w *identityWire) *managedlustre.Identity {
	out := &managedlustre.Identity{Type: w.Type}

	if len(w.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]managedlustre.UserAssignedValue, len(w.UserAssigned))
		for k := range w.UserAssigned {
			out.UserAssigned[k] = managedlustre.UserAssignedValue{}
		}
	}

	return out
}

// encryptionFromWire maps a request encryption block to the driver settings.
func encryptionFromWire(w *encryptionWire) *managedlustre.EncryptionSettings {
	kek := &managedlustre.KeyVaultKeyReference{KeyURL: w.KeyEncryptionKey.KeyURL}
	if w.KeyEncryptionKey.SourceVault != nil {
		kek.SourceVault = w.KeyEncryptionKey.SourceVault.ID
	}

	return &managedlustre.EncryptionSettings{KeyEncryptionKey: kek}
}
