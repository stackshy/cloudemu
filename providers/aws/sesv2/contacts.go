package sesv2

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// CreateContactList registers a contact list.
func (m *Mock) CreateContactList(_ context.Context, in driver.ContactListInput) error {
	if in.Name == "" {
		return cerrors.New(cerrors.InvalidArgument, "ContactListName is required")
	}

	now := m.now()
	cl := driver.ContactList{
		Name:        in.Name,
		Description: in.Description,
		Topics:      append([]driver.Topic(nil), in.Topics...),
		CreatedAt:   now,
		UpdatedAt:   now,
		Tags:        copyTags(in.Tags),
	}

	data := &contactListData{cl: cl, contacts: memstore.New[*driver.Contact]()}
	if !m.contactLists.SetIfAbsent(in.Name, data) {
		return cerrors.Newf(cerrors.AlreadyExists, "contact list %q already exists", in.Name)
	}

	return nil
}

// GetContactList returns a contact list by name.
func (m *Mock) GetContactList(_ context.Context, name string) (*driver.ContactList, error) {
	d, err := m.getContactList(name)
	if err != nil {
		return nil, err
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	out := d.cl
	out.Topics = append([]driver.Topic(nil), d.cl.Topics...)
	out.Tags = copyTags(d.cl.Tags)

	return &out, nil
}

// UpdateContactList replaces a contact list's description and topics.
func (m *Mock) UpdateContactList(_ context.Context, in driver.ContactListInput) error {
	d, err := m.getContactList(in.Name)
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.cl.Description = in.Description
	d.cl.Topics = append([]driver.Topic(nil), in.Topics...)
	d.cl.UpdatedAt = m.now()

	return nil
}

// DeleteContactList removes a contact list.
func (m *Mock) DeleteContactList(_ context.Context, name string) error {
	if !m.contactLists.Delete(name) {
		return errContactListNotFound(name)
	}

	return nil
}

// ListContactLists returns all contact lists ordered by name.
func (m *Mock) ListContactLists(_ context.Context) ([]driver.ContactList, error) {
	all := m.contactLists.SortedValues()
	out := make([]driver.ContactList, 0, len(all))

	for _, d := range all {
		d.mu.RLock()
		cl := d.cl
		cl.Tags = copyTags(d.cl.Tags)
		out = append(out, cl)
		d.mu.RUnlock()
	}

	return out, nil
}

// CreateContact adds a contact to a list.
//
//nolint:gocritic // in is passed by value to match the driver interface.
func (m *Mock) CreateContact(_ context.Context, in driver.ContactInput) error {
	d, err := m.getContactList(in.ContactListName)
	if err != nil {
		return err
	}

	if in.EmailAddress == "" {
		return cerrors.New(cerrors.InvalidArgument, "EmailAddress is required")
	}

	now := m.now()
	c := &driver.Contact{
		EmailAddress:     in.EmailAddress,
		TopicPreferences: append([]driver.TopicPreference(nil), in.TopicPreferences...),
		UnsubscribeAll:   in.UnsubscribeAll,
		AttributesData:   in.AttributesData,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if !d.contacts.SetIfAbsent(in.EmailAddress, c) {
		return cerrors.Newf(cerrors.AlreadyExists, "contact %q already exists", in.EmailAddress)
	}

	return nil
}

// GetContact returns a contact from a list.
func (m *Mock) GetContact(_ context.Context, listName, addr string) (*driver.Contact, error) {
	d, err := m.getContactList(listName)
	if err != nil {
		return nil, err
	}

	c, ok := d.contacts.Get(addr)
	if !ok {
		return nil, errContactNotFound(addr)
	}

	out := *c
	out.TopicPreferences = append([]driver.TopicPreference(nil), c.TopicPreferences...)

	return &out, nil
}

// UpdateContact replaces a contact's preferences.
//
//nolint:gocritic // in is passed by value to match the driver interface.
func (m *Mock) UpdateContact(_ context.Context, in driver.ContactInput) error {
	d, err := m.getContactList(in.ContactListName)
	if err != nil {
		return err
	}

	ok := d.contacts.Update(in.EmailAddress, func(c *driver.Contact) *driver.Contact {
		c.TopicPreferences = append([]driver.TopicPreference(nil), in.TopicPreferences...)
		c.UnsubscribeAll = in.UnsubscribeAll
		c.AttributesData = in.AttributesData
		c.UpdatedAt = m.now()

		return c
	})
	if !ok {
		return errContactNotFound(in.EmailAddress)
	}

	return nil
}

// DeleteContact removes a contact from a list.
func (m *Mock) DeleteContact(_ context.Context, listName, addr string) error {
	d, err := m.getContactList(listName)
	if err != nil {
		return err
	}

	if !d.contacts.Delete(addr) {
		return errContactNotFound(addr)
	}

	return nil
}

// ListContacts returns all contacts on a list ordered by address.
func (m *Mock) ListContacts(_ context.Context, listName string) ([]driver.Contact, error) {
	d, err := m.getContactList(listName)
	if err != nil {
		return nil, err
	}

	all := d.contacts.SortedValues()
	out := make([]driver.Contact, 0, len(all))

	for _, c := range all {
		out = append(out, *c)
	}

	return out, nil
}

func (m *Mock) getContactList(name string) (*contactListData, error) {
	d, ok := m.contactLists.Get(name)
	if !ok {
		return nil, errContactListNotFound(name)
	}

	return d, nil
}

func errContactListNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "contact list %q does not exist", name)
}

func errContactNotFound(addr string) error {
	return cerrors.Newf(cerrors.NotFound, "contact %q does not exist", addr)
}
