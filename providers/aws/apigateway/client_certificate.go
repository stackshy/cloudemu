package apigateway

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

const (
	msgCertNotFound = "Invalid Client Certificate identifier specified"

	// certValidity is how long an API Gateway generated certificate is valid.
	certValidity = 365 * 24 * time.Hour

	certKeyBits    = 2048
	certSerialBits = 128
)

// GenerateClientCertificate creates a self-signed RSA certificate. Only the
// public certificate is kept, as API Gateway never exposes the private key.
func (m *Mock) GenerateClientCertificate(
	_ context.Context, in driver.GenerateClientCertificateInput,
) (*driver.ClientCertificate, error) {
	id := genShortID()
	notBefore := m.opts.Clock.Now().UTC().Truncate(time.Second)
	notAfter := notBefore.Add(certValidity)

	pemCert, err := selfSignedPEM(id, notBefore, notAfter)
	if err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "generate client certificate: %v", err)
	}

	cc := &driver.ClientCertificate{
		ID: id, Description: in.Description, PEMEncodedCertificate: pemCert,
		CreatedDate: notBefore.Unix(), ExpirationDate: notAfter.Unix(), Tags: copyStrMap(in.Tags),
	}

	m.regionMu.Lock()
	m.certs[id] = cc
	m.regionMu.Unlock()

	out := copyCert(cc)

	return &out, nil
}

// selfSignedPEM issues a certificate whose subject names the certificate id,
// signed by its own fresh key.
func selfSignedPEM(id string, notBefore, notAfter time.Time) (string, error) {
	key, err := rsa.GenerateKey(rand.Reader, certKeyBits)
	if err != nil {
		return "", err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), certSerialBits))
	if err != nil {
		return "", err
	}

	name := pkix.Name{
		Country: []string{"US"}, Province: []string{"Washington"}, Locality: []string{"Seattle"},
		Organization: []string{"Amazon.com"}, OrganizationalUnit: []string{"ApiGateway"}, CommonName: id,
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: name, Issuer: name,
		NotBefore: notBefore, NotAfter: notAfter,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", err
	}

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), nil
}

// GetClientCertificate returns one certificate.
func (m *Mock) GetClientCertificate(_ context.Context, id string) (*driver.ClientCertificate, error) {
	m.regionMu.RLock()
	defer m.regionMu.RUnlock()

	cc, ok := m.certs[id]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgCertNotFound)
	}

	out := copyCert(cc)

	return &out, nil
}

// GetClientCertificates lists certificates oldest first, one page at a time.
func (m *Mock) GetClientCertificates(_ context.Context, page driver.PageInput) (*driver.ClientCertificatePage, error) {
	m.regionMu.RLock()

	all := make([]driver.ClientCertificate, 0, len(m.certs))
	for _, cc := range m.certs {
		all = append(all, copyCert(cc))
	}

	m.regionMu.RUnlock()

	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedDate != all[j].CreatedDate {
			return all[i].CreatedDate < all[j].CreatedDate
		}

		return all[i].ID < all[j].ID
	})

	items, next, err := pageOf(all, page)
	if err != nil {
		return nil, err
	}

	return &driver.ClientCertificatePage{Items: items, Position: next}, nil
}

// UpdateClientCertificate applies a patch document; only /description can
// change.
func (m *Mock) UpdateClientCertificate(
	_ context.Context, id string, ops []driver.PatchOperation,
) (*driver.ClientCertificate, error) {
	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	cc, ok := m.certs[id]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgCertNotFound)
	}

	desc := cc.Description

	for _, op := range ops {
		if op.Path != pathDescription {
			return nil, invalidPatchPath(op, pathDescription)
		}

		desc = op.Value
		if op.Op == opRemove {
			desc = ""
		}
	}

	cc.Description = desc
	out := copyCert(cc)

	return &out, nil
}

// DeleteClientCertificate removes a certificate that no stage references. The
// scan holds regionMu for writing, so no stage can attach it mid-delete.
func (m *Mock) DeleteClientCertificate(_ context.Context, id string) error {
	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	if _, ok := m.certs[id]; !ok {
		return cerrors.New(cerrors.NotFound, msgCertNotFound)
	}

	if users := m.stagesUsingCert(id); len(users) > 0 {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Cannot delete client certificate %s because it is in use by stage(s): %s", id, strings.Join(users, ", "))
	}

	delete(m.certs, id)

	return nil
}

// stagesUsingCert lists "apiId/stage" for every stage pointing at the
// certificate. The caller holds regionMu.
func (m *Mock) stagesUsingCert(id string) []string {
	var users []string

	for apiID, ad := range m.apis.All() {
		ad.mu.RLock()

		for name, st := range ad.stages {
			if st.ClientCertificateID == id {
				users = append(users, fmt.Sprintf("%s/%s", apiID, name))
			}
		}

		ad.mu.RUnlock()
	}

	sort.Strings(users)

	return users
}

// invalidPatchPath is the BadRequest API Gateway returns for a patch op on a
// path the resource does not allow.
func invalidPatchPath(op driver.PatchOperation, allowed ...string) error {
	return cerrors.Newf(cerrors.InvalidArgument,
		"Invalid patch path '%s' specified for op '%s'. Must be one of: [%s]", op.Path, op.Op, strings.Join(allowed, ", "))
}

func copyCert(cc *driver.ClientCertificate) driver.ClientCertificate {
	out := *cc
	out.Tags = copyStrMap(cc.Tags)

	return out
}
