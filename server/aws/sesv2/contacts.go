package sesv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// serveContactLists routes /contact-lists and its sub-paths.
func (h *Handler) serveContactLists(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodPost:
			h.createContactList(w, r)
		case http.MethodGet:
			h.listContactLists(w, r)
		default:
			methodNotAllowed(w)
		}
	case 1:
		h.serveContactListByName(w, r, rest[0])
	default:
		h.serveContacts(w, r, rest[0], rest[1:])
	}
}

func (h *Handler) serveContactListByName(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		h.getContactList(w, r, name)
	case http.MethodPut:
		h.updateContactList(w, r, name)
	case http.MethodDelete:
		h.deleteContactList(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

// serveContacts routes /contact-lists/{list}/contacts[...].
func (h *Handler) serveContacts(w http.ResponseWriter, r *http.Request, list string, rest []string) {
	if rest[0] != "contacts" {
		notFound(w, r.URL.Path)

		return
	}

	switch len(rest) {
	case 1:
		switch r.Method {
		case http.MethodPost:
			h.createContact(w, r, list)
		case http.MethodGet:
			h.listContacts(w, r, list)
		default:
			methodNotAllowed(w)
		}
	case twoSegments:
		if rest[1] == "list" && r.Method == http.MethodPost {
			h.listContacts(w, r, list)

			return
		}

		h.serveContactByAddr(w, r, list, rest[1])
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) serveContactByAddr(w http.ResponseWriter, r *http.Request, list, addr string) {
	switch r.Method {
	case http.MethodGet:
		h.getContact(w, r, list, addr)
	case http.MethodPut:
		h.updateContact(w, r, list, addr)
	case http.MethodDelete:
		h.deleteContact(w, r, list, addr)
	default:
		methodNotAllowed(w)
	}
}

func topicsToDriver(in []topicJSON) []driver.Topic {
	out := make([]driver.Topic, 0, len(in))
	for _, t := range in {
		out = append(out, driver.Topic{
			TopicName:                 t.TopicName,
			DisplayName:               t.DisplayName,
			Description:               t.Description,
			DefaultSubscriptionStatus: t.DefaultSubscriptionStatus,
		})
	}

	return out
}

func topicsToWire(in []driver.Topic) []topicJSON {
	out := make([]topicJSON, 0, len(in))
	for _, t := range in {
		out = append(out, topicJSON{
			TopicName:                 t.TopicName,
			DisplayName:               t.DisplayName,
			Description:               t.Description,
			DefaultSubscriptionStatus: t.DefaultSubscriptionStatus,
		})
	}

	return out
}

func prefsToDriver(in []topicPreferenceJSON) []driver.TopicPreference {
	out := make([]driver.TopicPreference, 0, len(in))
	for _, p := range in {
		out = append(out, driver.TopicPreference{TopicName: p.TopicName, SubscriptionStatus: p.SubscriptionStatus})
	}

	return out
}

func prefsToWire(in []driver.TopicPreference) []topicPreferenceJSON {
	out := make([]topicPreferenceJSON, 0, len(in))
	for _, p := range in {
		out = append(out, topicPreferenceJSON{TopicName: p.TopicName, SubscriptionStatus: p.SubscriptionStatus})
	}

	return out
}

func (h *Handler) createContactList(w http.ResponseWriter, r *http.Request) {
	var req contactListRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.CreateContactList(r.Context(), driver.ContactListInput{
		Name:        req.ContactListName,
		Description: req.Description,
		Topics:      topicsToDriver(req.Topics),
		Tags:        tagsToMap(req.Tags),
	}))
}

func (h *Handler) getContactList(w http.ResponseWriter, r *http.Request, name string) {
	cl, err := h.ses.GetContactList(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, contactListResponse{
		ContactListName:      cl.Name,
		Description:          cl.Description,
		Topics:               topicsToWire(cl.Topics),
		CreatedTimestamp:     epochSeconds(cl.CreatedAt),
		LastUpdatedTimestamp: epochSeconds(cl.UpdatedAt),
		Tags:                 mapToTags(cl.Tags),
	})
}

func (h *Handler) updateContactList(w http.ResponseWriter, r *http.Request, name string) {
	var req contactListRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.UpdateContactList(r.Context(), driver.ContactListInput{
		Name:        name,
		Description: req.Description,
		Topics:      topicsToDriver(req.Topics),
	}))
}

func (h *Handler) deleteContactList(w http.ResponseWriter, r *http.Request, name string) {
	writeOK(w, h.ses.DeleteContactList(r.Context(), name))
}

func (h *Handler) listContactLists(w http.ResponseWriter, r *http.Request) {
	lists, err := h.ses.ListContactLists(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]contactListSummaryJSON, 0, len(lists))
	for i := range lists {
		out = append(out, contactListSummaryJSON{
			ContactListName:      lists[i].Name,
			LastUpdatedTimestamp: epochSeconds(lists[i].UpdatedAt),
		})
	}

	writeJSON(w, listContactListsResponse{ContactLists: out})
}

func (h *Handler) createContact(w http.ResponseWriter, r *http.Request, list string) {
	var req contactRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.CreateContact(r.Context(), driver.ContactInput{
		ContactListName:  list,
		EmailAddress:     req.EmailAddress,
		TopicPreferences: prefsToDriver(req.TopicPreferences),
		UnsubscribeAll:   req.UnsubscribeAll,
		AttributesData:   req.AttributesData,
	}))
}

func (h *Handler) getContact(w http.ResponseWriter, r *http.Request, list, addr string) {
	c, err := h.ses.GetContact(r.Context(), list, addr)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, contactResponse{
		ContactListName:      list,
		EmailAddress:         c.EmailAddress,
		TopicPreferences:     prefsToWire(c.TopicPreferences),
		UnsubscribeAll:       c.UnsubscribeAll,
		AttributesData:       c.AttributesData,
		CreatedTimestamp:     epochSeconds(c.CreatedAt),
		LastUpdatedTimestamp: epochSeconds(c.UpdatedAt),
	})
}

func (h *Handler) updateContact(w http.ResponseWriter, r *http.Request, list, addr string) {
	var req contactRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.UpdateContact(r.Context(), driver.ContactInput{
		ContactListName:  list,
		EmailAddress:     addr,
		TopicPreferences: prefsToDriver(req.TopicPreferences),
		UnsubscribeAll:   req.UnsubscribeAll,
		AttributesData:   req.AttributesData,
	}))
}

func (h *Handler) deleteContact(w http.ResponseWriter, r *http.Request, list, addr string) {
	writeOK(w, h.ses.DeleteContact(r.Context(), list, addr))
}

func (h *Handler) listContacts(w http.ResponseWriter, r *http.Request, list string) {
	contacts, err := h.ses.ListContacts(r.Context(), list)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]contactSummaryJSON, 0, len(contacts))
	for i := range contacts {
		out = append(out, contactSummaryJSON{
			EmailAddress:         contacts[i].EmailAddress,
			TopicPreferences:     prefsToWire(contacts[i].TopicPreferences),
			UnsubscribeAll:       contacts[i].UnsubscribeAll,
			LastUpdatedTimestamp: epochSeconds(contacts[i].UpdatedAt),
		})
	}

	writeJSON(w, listContactsResponse{Contacts: out})
}
