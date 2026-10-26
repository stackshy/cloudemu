package kms

import (
	"context"
	"regexp"
	"sort"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

// Field constraints KMS models for custom key store parameters.
var (
	cloudHsmClusterIDPattern = regexp.MustCompile(`^cluster-[2-7a-zA-Z]{11,16}$`)
	xksURIEndpointPattern    = regexp.MustCompile(`^https://[a-zA-Z0-9.-]+$`)
	xksURIPathPattern        = regexp.MustCompile(`^(/[a-zA-Z0-9/_-]+/kms/xks/v\d{1,2})$|^(/kms/xks/v\d{1,2})$`)
	xksVpcServicePattern     = regexp.MustCompile(`^(com|eu)\.amazonaws\.vpce\.([a-z]+-){2,3}\d+\.vpce-svc-[0-9a-z]+$`)
	xksAccessKeyIDPattern    = regexp.MustCompile(`^[A-Z2-7]+$`)
	xksSecretPattern         = regexp.MustCompile(`^[a-zA-Z0-9/+=]+$`)
	xksKeyIDPattern          = regexp.MustCompile(`^[a-zA-Z0-9-_.]+$`)
)

const (
	maxCustomKeyStoreNameLen = 256
	minKeyStorePasswordLen   = 7
	maxKeyStorePasswordLen   = 32
	maxTrustAnchorLen        = 5000
	minXksURILen             = 10
	maxXksURILen             = 128
	minXksVpcServiceLen      = 20
	maxXksVpcServiceLen      = 64
	minXksAccessKeyIDLen     = 20
	maxXksAccessKeyIDLen     = 30
	minXksSecretLen          = 43
	maxXksSecretLen          = 64
	maxXksKeyIDLen           = 128
)

func invalidParam(format string, args ...any) error {
	return errors.Newf(errors.InvalidArgument, format, args...)
}

func cksNotFound(ref string) error {
	return driver.NewAPIError(driver.ExCustomKeyStoreNotFound, errors.NotFound,
		"Custom key store %s does not exist", ref)
}

func cksInvalidState(format string, args ...any) error {
	return driver.NewAPIError(driver.ExCustomKeyStoreInvalidState, errors.FailedPrecondition, format, args...)
}

func isCustomKeyStoreOrigin(origin string) bool {
	return origin == driver.OriginAWSCloudHSM || origin == driver.OriginExternalKeyStore
}

// customKeyStoreConnected reports whether the store id exists and is CONNECTED.
func (m *Mock) customKeyStoreConnected(id string) bool {
	s, ok := m.customKeyStores.Get(id)

	return ok && s.ConnectionState == driver.ConnectionStateConnected
}

// keyUnavailable reports whether a key is in the Unavailable state: it lives in
// a custom key store that is not CONNECTED. A PendingDeletion key keeps that
// state while its store is disconnected, so its deletion can still be
// canceled. Caller holds kd.mu.
func (m *Mock) keyUnavailable(kd *keyData) bool {
	return kd.meta.CustomKeyStoreID != "" && kd.meta.KeyState != driver.StatePendingDeletion &&
		!m.customKeyStoreConnected(kd.meta.CustomKeyStoreID)
}

// keyMetadata returns the key's metadata as KMS reports it. The stored state
// is kept as is (EnableKey/DisableKey on an Unavailable key take effect once
// the store reconnects); only the reported state is Unavailable while the
// key's store is not connected. Caller holds kd.mu.
func (m *Mock) keyMetadata(kd *keyData) driver.KeyMetadata {
	out := kd.meta

	if m.keyUnavailable(kd) {
		out.KeyState = driver.StateUnavailable
		out.Enabled = false
	}

	return out
}

// keyGone reports whether a key no longer counts as living in its store: real
// KMS deletes a PendingDeletion key once its waiting period ends.
func (m *Mock) keyGone(kd *keyData) bool {
	return kd.meta.KeyState == driver.StatePendingDeletion && !kd.meta.DeletionDate.After(m.now())
}

// customKeyStoreForNewKey validates CreateKey's custom key store placement and
// returns the target store (nil for a key outside any store). The caller holds
// cksMu whenever a store or store origin is involved.
//
//nolint:gocyclo // a flat list of the CreateKey custom-store rules KMS documents
func (m *Mock) customKeyStoreForNewKey(in *driver.CreateKeyInput, origin, spec, usage string) (*driver.CustomKeyStore, error) {
	if in.CustomKeyStoreID == "" {
		if isCustomKeyStoreOrigin(origin) {
			return nil, invalidParam("CustomKeyStoreId is required when Origin is %s", origin)
		}

		if in.XksKeyID != "" {
			return nil, invalidParam("XksKeyId is valid only for keys with Origin EXTERNAL_KEY_STORE")
		}

		return nil, nil
	}

	store, ok := m.customKeyStores.Get(in.CustomKeyStoreID)
	if !ok {
		return nil, cksNotFound(in.CustomKeyStoreID)
	}

	wantOrigin := driver.OriginAWSCloudHSM
	if store.CustomKeyStoreType == driver.CustomKeyStoreTypeExternal {
		wantOrigin = driver.OriginExternalKeyStore
	}

	if origin != wantOrigin {
		return nil, invalidParam("Origin must be %s to create a key in custom key store %s",
			wantOrigin, in.CustomKeyStoreID)
	}

	if spec != driver.SpecSymmetricDefault || usage != driver.UsageEncryptDecrypt || in.MultiRegion {
		return nil, driver.WithMessage(driver.ErrUnsupportedOperation,
			"Custom key stores support only single-Region symmetric encryption keys "+
				"(KeySpec SYMMETRIC_DEFAULT, KeyUsage ENCRYPT_DECRYPT, MultiRegion false)")
	}

	if store.ConnectionState != driver.ConnectionStateConnected {
		return nil, cksInvalidState("Custom key store %s is not connected (ConnectionState %s)",
			in.CustomKeyStoreID, store.ConnectionState)
	}

	if store.CustomKeyStoreType != driver.CustomKeyStoreTypeExternal {
		if in.XksKeyID != "" {
			return nil, invalidParam("XksKeyId is valid only for keys with Origin EXTERNAL_KEY_STORE")
		}

		return store, nil
	}

	if in.XksKeyID == "" {
		return nil, invalidParam("XksKeyId is required when Origin is EXTERNAL_KEY_STORE")
	}

	if len(in.XksKeyID) > maxXksKeyIDLen || !xksKeyIDPattern.MatchString(in.XksKeyID) {
		return nil, invalidParam("XksKeyId %q is not valid", in.XksKeyID)
	}

	for _, kd := range m.keys.All() {
		kd.mu.RLock()
		inUse := kd.meta.CustomKeyStoreID == store.CustomKeyStoreID && kd.meta.XksKeyID == in.XksKeyID && !m.keyGone(kd)
		kd.mu.RUnlock()

		if inUse {
			return nil, driver.NewAPIError(driver.ExXksKeyAlreadyInUse, errors.AlreadyExists,
				"External key %s is already associated with a KMS key in this external key store", in.XksKeyID)
		}
	}

	return store, nil
}

// CreateCustomKeyStore creates a DISCONNECTED CloudHSM or external key store.
//
//nolint:gocritic // in is the public input, taken by value to match the driver API
func (m *Mock) CreateCustomKeyStore(_ context.Context, in driver.CreateCustomKeyStoreInput) (string, error) {
	storeType := in.CustomKeyStoreType
	if storeType == "" {
		storeType = driver.CustomKeyStoreTypeCloudHSM
	}

	s := &driver.CustomKeyStore{
		CustomKeyStoreName:     in.CustomKeyStoreName,
		CustomKeyStoreType:     storeType,
		CloudHsmClusterID:      in.CloudHsmClusterID,
		TrustAnchorCertificate: in.TrustAnchorCertificate,
		KeyStorePassword:       in.KeyStorePassword,
		ConnectionState:        driver.ConnectionStateDisconnected,
	}

	if err := validateNewStore(s, &in); err != nil {
		return "", err
	}

	m.cksMu.Lock()
	defer m.cksMu.Unlock()

	if err := m.checkStoreUniqueness(s); err != nil {
		return "", err
	}

	id, err := idgen.GenerateLongID("cks-")
	if err != nil {
		return "", errors.Newf(errors.Internal, "generate custom key store id: %v", err)
	}

	s.CustomKeyStoreID = id
	s.CreationDate = m.now()
	m.customKeyStores.Set(id, s)

	return id, nil
}

// validateNewStore checks the type-specific required parameters of a new store
// and fills in its XKS proxy configuration.
//
//nolint:gocyclo // a flat list of the per-type required/forbidden parameters KMS documents
func validateNewStore(s *driver.CustomKeyStore, in *driver.CreateCustomKeyStoreInput) error {
	if err := validateStoreName(in.CustomKeyStoreName); err != nil {
		return err
	}

	switch s.CustomKeyStoreType {
	case driver.CustomKeyStoreTypeCloudHSM:
		if in.XksProxyURIEndpoint != "" || in.XksProxyURIPath != "" || in.XksProxyConnectivity != "" ||
			in.XksProxyVpcEndpointServiceName != "" || in.XksProxyVpcEndpointServiceOwner != "" ||
			in.XksProxyAuthenticationCredential != nil {
			return invalidParam("XksProxy parameters are valid only for custom key stores of type EXTERNAL_KEY_STORE")
		}

		if in.CloudHsmClusterID == "" || in.TrustAnchorCertificate == "" || in.KeyStorePassword == "" {
			return invalidParam("CloudHsmClusterId, TrustAnchorCertificate, and KeyStorePassword are required " +
				"for a custom key store of type AWS_CLOUDHSM")
		}

		return validateCloudHSMFields(in.CloudHsmClusterID, in.TrustAnchorCertificate, in.KeyStorePassword)
	case driver.CustomKeyStoreTypeExternal:
		if in.CloudHsmClusterID != "" || in.TrustAnchorCertificate != "" || in.KeyStorePassword != "" {
			return invalidParam("CloudHsmClusterId, TrustAnchorCertificate, and KeyStorePassword are valid only " +
				"for custom key stores of type AWS_CLOUDHSM")
		}

		if in.XksProxyURIEndpoint == "" || in.XksProxyURIPath == "" || in.XksProxyConnectivity == "" ||
			in.XksProxyAuthenticationCredential == nil {
			return invalidParam("XksProxyUriEndpoint, XksProxyUriPath, XksProxyConnectivity, and " +
				"XksProxyAuthenticationCredential are required for a custom key store of type EXTERNAL_KEY_STORE")
		}

		s.XksProxyConfiguration = &driver.XksProxyConfiguration{
			Connectivity:            in.XksProxyConnectivity,
			AccessKeyID:             in.XksProxyAuthenticationCredential.AccessKeyID,
			URIEndpoint:             in.XksProxyURIEndpoint,
			URIPath:                 in.XksProxyURIPath,
			VpcEndpointServiceName:  in.XksProxyVpcEndpointServiceName,
			VpcEndpointServiceOwner: in.XksProxyVpcEndpointServiceOwner,
		}
		s.XksRawSecretAccessKey = in.XksProxyAuthenticationCredential.RawSecretAccessKey

		return validateXksConfig(s.XksProxyConfiguration, in.XksProxyAuthenticationCredential)
	default:
		return invalidParam("CustomKeyStoreType %q is not valid; expected AWS_CLOUDHSM or EXTERNAL_KEY_STORE",
			s.CustomKeyStoreType)
	}
}

func validateStoreName(name string) error {
	if name == "" || len(name) > maxCustomKeyStoreNameLen {
		return invalidParam("CustomKeyStoreName must be 1-%d characters", maxCustomKeyStoreNameLen)
	}

	return nil
}

func validateCloudHSMFields(clusterID, trustAnchor, password string) error {
	if clusterID != "" && !cloudHsmClusterIDPattern.MatchString(clusterID) {
		return invalidParam("CloudHsmClusterId %q is not valid", clusterID)
	}

	if len(trustAnchor) > maxTrustAnchorLen {
		return invalidParam("TrustAnchorCertificate must be at most %d characters", maxTrustAnchorLen)
	}

	if password != "" && (len(password) < minKeyStorePasswordLen || len(password) > maxKeyStorePasswordLen) {
		return invalidParam("KeyStorePassword must be %d-%d characters", minKeyStorePasswordLen, maxKeyStorePasswordLen)
	}

	return nil
}

// validateXksConfig checks an external key store proxy configuration (and a
// credential, when one is being set).
//
//nolint:gocyclo // a flat list of the modeled XKS field constraints
func validateXksConfig(cfg *driver.XksProxyConfiguration, cred *driver.XksProxyAuthenticationCredential) error {
	switch cfg.Connectivity {
	case driver.XksConnectivityPublicEndpoint:
		if cfg.VpcEndpointServiceName != "" || cfg.VpcEndpointServiceOwner != "" {
			return invalidParam("XksProxyVpcEndpointServiceName is valid only when XksProxyConnectivity is VPC_ENDPOINT_SERVICE")
		}
	case driver.XksConnectivityVpcEndpointService:
		if cfg.VpcEndpointServiceName == "" {
			return invalidParam("XksProxyVpcEndpointServiceName is required when XksProxyConnectivity is VPC_ENDPOINT_SERVICE")
		}
	default:
		return invalidParam("XksProxyConnectivity %q is not valid; expected PUBLIC_ENDPOINT or VPC_ENDPOINT_SERVICE",
			cfg.Connectivity)
	}

	if !lenIn(cfg.URIEndpoint, minXksURILen, maxXksURILen) || !xksURIEndpointPattern.MatchString(cfg.URIEndpoint) {
		return invalidParam("XksProxyUriEndpoint %q is not valid: it must be https:// followed by a host name", cfg.URIEndpoint)
	}

	if !lenIn(cfg.URIPath, minXksURILen, maxXksURILen) || !xksURIPathPattern.MatchString(cfg.URIPath) {
		return invalidParam("XksProxyUriPath %q is not valid: it must end with /kms/xks/v<version>", cfg.URIPath)
	}

	if cfg.VpcEndpointServiceName != "" && (!lenIn(cfg.VpcEndpointServiceName, minXksVpcServiceLen, maxXksVpcServiceLen) ||
		!xksVpcServicePattern.MatchString(cfg.VpcEndpointServiceName)) {
		return invalidParam("XksProxyVpcEndpointServiceName %q is not valid", cfg.VpcEndpointServiceName)
	}

	if cfg.VpcEndpointServiceOwner != "" && !accountIDPattern.MatchString(cfg.VpcEndpointServiceOwner) {
		return invalidParam("XksProxyVpcEndpointServiceOwner %q is not a 12-digit account ID", cfg.VpcEndpointServiceOwner)
	}

	if cred == nil {
		return nil
	}

	if !lenIn(cred.AccessKeyID, minXksAccessKeyIDLen, maxXksAccessKeyIDLen) || !xksAccessKeyIDPattern.MatchString(cred.AccessKeyID) {
		return invalidParam("XksProxyAuthenticationCredential.AccessKeyId must be %d-%d characters of A-Z and 2-7",
			minXksAccessKeyIDLen, maxXksAccessKeyIDLen)
	}

	if !lenIn(cred.RawSecretAccessKey, minXksSecretLen, maxXksSecretLen) || !xksSecretPattern.MatchString(cred.RawSecretAccessKey) {
		return invalidParam("XksProxyAuthenticationCredential.RawSecretAccessKey must be %d-%d characters of "+
			"a-z, A-Z, 0-9, /, +, and =", minXksSecretLen, maxXksSecretLen)
	}

	return nil
}

func lenIn(s string, lo, hi int) bool {
	return len(s) >= lo && len(s) <= hi
}

// checkStoreUniqueness enforces the account/Region uniqueness rules KMS
// documents for custom key stores against every other store. Caller holds
// cksMu.
func (m *Mock) checkStoreUniqueness(s *driver.CustomKeyStore) error {
	for id, other := range m.customKeyStores.All() {
		if id == s.CustomKeyStoreID {
			continue
		}

		if other.CustomKeyStoreName == s.CustomKeyStoreName {
			return driver.NewAPIError(driver.ExCustomKeyStoreNameInUse, errors.AlreadyExists,
				"Custom key store name %s is already in use", s.CustomKeyStoreName)
		}

		if s.CloudHsmClusterID != "" && other.CloudHsmClusterID == s.CloudHsmClusterID {
			return driver.NewAPIError(driver.ExCloudHsmClusterInUse, errors.AlreadyExists,
				"CloudHSM cluster %s is already associated with custom key store %s", s.CloudHsmClusterID, id)
		}

		if err := xksConflict(s.XksProxyConfiguration, other.XksProxyConfiguration); err != nil {
			return err
		}
	}

	return nil
}

// xksConflict reports a proxy-address clash between two external key stores:
// the endpoint+path pair must be unique, a VPC_ENDPOINT_SERVICE store's
// endpoint (private DNS name) and endpoint service must be unique, and a public
// store can't reuse a VPC store's endpoint.
func xksConflict(mine, other *driver.XksProxyConfiguration) error {
	if mine == nil || other == nil {
		return nil
	}

	if mine.URIEndpoint == other.URIEndpoint && mine.URIPath == other.URIPath {
		return driver.NewAPIError(driver.ExXksProxyURIInUse, errors.AlreadyExists,
			"XksProxyUriEndpoint and XksProxyUriPath %s%s are already associated with another external key store",
			mine.URIEndpoint, mine.URIPath)
	}

	vpc := mine.Connectivity == driver.XksConnectivityVpcEndpointService ||
		other.Connectivity == driver.XksConnectivityVpcEndpointService
	if vpc && mine.URIEndpoint == other.URIEndpoint {
		return driver.NewAPIError(driver.ExXksProxyURIEndpointInUse, errors.AlreadyExists,
			"XksProxyUriEndpoint %s is already associated with another external key store", mine.URIEndpoint)
	}

	if mine.VpcEndpointServiceName != "" && mine.VpcEndpointServiceName == other.VpcEndpointServiceName {
		return driver.NewAPIError(driver.ExXksProxyVpcEndpointServiceInUse, errors.AlreadyExists,
			"VPC endpoint service %s is already associated with another external key store", mine.VpcEndpointServiceName)
	}

	return nil
}

// DescribeCustomKeyStores lists every store, or the one named by id or name.
func (m *Mock) DescribeCustomKeyStores(_ context.Context, id, name string) ([]driver.CustomKeyStore, error) {
	if id != "" && name != "" {
		return nil, invalidParam("specify either CustomKeyStoreId or CustomKeyStoreName, not both")
	}

	m.cksMu.Lock()
	defer m.cksMu.Unlock()

	if id != "" {
		s, ok := m.customKeyStores.Get(id)
		if !ok {
			return nil, cksNotFound(id)
		}

		return []driver.CustomKeyStore{copyStore(s)}, nil
	}

	out := make([]driver.CustomKeyStore, 0, m.customKeyStores.Len())

	for _, s := range m.customKeyStores.All() {
		if name == "" || s.CustomKeyStoreName == name {
			out = append(out, copyStore(s))
		}
	}

	if name != "" && len(out) == 0 {
		return nil, cksNotFound(name)
	}

	sortStores(out)

	return out, nil
}

// sortStores orders stores by creation date then ID, so paging is stable.
func sortStores(out []driver.CustomKeyStore) {
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreationDate.Equal(out[j].CreationDate) {
			return out[i].CreationDate.Before(out[j].CreationDate)
		}

		return out[i].CustomKeyStoreID < out[j].CustomKeyStoreID
	})
}

func copyStore(s *driver.CustomKeyStore) driver.CustomKeyStore {
	out := *s

	if s.XksProxyConfiguration != nil {
		cfg := *s.XksProxyConfiguration
		out.XksProxyConfiguration = &cfg
	}

	return out
}

// UpdateCustomKeyStore changes a store's properties. The name (and, for an
// external key store, the proxy credential and URI path) can change while the
// store is CONNECTED or DISCONNECTED; every other property requires
// DISCONNECTED.
//
//nolint:gocritic // in is the public input, taken by value to match the driver API
func (m *Mock) UpdateCustomKeyStore(_ context.Context, in driver.UpdateCustomKeyStoreInput) error {
	m.cksMu.Lock()
	defer m.cksMu.Unlock()

	cur, ok := m.customKeyStores.Get(in.CustomKeyStoreID)
	if !ok {
		return cksNotFound(in.CustomKeyStoreID)
	}

	next := copyStore(cur)

	if in.NewCustomKeyStoreName != "" {
		if err := validateStoreName(in.NewCustomKeyStoreName); err != nil {
			return err
		}

		next.CustomKeyStoreName = in.NewCustomKeyStoreName
	}

	var err error
	if cur.CustomKeyStoreType == driver.CustomKeyStoreTypeExternal {
		err = applyXksUpdate(&next, &in)
	} else {
		err = applyCloudHSMUpdate(&next, &in)
	}

	if err != nil {
		return err
	}

	if err := m.checkStoreUniqueness(&next); err != nil {
		return err
	}

	m.customKeyStores.Set(next.CustomKeyStoreID, &next)

	return nil
}

func requireDisconnected(s *driver.CustomKeyStore, what string) error {
	if s.ConnectionState != driver.ConnectionStateDisconnected {
		return cksInvalidState("Custom key store %s must be DISCONNECTED to update %s (ConnectionState %s)",
			s.CustomKeyStoreID, what, s.ConnectionState)
	}

	return nil
}

func requireConnectedOrDisconnected(s *driver.CustomKeyStore, what string) error {
	if s.ConnectionState != driver.ConnectionStateDisconnected && s.ConnectionState != driver.ConnectionStateConnected {
		return cksInvalidState("Custom key store %s must be CONNECTED or DISCONNECTED to update %s (ConnectionState %s)",
			s.CustomKeyStoreID, what, s.ConnectionState)
	}

	return nil
}

//nolint:gocyclo // a flat list of the CloudHSM update rules KMS documents
func applyCloudHSMUpdate(s *driver.CustomKeyStore, in *driver.UpdateCustomKeyStoreInput) error {
	if in.XksProxyURIEndpoint != "" || in.XksProxyURIPath != "" || in.XksProxyConnectivity != "" ||
		in.XksProxyVpcEndpointServiceName != "" || in.XksProxyVpcEndpointServiceOwner != "" ||
		in.XksProxyAuthenticationCredential != nil {
		return invalidParam("XksProxy parameters are valid only for custom key stores of type EXTERNAL_KEY_STORE")
	}

	if in.KeyStorePassword == "" && in.CloudHsmClusterID == "" {
		return nil
	}

	if err := requireDisconnected(s, "KeyStorePassword or CloudHsmClusterId"); err != nil {
		return err
	}

	if err := validateCloudHSMFields(in.CloudHsmClusterID, "", in.KeyStorePassword); err != nil {
		return err
	}

	if in.KeyStorePassword != "" {
		s.KeyStorePassword = in.KeyStorePassword
	}

	if in.CloudHsmClusterID != "" {
		s.CloudHsmClusterID = in.CloudHsmClusterID
	}

	return nil
}

//nolint:gocyclo // a flat list of the XKS update rules KMS documents
func applyXksUpdate(s *driver.CustomKeyStore, in *driver.UpdateCustomKeyStoreInput) error {
	if in.KeyStorePassword != "" || in.CloudHsmClusterID != "" {
		return invalidParam("KeyStorePassword and CloudHsmClusterId are valid only for custom key stores of type AWS_CLOUDHSM")
	}

	cfg := s.XksProxyConfiguration
	if cfg == nil {
		cfg = &driver.XksProxyConfiguration{}
		s.XksProxyConfiguration = cfg
	}

	if in.XksProxyAuthenticationCredential != nil || in.XksProxyURIPath != "" {
		if err := requireConnectedOrDisconnected(s, "XksProxyAuthenticationCredential or XksProxyUriPath"); err != nil {
			return err
		}
	}

	if in.XksProxyURIEndpoint != "" || in.XksProxyConnectivity != "" ||
		in.XksProxyVpcEndpointServiceName != "" || in.XksProxyVpcEndpointServiceOwner != "" {
		if err := requireDisconnected(s, "the external key store proxy connection settings"); err != nil {
			return err
		}
	}

	if err := applyXksConnectivity(cfg, in); err != nil {
		return err
	}

	if in.XksProxyURIEndpoint != "" {
		cfg.URIEndpoint = in.XksProxyURIEndpoint
	}

	if in.XksProxyURIPath != "" {
		cfg.URIPath = in.XksProxyURIPath
	}

	if in.XksProxyVpcEndpointServiceName != "" {
		cfg.VpcEndpointServiceName = in.XksProxyVpcEndpointServiceName
	}

	if in.XksProxyVpcEndpointServiceOwner != "" {
		cfg.VpcEndpointServiceOwner = in.XksProxyVpcEndpointServiceOwner
	}

	if c := in.XksProxyAuthenticationCredential; c != nil {
		cfg.AccessKeyID = c.AccessKeyID
		s.XksRawSecretAccessKey = c.RawSecretAccessKey
	}

	return validateXksConfig(cfg, in.XksProxyAuthenticationCredential)
}

// applyXksConnectivity switches the proxy connectivity. Moving to
// VPC_ENDPOINT_SERVICE needs a new endpoint and an endpoint service name;
// moving to PUBLIC_ENDPOINT needs a new endpoint and drops the service name.
func applyXksConnectivity(cfg *driver.XksProxyConfiguration, in *driver.UpdateCustomKeyStoreInput) error {
	if in.XksProxyConnectivity == "" || in.XksProxyConnectivity == cfg.Connectivity {
		return nil
	}

	switch in.XksProxyConnectivity {
	case driver.XksConnectivityVpcEndpointService:
		if in.XksProxyURIEndpoint == "" || in.XksProxyVpcEndpointServiceName == "" {
			return invalidParam("changing XksProxyConnectivity to VPC_ENDPOINT_SERVICE requires " +
				"XksProxyUriEndpoint and XksProxyVpcEndpointServiceName")
		}
	case driver.XksConnectivityPublicEndpoint:
		if in.XksProxyURIEndpoint == "" {
			return invalidParam("changing XksProxyConnectivity to PUBLIC_ENDPOINT requires XksProxyUriEndpoint")
		}

		cfg.VpcEndpointServiceName = ""
		cfg.VpcEndpointServiceOwner = ""
	}

	cfg.Connectivity = in.XksProxyConnectivity

	return nil
}

// DeleteCustomKeyStore deletes a DISCONNECTED store that holds no KMS keys.
func (m *Mock) DeleteCustomKeyStore(_ context.Context, id string) error {
	m.cksMu.Lock()
	defer m.cksMu.Unlock()

	s, ok := m.customKeyStores.Get(id)
	if !ok {
		return cksNotFound(id)
	}

	if s.ConnectionState != driver.ConnectionStateDisconnected {
		return cksInvalidState("Custom key store %s must be DISCONNECTED before it can be deleted (ConnectionState %s)",
			id, s.ConnectionState)
	}

	for _, kd := range m.keys.All() {
		kd.mu.RLock()
		holds := kd.meta.CustomKeyStoreID == id && !m.keyGone(kd)
		kd.mu.RUnlock()

		if holds {
			return driver.NewAPIError(driver.ExCustomKeyStoreHasCMKs, errors.FailedPrecondition,
				"Custom key store %s contains KMS keys; delete them before deleting the key store", id)
		}
	}

	m.customKeyStores.Delete(id)

	return nil
}

// ConnectCustomKeyStore connects a store. cloudemu has no backing CloudHSM
// cluster or XKS proxy to reach, so the connection completes immediately
// (DescribeCustomKeyStores reports CONNECTED). Connecting a store that is
// DISCONNECTING or FAILED is rejected, as in KMS.
func (m *Mock) ConnectCustomKeyStore(_ context.Context, id string) error {
	return m.transitionStore(id, func(s *driver.CustomKeyStore) error {
		switch s.ConnectionState {
		case driver.ConnectionStateDisconnecting, driver.ConnectionStateFailed:
			return cksInvalidState("Custom key store %s cannot be connected from ConnectionState %s",
				id, s.ConnectionState)
		}

		s.ConnectionState = driver.ConnectionStateConnected
		s.ConnectionErrorCode = ""

		return nil
	})
}

// DisconnectCustomKeyStore disconnects a store; a store already DISCONNECTED or
// DISCONNECTING is rejected, as in KMS.
func (m *Mock) DisconnectCustomKeyStore(_ context.Context, id string) error {
	return m.transitionStore(id, func(s *driver.CustomKeyStore) error {
		switch s.ConnectionState {
		case driver.ConnectionStateDisconnected, driver.ConnectionStateDisconnecting:
			return cksInvalidState("Custom key store %s cannot be disconnected from ConnectionState %s",
				id, s.ConnectionState)
		}

		s.ConnectionState = driver.ConnectionStateDisconnected
		s.ConnectionErrorCode = ""

		return nil
	})
}

func (m *Mock) transitionStore(id string, fn func(*driver.CustomKeyStore) error) error {
	m.cksMu.Lock()
	defer m.cksMu.Unlock()

	cur, ok := m.customKeyStores.Get(id)
	if !ok {
		return cksNotFound(id)
	}

	next := copyStore(cur)
	if err := fn(&next); err != nil {
		return err
	}

	m.customKeyStores.Set(id, &next)

	return nil
}

// GenerateRandomInCustomKeyStore generates random bytes in a custom key
// store's CloudHSM cluster. KMS rejects an external key store with
// UnsupportedOperationException and a store that isn't CONNECTED with
// CustomKeyStoreInvalidStateException.
func (m *Mock) GenerateRandomInCustomKeyStore(ctx context.Context, id string, numberOfBytes int32) ([]byte, error) {
	if err := validateRandomBytes(numberOfBytes); err != nil {
		return nil, err
	}

	s, ok := m.customKeyStores.Get(id)
	if !ok {
		return nil, cksNotFound(id)
	}

	if s.CustomKeyStoreType == driver.CustomKeyStoreTypeExternal {
		return nil, driver.WithMessage(driver.ErrUnsupportedOperation,
			"GenerateRandom is not supported for external key store %s", id)
	}

	if s.ConnectionState != driver.ConnectionStateConnected {
		return nil, cksInvalidState("Custom key store %s is not connected (ConnectionState %s)", id, s.ConnectionState)
	}

	return m.GenerateRandom(ctx, numberOfBytes)
}
