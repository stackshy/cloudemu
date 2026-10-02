package managedidentity

import (
	"context"
	"regexp"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// ficSegment is the child type segment of a federated identity credential.
const ficSegment = "/federatedidentitycredentials/"

// maxFICsPerIdentity is the documented limit of credentials per identity.
const maxFICsPerIdentity = 20

// ficNamePattern is the documented credential name rule: 3 to 120 characters,
// starting with a letter or digit, then letters, digits, '-' or '_'.
var ficNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{2,119}$`)

// FederatedCredential is a federated identity credential stored under a
// user-assigned identity.
type FederatedCredential struct {
	Subscription  string   `json:"subscription"`
	ResourceGroup string   `json:"resourceGroup"`
	Identity      string   `json:"identity"`
	Name          string   `json:"name"`
	Issuer        string   `json:"issuer"`
	Subject       string   `json:"subject"`
	Audiences     []string `json:"audiences"`
}

// FICInput carries the writable properties of a credential.
type FICInput struct {
	Issuer    string
	Subject   string
	Audiences []string
}

func ficKey(sub, rg, identity, name string) string {
	return key(sub, rg, identity) + ficSegment + strings.ToLower(name)
}

func cloneFIC(f *FederatedCredential) FederatedCredential {
	out := *f
	out.Audiences = append([]string(nil), f.Audiences...)

	return out
}

func validateFIC(name string, in *FICInput) error {
	switch {
	case !ficNamePattern.MatchString(name):
		return cerrors.Newf(cerrors.InvalidArgument,
			"federated identity credential name %q must be 3-120 characters of letters, digits, '-' or '_', "+
				"starting with a letter or digit", name)
	case !strings.HasPrefix(in.Issuer, "https://"):
		return cerrors.New(cerrors.InvalidArgument, "issuer is required and must be an https URL")
	case in.Subject == "":
		return cerrors.New(cerrors.InvalidArgument, "subject is required")
	case len(in.Audiences) != 1 || in.Audiences[0] == "":
		return cerrors.New(cerrors.InvalidArgument, "audiences must contain exactly one value")
	}

	return nil
}

// CreateOrUpdateFIC creates or replaces a credential under an existing
// identity, reporting whether it was created. A missing identity is NotFound;
// a second credential with the same issuer and subject is AlreadyExists.
func (m *Mock) CreateOrUpdateFIC(_ context.Context, sub, rg, identity, name string,
	in FICInput,
) (FederatedCredential, bool, error) {
	if err := validateFIC(name, &in); err != nil {
		return FederatedCredential{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	id, ok := m.store.Get(key(sub, rg, identity))
	if !ok {
		return FederatedCredential{}, false, cerrors.Newf(cerrors.NotFound, "managed identity %q not found", identity)
	}

	k := ficKey(sub, rg, identity, name)
	_, existed := m.fics.Get(k)
	siblings := m.ficsUnder(key(sub, rg, identity))

	for _, f := range siblings {
		if !strings.EqualFold(f.Name, name) && f.Issuer == in.Issuer && f.Subject == in.Subject {
			return FederatedCredential{}, false, cerrors.Newf(cerrors.AlreadyExists,
				"federated identity credential %q already uses this issuer and subject", f.Name)
		}
	}

	if !existed && len(siblings) >= maxFICsPerIdentity {
		return FederatedCredential{}, false, cerrors.Newf(cerrors.InvalidArgument,
			"identity %q already has the maximum of %d federated identity credentials", identity, maxFICsPerIdentity)
	}

	f := FederatedCredential{
		Subscription: id.Subscription, ResourceGroup: id.ResourceGroup, Identity: id.Name, Name: name,
		Issuer: in.Issuer, Subject: in.Subject, Audiences: append([]string(nil), in.Audiences...),
	}
	m.fics.Set(k, f)

	return cloneFIC(&f), !existed, nil
}

// GetFIC returns a credential, or NotFound.
func (m *Mock) GetFIC(_ context.Context, sub, rg, identity, name string) (FederatedCredential, error) {
	f, ok := m.fics.Get(ficKey(sub, rg, identity, name))
	if !ok {
		return FederatedCredential{}, cerrors.Newf(cerrors.NotFound, "federated identity credential %q not found", name)
	}

	return cloneFIC(&f), nil
}

// DeleteFIC removes a credential, reporting whether it existed.
func (m *Mock) DeleteFIC(_ context.Context, sub, rg, identity, name string) bool {
	return m.fics.Delete(ficKey(sub, rg, identity, name))
}

// ListFICs returns an identity's credentials sorted by name; a missing
// identity is NotFound.
func (m *Mock) ListFICs(_ context.Context, sub, rg, identity string) ([]FederatedCredential, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.store.Get(key(sub, rg, identity)); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "managed identity %q not found", identity)
	}

	return m.ficsUnder(key(sub, rg, identity)), nil
}

// ficsUnder returns copies of the credentials of the identity at idKey. The
// trailing segment bounds the prefix, so id1 never matches id10's credentials.
func (m *Mock) ficsUnder(idKey string) []FederatedCredential {
	prefix := idKey + ficSegment

	var out []FederatedCredential

	for k, f := range m.fics.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, cloneFIC(&f))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// deleteFICsUnder removes every credential of the identity at idKey.
func (m *Mock) deleteFICsUnder(idKey string) {
	prefix := idKey + ficSegment

	for k := range m.fics.All() {
		if strings.HasPrefix(k, prefix) {
			m.fics.Delete(k)
		}
	}
}
