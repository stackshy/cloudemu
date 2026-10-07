package kendra

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// validFaqFormats is the FAQ file format value set; CSV is the default.
//
//nolint:gochecknoglobals // static validation set
var validFaqFormats = map[string]bool{"CSV": true, "CSV_WITH_HEADER": true, "JSON": true}

//nolint:dupl // child accessors differ per resource type by design
func (m *Mock) faqChild() *child[driver.Faq] {
	return &child[driver.Faq]{
		kind: "FAQ", store: m.faqs,
		idOf:     func(f *driver.Faq) string { return f.ID },
		indexOf:  func(f *driver.Faq) string { return f.IndexID },
		tokenOf:  func(f *driver.Faq) string { return f.ClientToken },
		statusOf: func(f *driver.Faq) string { return f.Status },
	}
}

func (m *Mock) viewFaq(f *driver.Faq) driver.Faq {
	out := *f
	out.Tags = copyTags(f.Tags)
	out.Status = m.settleStatus(childKey(f.IndexID, f.ID), f.Status)

	return out
}

// CreateFaq registers a FAQ file with an index. The S3 object is not read (the
// emulator keeps the reference only), so a FAQ never contributes question-answer
// results or statistics. A repeated ClientToken returns the first FAQ.
func (m *Mock) CreateFaq(_ context.Context, in *driver.CreateFaqInput) (*driver.Faq, error) {
	format, err := validateCreateFaq(in)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveIndex(in.IndexID); err != nil {
		return nil, err
	}

	if existing, ok := childByToken(m.faqChild(), in.IndexID, in.ClientToken); ok {
		out := m.viewFaq(&existing)

		return &out, nil
	}

	id := newUUID()
	now := m.now()
	lang := in.LanguageCode

	if lang == "" {
		lang = "en"
	}

	f := driver.Faq{
		ID: id, IndexID: in.IndexID, Name: in.Name, Description: in.Description, RoleArn: in.RoleArn,
		S3Path: in.S3Path, FileFormat: format, LanguageCode: lang, Status: driver.ChildStatusActive,
		ClientToken: in.ClientToken, CreatedAt: now, UpdatedAt: now, Tags: copyTags(in.Tags),
	}

	m.faqs.Set(childKey(in.IndexID, id), f)
	m.beginSettle(childKey(in.IndexID, id), driver.ChildStatusCreating)

	out := m.viewFaq(&f)

	return &out, nil
}

// validateCreateFaq applies CreateFaq's input rules and returns the file format
// to store (CSV when omitted).
func validateCreateFaq(in *driver.CreateFaqInput) (string, error) {
	if err := validateIndexID(in.IndexID); err != nil {
		return "", err
	}

	if err := validateName(in.Name, maxChildName); err != nil {
		return "", err
	}

	if err := validateRoleArn(in.RoleArn); err != nil {
		return "", err
	}

	if err := checkS3Path("S3Path", in.S3Path); err != nil {
		return "", err
	}

	format := in.FileFormat
	if format == "" {
		format = "CSV"
	}

	if !validFaqFormats[format] {
		return "", validation("invalid FileFormat: %q", in.FileFormat)
	}

	return format, validateCommon(in.ClientToken, in.Description, in.Tags)
}

// DescribeFaq returns a FAQ by index and id.
func (m *Mock) DescribeFaq(_ context.Context, indexID, id string) (*driver.Faq, error) {
	f, err := childGet(m, m.faqChild(), indexID, id)
	if err != nil {
		return nil, err
	}

	out := m.viewFaq(&f)

	return &out, nil
}

// ListFaqs returns a deterministic page of an index's FAQs ordered by id.
func (m *Mock) ListFaqs(_ context.Context, indexID string, page driver.Page) (faqs []driver.Faq, nextToken string, err error) {
	items, next, err := childList(m, m.faqChild(), indexID, page, maxPageSize)
	if err != nil {
		return nil, "", err
	}

	out := make([]driver.Faq, 0, len(items))
	for i := range items {
		out = append(out, m.viewFaq(&items[i]))
	}

	return out, next, nil
}

// DeleteFaq removes a FAQ. A FAQ that is still settling cannot be deleted
// (ConflictException).
func (m *Mock) DeleteFaq(_ context.Context, indexID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := childRequireActive(m, m.faqChild(), indexID, id); err != nil {
		return err
	}

	m.faqs.Delete(childKey(indexID, id))
	m.settling.Clear(childKey(indexID, id))

	return nil
}
