package notificationhubs

import (
	"encoding/xml"
	"net/http"
	"strings"
	"time"

	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
)

const (
	sbHostSuffix       = "servicebus.windows.net"
	segRegistrations   = "registrations"
	segRegistrationIDs = "registrationids"
	atomEntryType      = "application/atom+xml;type=entry;charset=utf-8"
	connectNS          = "http://schemas.microsoft.com/netservices/2010/10/servicebus/connect"
	// segWithID is the segment count once a registration id is present:
	// {hub}/registrations/{id}.
	segWithID = 3
)

// RegistrationHandler serves the Notification Hubs data-plane registration REST
// API (Atom over {namespace}.servicebus.windows.net/{hub}/registrations). The
// namespace is the leftmost Host label and the hub is the first path segment,
// matching how a device SDK addresses a hub. State lives on the notification
// driver's AzureNotificationHubs optional capability.
type RegistrationHandler struct {
	notif notifdriver.Notification
}

// NewRegistrationHandler returns the registration data-plane handler backed by n.
func NewRegistrationHandler(n notifdriver.Notification) *RegistrationHandler {
	return &RegistrationHandler{notif: n}
}

// Matches claims servicebus.windows.net registration data-plane requests.
func (*RegistrationHandler) Matches(r *http.Request) bool {
	if !strings.Contains(hostOnly(r.Host), sbHostSuffix) {
		return false
	}

	seg := regSegments(r.URL.Path)

	return len(seg) >= 2 && (eq(seg[1], segRegistrations) || eq(seg[1], segRegistrationIDs))
}

func hostOnly(host string) string {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		return host[:i]
	}

	return host
}

func regSegments(path string) []string {
	return strings.Split(strings.Trim(path, "/"), "/")
}

// ServeHTTP routes registration requests on the trailing path segments.
func (h *RegistrationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	az, ok := h.notif.(notifdriver.AzureNotificationHubs)
	if !ok {
		writeRegError(w, http.StatusNotImplemented, "registrations not supported")
		return
	}

	seg := regSegments(r.URL.Path)
	host := hostOnly(r.Host)
	namespace, _, _ := strings.Cut(host, ".")
	hub := seg[0]
	key := hubKey(namespace, hub)

	if _, err := h.notif.GetTopic(r.Context(), key); err != nil {
		writeRegError(w, http.StatusNotFound, "notification hub "+hub+" not found")
		return
	}

	if eq(seg[1], segRegistrationIDs) {
		h.createRegistrationID(w, r, seg, host)
		return
	}

	regID := ""
	if len(seg) >= segWithID {
		regID = seg[2]
	}

	h.serveRegistration(w, r, az, regContext{key: key, hub: hub, host: host, regID: regID})
}

type regContext struct {
	key   string
	hub   string
	host  string
	regID string
}

// createRegistrationID answers a POST .../registrationids/ with a 201 and a
// Location header pointing at the new registration slot. The path segment is
// "registrationids" (not "registrations"): the real .NET SDK's
// CreateRegistrationIdAsync only extracts the id when Location.Segments[2]
// equals "registrationids/".
func (*RegistrationHandler) createRegistrationID(w http.ResponseWriter, r *http.Request, seg []string, host string) {
	if r.Method != http.MethodPost {
		writeRegError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	id := "reg-" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	loc := "https://" + host + "/" + seg[0] + "/" + segRegistrationIDs + "/" + id + "?api-version=2015-01"
	w.Header().Set("Location", loc)
	w.WriteHeader(http.StatusCreated)
}

func (h *RegistrationHandler) serveRegistration(
	w http.ResponseWriter, r *http.Request, az notifdriver.AzureNotificationHubs, c regContext,
) {
	switch r.Method {
	case http.MethodPost:
		h.upsertRegistration(w, r, az, c, false)
	case http.MethodPut:
		h.upsertRegistration(w, r, az, c, true)
	case http.MethodGet:
		if c.regID == "" {
			h.listRegistrations(w, r, az, c)
			return
		}

		h.getRegistration(w, r, az, c)
	case http.MethodDelete:
		h.deleteRegistration(w, r, az, c)
	default:
		writeRegError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// atomEntry is the request Atom envelope.
type atomEntry struct {
	XMLName xml.Name `xml:"entry"`
	Content struct {
		Inner string `xml:",innerxml"`
	} `xml:"content"`
}

// descriptionBody parses the RegistrationDescription fragment, returning the
// platform element name, the tags, and the raw inner XML.
type descriptionBody struct {
	XMLName xml.Name
	Tags    string `xml:"Tags"`
	Inner   string `xml:",innerxml"`
}

func (*RegistrationHandler) upsertRegistration(
	w http.ResponseWriter, r *http.Request, az notifdriver.AzureNotificationHubs, c regContext, isPut bool,
) {
	var entry atomEntry
	if err := xml.NewDecoder(r.Body).Decode(&entry); err != nil {
		writeRegError(w, http.StatusBadRequest, "invalid Atom entry: "+err.Error())
		return
	}

	var desc descriptionBody
	if err := xml.Unmarshal([]byte(entry.Content.Inner), &desc); err != nil || desc.XMLName.Local == "" {
		writeRegError(w, http.StatusBadRequest, "invalid registration description")
		return
	}

	reg := notifdriver.AzureRegistration{
		RegistrationID: c.regID,
		Platform:       desc.XMLName.Local,
		Body:           desc.Inner,
		Tags:           splitTags(desc.Tags),
	}

	stored, err := az.CreateRegistration(r.Context(), c.key, reg)
	if err != nil {
		writeRegError(w, http.StatusBadRequest, err.Error())
		return
	}

	status := http.StatusCreated
	if isPut {
		status = http.StatusOK
	}

	writeRegistrationEntry(w, c, &stored, status)
}

func (*RegistrationHandler) getRegistration(
	w http.ResponseWriter, r *http.Request, az notifdriver.AzureNotificationHubs, c regContext,
) {
	reg, err := az.GetRegistration(r.Context(), c.key, c.regID)
	if err != nil {
		writeRegError(w, http.StatusNotFound, err.Error())
		return
	}

	writeRegistrationEntry(w, c, &reg, http.StatusOK)
}

func (*RegistrationHandler) deleteRegistration(
	w http.ResponseWriter, r *http.Request, az notifdriver.AzureNotificationHubs, c regContext,
) {
	if err := az.DeleteRegistration(r.Context(), c.key, c.regID); err != nil {
		writeRegError(w, http.StatusNotFound, err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (*RegistrationHandler) listRegistrations(
	w http.ResponseWriter, r *http.Request, az notifdriver.AzureNotificationHubs, c regContext,
) {
	regs, err := az.ListRegistrations(r.Context(), c.key)
	if err != nil {
		writeRegError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var b strings.Builder

	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
	b.WriteString(`<feed xmlns="http://www.w3.org/2005/Atom">`)

	for i := range regs {
		reg := regs[i]
		b.WriteString(registrationEntryXML(&c, &reg))
	}

	b.WriteString(`</feed>`)

	w.Header().Set("Content-Type", "application/atom+xml;type=feed;charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}

func splitTags(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}

	parts := strings.Split(s, ",")

	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}

	return out
}

// registrationEntryXML renders a single registration as an Atom entry with the
// read-only ETag and RegistrationId prepended to the stored description body.
func registrationEntryXML(c *regContext, reg *notifdriver.AzureRegistration) string {
	self := "https://" + c.host + "/" + c.hub + "/" + segRegistrations + "/" + reg.RegistrationID
	platform := reg.Platform
	if platform == "" {
		platform = "GcmRegistrationDescription"
	}

	var b strings.Builder

	b.WriteString(`<entry xmlns="http://www.w3.org/2005/Atom">`)
	b.WriteString("<id>" + self + "</id>")
	b.WriteString(`<title type="text">` + "/" + c.hub + "/" + segRegistrations + "/" + reg.RegistrationID + "</title>")
	b.WriteString("<updated>" + time.Now().UTC().Format(time.RFC3339) + "</updated>")
	b.WriteString(`<content type="application/xml">`)
	b.WriteString("<" + platform + ` xmlns:i="http://www.w3.org/2001/XMLSchema-instance" xmlns="` + connectNS + `">`)
	b.WriteString("<ETag>" + reg.ETag + "</ETag>")
	b.WriteString("<RegistrationId>" + reg.RegistrationID + "</RegistrationId>")
	b.WriteString(reg.Body)
	b.WriteString("</" + platform + ">")
	b.WriteString("</content></entry>")

	return b.String()
}

func writeRegistrationEntry(w http.ResponseWriter, c regContext, reg *notifdriver.AzureRegistration, status int) {
	w.Header().Set("Content-Type", atomEntryType)
	w.Header().Set("ETag", `W/"`+reg.ETag+`"`)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>` + registrationEntryXML(&c, reg)))
}

// writeRegError writes a plain-text error with the given status.
func writeRegError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(msg)) //nolint:gosec // plain-text body, not HTML — no XSS surface
}
