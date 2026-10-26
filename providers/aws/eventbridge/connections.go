package eventbridge

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

const (
	connStateAuthorized    = "AUTHORIZED"
	connStateAuthorizing   = "AUTHORIZING"
	connStateDeauthorized  = "DEAUTHORIZED"
	connStateDeauthorizing = "DEAUTHORIZING"
	connStateDeleting      = "DELETING"

	connectionSecretPrefix = "events!connection/"
)

// connectionStates is the ConnectionState enum, in the order EventBridge lists it.
func connectionStates() []string {
	return []string{
		stateCreating, stateUpdating, connStateDeleting, connStateAuthorized, connStateDeauthorized,
		connStateAuthorizing, connStateDeauthorizing, "ACTIVE", "FAILED_CONNECTIVITY",
	}
}

// ConnectionSecretStore is the Secrets Manager surface a connection keeps its
// credentials in. The Secrets Manager mock satisfies it.
type ConnectionSecretStore interface {
	CreateSecret(ctx context.Context, cfg secretsdriver.SecretConfig, value []byte) (*secretsdriver.SecretInfo, error)
	PutSecretValue(ctx context.Context, name string, value []byte) (*secretsdriver.SecretVersion, error)
	DeleteSecretWithOptions(ctx context.Context, name string, recoveryWindow *int64, force bool) (*secretsdriver.SecretInfo, string, error)
}

// SetConnectionSecretStore wires the Secrets Manager backend that holds each
// connection's credentials, so the secret behind a connection's SecretArn is a
// real secret. Without it the SecretArn is only a well-formed identifier.
func (m *Mock) SetConnectionSecretStore(s ConnectionSecretStore) {
	m.secrets = s
}

// connectionData is a stored connection. Info keeps the secret values (they are
// needed to call an API destination); every read path returns a redacted copy.
type connectionData struct {
	Info       driver.Connection `json:"info"`
	SecretName string            `json:"secretName"`
}

func errConnectionNotExist(name string) error {
	return errors.Newf(errors.NotFound, "Connection '%s' does not exist.", name)
}

// initialAuthState is the state a connection enters when it is created or its
// authorization changes: BASIC and API_KEY are authorized at once, OAuth first
// fetches a token.
func initialAuthState(authType string) string {
	if authType == authTypeOAuth {
		return connStateAuthorizing
	}

	return connStateAuthorized
}

// authorize records authType's initial state on c at now.
func authorize(c *driver.Connection, now time.Time) {
	c.State = initialAuthState(c.AuthorizationType)
	c.StateReason = ""
	c.LastModifiedAt = now

	if c.State == connStateAuthorized {
		c.LastAuthorizedAt = now
	}
}

func publicConnection(c *driver.Connection) *driver.Connection {
	out := *c
	out.AuthParameters = redactAuth(c.AuthParameters)

	return &out
}

// CreateConnection creates a connection and the Secrets Manager secret that
// holds its credentials.
func (m *Mock) CreateConnection(ctx context.Context, cfg *driver.ConnectionConfig) (*driver.Connection, error) {
	if err := validateConnectionConfig(cfg); err != nil {
		return nil, err
	}

	now := m.opts.Clock.Now().UTC()
	id := idgen.UUID()

	cd := connectionData{
		SecretName: connectionSecretPrefix + cfg.Name + "/" + id,
		Info: driver.Connection{
			Name:              cfg.Name,
			ARN:               idgen.AWSARN("events", m.opts.Region, m.opts.AccountID, "connection/"+cfg.Name+"/"+id),
			Description:       cfg.Description,
			AuthorizationType: cfg.AuthorizationType,
			KMSKeyIdentifier:  cfg.KMSKeyIdentifier,
			AuthParameters:    onlyBlockFor(cfg.AuthorizationType, cloneAuth(*cfg.AuthParameters)),
			CreatedAt:         now,
		},
	}
	authorize(&cd.Info, now)

	if !m.connections.SetIfAbsent(cfg.Name, cd) {
		return nil, errors.Newf(errors.AlreadyExists, "Connection %s already exists.", cfg.Name)
	}

	secretARN, err := m.createConnectionSecret(ctx, &cd)
	if err != nil {
		m.connections.Delete(cfg.Name)

		return nil, err
	}

	cd.Info.SecretARN = secretARN

	if !m.connections.Update(cfg.Name, func(c connectionData) connectionData {
		c.Info.SecretARN = secretARN
		return c
	}) {
		// Deleted while its secret was being created: drop the secret too.
		m.deleteConnectionSecret(ctx, cd.SecretName)

		return nil, errConnectionNotExist(cfg.Name)
	}

	return publicConnection(&cd.Info), nil
}

func (m *Mock) createConnectionSecret(ctx context.Context, cd *connectionData) (string, error) {
	if m.secrets == nil {
		return idgen.AWSARN("secretsmanager", m.opts.Region, m.opts.AccountID,
			"secret:"+cd.SecretName+"-"+idgen.SecretARNSuffix()), nil
	}

	info, err := m.secrets.CreateSecret(ctx, secretsdriver.SecretConfig{
		Name: cd.SecretName, KMSKeyID: cd.Info.KMSKeyIdentifier,
	}, connectionSecretString(&cd.Info.AuthParameters))
	if err != nil {
		return "", err
	}

	return info.ResourceID, nil
}

func (m *Mock) putConnectionSecret(ctx context.Context, cd *connectionData) {
	if m.secrets != nil {
		_, _ = m.secrets.PutSecretValue(ctx, cd.SecretName, connectionSecretString(&cd.Info.AuthParameters))
	}
}

func (m *Mock) deleteConnectionSecret(ctx context.Context, name string) {
	if m.secrets != nil {
		_, _, _ = m.secrets.DeleteSecretWithOptions(ctx, name, nil, true)
	}
}

// settleConnection moves a connection out of a transient authorization state on
// read: an OAuth connection that is AUTHORIZING has its token, and one that is
// DEAUTHORIZING has finished. It returns the stored connection.
func (m *Mock) settleConnection(name string) (connectionData, bool) {
	var out connectionData

	ok := m.connections.Update(name, func(c connectionData) connectionData {
		switch c.Info.State {
		case connStateAuthorizing:
			c.Info.State = connStateAuthorized
			c.Info.LastAuthorizedAt = m.opts.Clock.Now().UTC()
		case connStateDeauthorizing:
			c.Info.State = connStateDeauthorized
		}

		out = c

		return c
	})

	return out, ok
}

// DescribeConnection returns a connection without its secret values.
func (m *Mock) DescribeConnection(_ context.Context, name string) (*driver.Connection, error) {
	cd, ok := m.settleConnection(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound,
			"Failed to describe the connection(s). Connection '%s' does not exist.", name)
	}

	return publicConnection(&cd.Info), nil
}

func validateConnectionUpdate(upd *driver.ConnectionUpdate) error {
	var v violations

	if upd.Description != nil {
		v.maxLen(*upd.Description, "description", maxDescriptionLen)
	}

	if upd.AuthorizationType != nil {
		v.enum(*upd.AuthorizationType, "authorizationType", authorizationTypes())
	}

	return v.err()
}

// applyConnectionUpdate merges upd into c and re-authorizes it, failing when the
// merged authorization parameters do not suit the authorization type.
func applyConnectionUpdate(c *driver.Connection, upd *driver.ConnectionUpdate, now time.Time) error {
	authType := c.AuthorizationType
	if upd.AuthorizationType != nil {
		authType = *upd.AuthorizationType
	}

	merged := mergeAuthParameters(c.AuthParameters, upd.AuthParameters)
	if err := validateAuthParameters(authType, &driver.ConnectionAuthParameters{
		Basic: pick(authType == authTypeBasic, merged.Basic), APIKey: pick(authType == authTypeAPIKey, merged.APIKey),
		OAuth: pick(authType == authTypeOAuth, merged.OAuth), InvocationHTTPParameters: merged.InvocationHTTPParameters,
	}); err != nil {
		return err
	}

	if upd.Description != nil {
		c.Description = *upd.Description
	}

	if upd.KMSKeyIdentifier != nil {
		c.KMSKeyIdentifier = *upd.KMSKeyIdentifier
	}

	c.AuthorizationType = authType
	c.AuthParameters = onlyBlockFor(authType, merged)
	authorize(c, now)

	return nil
}

func pick[T any](keep bool, v *T) *T {
	if keep {
		return v
	}

	return nil
}

// UpdateConnection changes a connection's description, KMS key, authorization
// type or credentials and re-authorizes it, then writes the new credentials to
// its secret.
func (m *Mock) UpdateConnection(ctx context.Context, upd *driver.ConnectionUpdate) (*driver.Connection, error) {
	if err := validateConnectionUpdate(upd); err != nil {
		return nil, err
	}

	var (
		out      connectionData
		applyErr error
	)

	now := m.opts.Clock.Now().UTC()

	found := m.connections.Update(upd.Name, func(c connectionData) connectionData {
		next := c
		next.Info.AuthParameters = cloneAuth(c.Info.AuthParameters)

		if applyErr = applyConnectionUpdate(&next.Info, upd, now); applyErr != nil {
			return c
		}

		out = next

		return next
	})

	if !found {
		return nil, errConnectionNotExist(upd.Name)
	}

	if applyErr != nil {
		return nil, applyErr
	}

	m.putConnectionSecret(ctx, &out)

	return publicConnection(&out.Info), nil
}

// DeauthorizeConnection removes a connection's credential values, keeping the
// connection so it can be re-authorized later.
func (m *Mock) DeauthorizeConnection(ctx context.Context, name string) (*driver.Connection, error) {
	var out connectionData

	now := m.opts.Clock.Now().UTC()

	found := m.connections.Update(name, func(c connectionData) connectionData {
		c.Info.AuthParameters = redactAuth(c.Info.AuthParameters)
		c.Info.State = connStateDeauthorizing
		c.Info.LastModifiedAt = now
		out = c

		return c
	})

	if !found {
		return nil, errConnectionNotExist(name)
	}

	m.putConnectionSecret(ctx, &out)

	return publicConnection(&out.Info), nil
}

// DeleteConnection deletes a connection and its secret. The reply carries the
// DELETING state.
func (m *Mock) DeleteConnection(ctx context.Context, name string) (*driver.Connection, error) {
	cd, ok := m.connections.Get(name)
	if !ok || !m.connections.Delete(name) {
		return nil, errConnectionNotExist(name)
	}

	m.deleteConnectionSecret(ctx, cd.SecretName)

	out := publicConnection(&cd.Info)
	out.State = connStateDeleting

	return out, nil
}

// ListConnections lists connections sorted by name, without secret values.
func (m *Mock) ListConnections(_ context.Context, filter driver.ConnectionFilter) ([]driver.Connection, error) {
	if filter.State != "" {
		var v violations

		v.enum(filter.State, "connectionState", connectionStates())

		if err := v.err(); err != nil {
			return nil, err
		}
	}

	names := m.connections.Keys()
	sort.Strings(names)

	out := make([]driver.Connection, 0, len(names))

	for _, name := range names {
		cd, ok := m.settleConnection(name)
		if !ok || !strings.HasPrefix(name, filter.NamePrefix) || (filter.State != "" && cd.Info.State != filter.State) {
			continue
		}

		out = append(out, *publicConnection(&cd.Info))
	}

	return out, nil
}
