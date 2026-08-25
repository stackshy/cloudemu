package cloudsql

import (
	"net/http"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

//nolint:gosec // placeholder PEM returned to SDK round-trips; not a real key.
const mockKeyPEM = "-----BEGIN RSA PRIVATE KEY-----\nMOCK\n-----END RSA PRIVATE KEY-----"

// ---- wire types ----

type database struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Instance  string `json:"instance"`
	Project   string `json:"project,omitempty"`
	Charset   string `json:"charset,omitempty"`
	Collation string `json:"collation,omitempty"`
	SelfLink  string `json:"selfLink,omitempty"`
}

type databasesList struct {
	Kind  string     `json:"kind"`
	Items []database `json:"items"`
}

type user struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Host     string `json:"host,omitempty"`
	Instance string `json:"instance,omitempty"`
	Project  string `json:"project,omitempty"`
	Password string `json:"password,omitempty"`
}

type usersList struct {
	Kind  string `json:"kind"`
	Items []user `json:"items"`
}

type sslCert struct {
	Kind             string `json:"kind"`
	CommonName       string `json:"commonName"`
	Sha1Fingerprint  string `json:"sha1Fingerprint"`
	CertSerialNumber string `json:"certSerialNumber,omitempty"`
	Cert             string `json:"cert,omitempty"`
	Instance         string `json:"instance,omitempty"`
	CreateTime       string `json:"createTime,omitempty"`
}

type sslCertsList struct {
	Kind  string    `json:"kind"`
	Items []sslCert `json:"items"`
}

type sslCertInsertResponse struct {
	Kind       string     `json:"kind"`
	ClientCert clientCert `json:"clientCert"`
	Operation  operation  `json:"operation"`
}

type clientCert struct {
	CertInfo       sslCert `json:"certInfo"`
	CertPrivateKey string  `json:"certPrivateKey"`
}

type cloneRequest struct {
	CloneContext struct {
		DestinationInstanceName string `json:"destinationInstanceName"`
	} `json:"cloneContext"`
}

// ---- capability accessors ----

func (h *Handler) databasesCap() (rdsdriver.Databases, bool) {
	c, ok := h.db.(rdsdriver.Databases)
	return c, ok
}

func (h *Handler) usersCap() (rdsdriver.Users, bool) {
	c, ok := h.db.(rdsdriver.Users)
	return c, ok
}

func (h *Handler) sslCertsCap() (rdsdriver.SslCerts, bool) {
	c, ok := h.db.(rdsdriver.SslCerts)
	return c, ok
}

func writeUnsupported(w http.ResponseWriter, what string) {
	writeError(w, http.StatusBadRequest, "OPERATION_NOT_SUPPORTED", what+" is not supported by this driver")
}

// ---- Databases ----

//nolint:dupl // mirrors the sibling sub-resource route by design.
func (h *Handler) serveDatabasesRoute(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	db, ok := h.databasesCap()
	if !ok {
		writeUnsupported(w, "databases")
		return
	}

	if p.subName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertDatabase(w, r, p, db)
		case http.MethodGet:
			h.listDatabases2(w, r, p, db)
		default:
			writeMethodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getDatabase(w, r, p, db)
	case http.MethodDelete:
		if err := db.DeleteDatabase(r.Context(), p.name, p.subName); err != nil {
			writeErr(w, err)
			return
		}

		h.completeOp(w, p.project, "delete-db", "DELETE_DATABASE", "instances", p.name)
	default:
		writeMethodNotAllowed(w)
	}
}

//nolint:dupl // mirrors the sibling insert handler by design.
func (h *Handler) insertDatabase(w http.ResponseWriter, r *http.Request, p *sqlPath, db rdsdriver.Databases) {
	var body database
	if !decodeJSON(w, r, &body) {
		return
	}

	_, err := db.CreateDatabase(r.Context(), rdsdriver.DatabaseConfig{
		Server: p.name, Name: body.Name, Charset: body.Charset, Collation: body.Collation,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	h.completeOp(w, p.project, "insert-db", "CREATE_DATABASE", "instances", p.name)
}

func (*Handler) getDatabase(w http.ResponseWriter, r *http.Request, p *sqlPath, db rdsdriver.Databases) {
	out, err := db.GetDatabase(r.Context(), p.name, p.subName)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toWireDatabase(out, p.project))
}

func (*Handler) listDatabases2(w http.ResponseWriter, r *http.Request, p *sqlPath, db rdsdriver.Databases) {
	items, err := db.ListDatabases(r.Context(), p.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]database, 0, len(items))
	for i := range items {
		out = append(out, toWireDatabase(&items[i], p.project))
	}

	writeJSON(w, http.StatusOK, databasesList{Kind: "sql#databasesList", Items: out})
}

func toWireDatabase(d *rdsdriver.Database, project string) database {
	return database{
		Kind:      "sql#database",
		Name:      d.Name,
		Instance:  d.Server,
		Project:   project,
		Charset:   d.Charset,
		Collation: d.Collation,
		SelfLink:  selfLinkBase + project + "/instances/" + d.Server + "/databases/" + d.Name,
	}
}

// ---- Users ----

// serveUsersRoute handles the Cloud SQL user quirk: Get uses /users/{name}
// while Delete and Update act on the /users collection with a ?name= query
// parameter.
func (h *Handler) serveUsersRoute(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	u, ok := h.usersCap()
	if !ok {
		writeUnsupported(w, "users")
		return
	}

	if p.subName != "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.getUser(w, r, p, u)

		return
	}

	switch r.Method {
	case http.MethodPost:
		h.insertUser(w, r, p, u)
	case http.MethodGet:
		h.listUsers(w, r, p, u)
	case http.MethodPut:
		h.updateUser(w, r, p, u)
	case http.MethodDelete:
		h.deleteUser(w, r, p, u)
	default:
		writeMethodNotAllowed(w)
	}
}

//nolint:dupl // mirrors the sibling insert handler by design.
func (h *Handler) insertUser(w http.ResponseWriter, r *http.Request, p *sqlPath, u rdsdriver.Users) {
	var body user
	if !decodeJSON(w, r, &body) {
		return
	}

	_, err := u.CreateUser(r.Context(), rdsdriver.UserConfig{
		Instance: p.name, Name: body.Name, Host: body.Host, Password: body.Password,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	h.completeOp(w, p.project, "insert-user", "CREATE_USER", "instances", p.name)
}

func (*Handler) getUser(w http.ResponseWriter, r *http.Request, p *sqlPath, u rdsdriver.Users) {
	out, err := u.GetUser(r.Context(), p.name, p.subName)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toWireUser(out, p.project))
}

func (*Handler) listUsers(w http.ResponseWriter, r *http.Request, p *sqlPath, u rdsdriver.Users) {
	items, err := u.ListUsers(r.Context(), p.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]user, 0, len(items))
	for i := range items {
		out = append(out, toWireUser(&items[i], p.project))
	}

	writeJSON(w, http.StatusOK, usersList{Kind: "sql#usersList", Items: out})
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request, p *sqlPath, u rdsdriver.Users) {
	var body user
	if !decodeJSON(w, r, &body) {
		return
	}

	name := r.URL.Query().Get("name")

	_, err := u.UpdateUser(r.Context(), rdsdriver.UserConfig{
		Instance: p.name, Name: name, Host: r.URL.Query().Get("host"), Password: body.Password,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	h.completeOp(w, p.project, "update-user", "UPDATE_USER", "instances", p.name)
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request, p *sqlPath, u rdsdriver.Users) {
	name := r.URL.Query().Get("name")

	if err := u.DeleteUser(r.Context(), p.name, name); err != nil {
		writeErr(w, err)
		return
	}

	h.completeOp(w, p.project, "delete-user", "DELETE_USER", "instances", p.name)
}

func toWireUser(u *rdsdriver.User, project string) user {
	return user{Kind: "sql#user", Name: u.Name, Host: u.Host, Instance: u.Instance, Project: project}
}

// ---- SSL certs ----

//nolint:dupl // mirrors the sibling sub-resource route by design.
func (h *Handler) serveSslCertsRoute(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	sc, ok := h.sslCertsCap()
	if !ok {
		writeUnsupported(w, "sslCerts")
		return
	}

	if p.subName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertSslCert(w, r, p, sc)
		case http.MethodGet:
			h.listSslCerts(w, r, p, sc)
		default:
			writeMethodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getSslCert(w, r, p, sc)
	case http.MethodDelete:
		if err := sc.DeleteSslCert(r.Context(), p.name, p.subName); err != nil {
			writeErr(w, err)
			return
		}

		h.completeOp(w, p.project, "delete-cert", "DELETE_SSL_CERT", "instances", p.name)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) insertSslCert(w http.ResponseWriter, r *http.Request, p *sqlPath, sc rdsdriver.SslCerts) {
	var body struct {
		CommonName string `json:"commonName"`
	}

	if !decodeJSON(w, r, &body) {
		return
	}

	out, err := sc.CreateSslCert(r.Context(), rdsdriver.SslCertConfig{Instance: p.name, CommonName: body.CommonName})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, sslCertInsertResponse{
		Kind:       "sql#sslCertsInsert",
		ClientCert: clientCert{CertInfo: toWireSslCert(out), CertPrivateKey: mockKeyPEM},
		Operation:  h.buildOp(p.project, "insert-cert", "CREATE_SSL_CERT", "instances", p.name),
	})
}

func (*Handler) getSslCert(w http.ResponseWriter, r *http.Request, p *sqlPath, sc rdsdriver.SslCerts) {
	out, err := sc.GetSslCert(r.Context(), p.name, p.subName)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toWireSslCert(out))
}

func (*Handler) listSslCerts(w http.ResponseWriter, r *http.Request, p *sqlPath, sc rdsdriver.SslCerts) {
	items, err := sc.ListSslCerts(r.Context(), p.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]sslCert, 0, len(items))
	for i := range items {
		out = append(out, toWireSslCert(&items[i]))
	}

	writeJSON(w, http.StatusOK, sslCertsList{Kind: "sql#sslCertsList", Items: out})
}

func toWireSslCert(c *rdsdriver.SslCert) sslCert {
	return sslCert{
		Kind:             "sql#sslCert",
		CommonName:       c.CommonName,
		Sha1Fingerprint:  c.Sha1Fingerprint,
		CertSerialNumber: c.SerialNumber,
		Cert:             c.Cert,
		Instance:         c.Instance,
	}
}

// ---- instance actions: clone, failover, replicas ----

func (h *Handler) cloneInstance(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	c, ok := h.db.(rdsdriver.Clonable)
	if !ok {
		writeUnsupported(w, "clone")
		return
	}

	var body cloneRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	if _, err := c.CloneInstance(r.Context(), p.name, body.CloneContext.DestinationInstanceName); err != nil {
		writeErr(w, err)
		return
	}

	h.completeOp(w, p.project, "clone", "CLONE", "instances", body.CloneContext.DestinationInstanceName)
}

func (h *Handler) failoverInstance(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	f, ok := h.db.(rdsdriver.Failover)
	if !ok {
		writeUnsupported(w, "failover")
		return
	}

	if err := f.FailoverInstance(r.Context(), p.name); err != nil {
		writeErr(w, err)
		return
	}

	h.completeOp(w, p.project, "failover", "FAILOVER", "instances", p.name)
}

func (h *Handler) promoteReplica(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	pr, ok := h.db.(rdsdriver.ReplicaPromotion)
	if !ok {
		writeUnsupported(w, "promoteReplica")
		return
	}

	if err := pr.PromoteReplica(r.Context(), p.name); err != nil {
		writeErr(w, err)
		return
	}

	h.completeOp(w, p.project, "promote", "PROMOTE_REPLICA", "instances", p.name)
}

// startReplica / stopReplica start and stop replication on a read replica.
// Unlike Start/StopInstance they do not change the RUNNABLE state (a replica
// stays running), and they require the target to actually be a replica —
// matching real Cloud SQL, which errors otherwise.
func (h *Handler) startReplica(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	if !h.requireReplica(w, r, p) {
		return
	}

	h.completeOp(w, p.project, "start-replica", "START_REPLICA", "instances", p.name)
}

// requireReplica writes an error and returns false unless p.name is an existing
// read replica (has a master).
func (h *Handler) requireReplica(w http.ResponseWriter, r *http.Request, p *sqlPath) bool {
	insts, err := h.db.DescribeInstances(r.Context(), []string{p.name})
	if err != nil {
		writeErr(w, err)
		return false
	}

	if insts[0].ReadReplicaSource == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "instance is not a read replica: "+p.name)
		return false
	}

	return true
}

func (h *Handler) stopReplica(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	if !h.requireReplica(w, r, p) {
		return
	}

	h.completeOp(w, p.project, "stop-replica", "STOP_REPLICA", "instances", p.name)
}
