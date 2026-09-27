package ssm

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	ssmdriver "github.com/stackshy/cloudemu/v2/providers/aws/ssm/driver"
)

var _ ssmdriver.Documents = (*Mock)(nil)

// Document quotas from the SSM API reference.
const (
	maxDocuments        = 500
	maxDocumentVersions = 1000
)

// Version selectors a DocumentVersion may carry instead of a number.
const (
	versionLatest  = "$LATEST"
	versionDefault = "$DEFAULT"
)

var (
	docNamePattern     = regexp.MustCompile(`^[a-zA-Z0-9_\-.]{3,128}$`)
	versionNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_\-.]{1,128}$`)
	docVersionPattern  = regexp.MustCompile(`^([$]LATEST|[$]DEFAULT|[1-9]\d*)$`)
	docARNPattern      = regexp.MustCompile(`^arn:aws[a-z-]*:ssm:[^:]*:[^:]*:document/(.+)$`)
)

// docVersion is one immutable version of a document.
type docVersion struct {
	number      int
	versionName string
	displayName string
	content     string
	format      string
	targetType  string
	created     time.Time
	meta        *contentMeta
}

// document holds every version of one document plus its document-wide state.
// All fields are guarded by Mock.docMu.
type document struct {
	name           string
	docType        string
	owner          string
	created        time.Time
	versions       map[int]*docVersion
	defaultVersion int
	latestVersion  int
	nextVersion    int
	tags           map[string]string
	shares         map[string]string
}

// validDocumentName checks the name pattern and the reserved prefixes.
func validDocumentName(name string) error {
	if !docNamePattern.MatchString(name) {
		return ssmErrf(excValidation, errors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'name' failed to satisfy constraint: "+
				"Member must satisfy regular expression pattern: ^[a-zA-Z0-9_\\-.]{3,128}$", name)
	}

	lower := strings.ToLower(name)
	for _, p := range []string{"aws", "amazon", "amzn"} {
		if strings.HasPrefix(lower, p) {
			return ssmErrf(excValidation, errors.InvalidArgument,
				"Document name %s can't start with %s. The prefixes aws, amazon and amzn are reserved.", name, p)
		}
	}

	return nil
}

// documentName accepts a plain name or a document ARN.
func documentName(ref string) string {
	if m := docARNPattern.FindStringSubmatch(ref); m != nil {
		return m[1]
	}

	return ref
}

func defaultFormat(f string) string {
	if f == "" {
		return ssmdriver.FormatJSON
	}

	return f
}

// lookupDocument finds a customer document or an AWS-owned one. The caller
// holds docMu.
func (m *Mock) lookupDocument(name string) (*document, error) {
	name = documentName(name)
	if d, ok := m.documents.Get(name); ok {
		return d, nil
	}

	if d, ok := m.catalog[name]; ok {
		return d, nil
	}

	return nil, invalidDocumentErr(name)
}

// ownedDocument finds a customer document, rejecting AWS-owned ones.
func (m *Mock) ownedDocument(name string) (*document, error) {
	name = documentName(name)
	if _, ok := m.catalog[name]; ok {
		return nil, awsOwnedErr(name)
	}

	d, ok := m.documents.Get(name)
	if !ok {
		return nil, invalidDocumentErr(name)
	}

	return d, nil
}

// resolveVersion selects the version a DocumentVersion/VersionName pair names.
func (d *document) resolveVersion(version, versionName string) (*docVersion, error) {
	var v *docVersion

	switch version {
	case "", versionDefault:
		v = d.versions[d.defaultVersion]
	case versionLatest:
		v = d.versions[d.latestVersion]
	default:
		if !docVersionPattern.MatchString(version) {
			return nil, ssmErrf(excInvalidDocumentVersion, errors.InvalidArgument,
				"The document version %s is not valid.", version)
		}

		n, _ := strconv.Atoi(version)
		v = d.versions[n]
	}

	if versionName != "" {
		named := d.versionNamed(versionName)
		if named == nil || (version != "" && v != named) {
			return nil, ssmErrf(excInvalidDocumentVersion, errors.NotFound,
				"The document version isn't valid or doesn't exist.")
		}

		v = named
	}

	if v == nil {
		return nil, ssmErrf(excInvalidDocumentVersion, errors.NotFound,
			"The document version isn't valid or doesn't exist.")
	}

	return v, nil
}

func (d *document) versionNamed(name string) *docVersion {
	for _, v := range d.versions {
		if v.versionName == name {
			return v
		}
	}

	return nil
}

// describe renders one version as a DocumentDescription.
func (d *document) describe(v *docVersion) *ssmdriver.DocumentDescription {
	out := &ssmdriver.DocumentDescription{
		Name:            d.name,
		DisplayName:     v.displayName,
		VersionName:     v.versionName,
		Owner:           d.owner,
		CreatedDate:     d.created,
		Status:          ssmdriver.StatusActive,
		DocumentVersion: strconv.Itoa(v.number),
		Description:     v.meta.description,
		Parameters:      v.meta.parameters,
		PlatformTypes:   v.meta.platformTypes,
		DocumentType:    d.docType,
		SchemaVersion:   v.meta.schemaVersion,
		LatestVersion:   strconv.Itoa(d.latestVersion),
		DefaultVersion:  strconv.Itoa(d.defaultVersion),
		DocumentFormat:  v.format,
		TargetType:      v.targetType,
		Hash:            v.meta.hash,
		HashType:        ssmdriver.HashTypeSha256,
		Tags:            copyTags(d.tags),
	}

	return out
}

// CreateDocument stores version 1 of a new document.
func (m *Mock) CreateDocument(_ context.Context, in *ssmdriver.CreateDocumentInput) (*ssmdriver.DocumentDescription, error) {
	if err := validDocumentName(in.Name); err != nil {
		return nil, err
	}

	if err := validDocumentInput(in.DocumentType, in.DocumentFormat, in.TargetType, in.VersionName); err != nil {
		return nil, err
	}

	docType := in.DocumentType
	if docType == "" {
		docType = ssmdriver.DocumentTypeCommand
	}

	format := defaultFormat(in.DocumentFormat)

	m.docMu.Lock()
	defer m.docMu.Unlock()

	if m.documents.Has(in.Name) {
		return nil, ssmErrf(excDocumentAlreadyExists, errors.AlreadyExists, "Document with same name %s already exists", in.Name)
	}

	if m.documents.Len() >= maxDocuments {
		return nil, ssmErrf(excDocumentLimitExceeded, errors.ResourceExhausted,
			"You can have at most %d active SSM documents.", maxDocuments)
	}

	meta, err := parseContent(in.Content, format, docType)
	if err != nil {
		return nil, err
	}

	now := m.opts.Clock.Now().UTC()
	d := &document{
		name: in.Name, docType: docType, owner: m.opts.AccountID, created: now,
		versions: map[int]*docVersion{1: {
			number: 1, versionName: in.VersionName, displayName: in.DisplayName, content: in.Content,
			format: format, targetType: in.TargetType, created: now, meta: meta,
		}},
		defaultVersion: 1, latestVersion: 1, nextVersion: 2,
		tags: copyTags(in.Tags),
	}

	m.documents.Set(in.Name, d)

	return d.describe(d.versions[1]), nil
}

func validVersionName(name string) error {
	if name != "" && !versionNamePattern.MatchString(name) {
		return ssmErrf(excValidation, errors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'versionName' failed to satisfy constraint: "+
				"Member must satisfy regular expression pattern: ^[a-zA-Z0-9_\\-.]{1,128}$", name)
	}

	return nil
}

// GetDocument returns one version's content, converted to format when it
// names a different one than the version was stored in.
func (m *Mock) GetDocument(_ context.Context, ref ssmdriver.DocumentRef, format string) (*ssmdriver.DocumentContent, error) {
	if err := validEnum("documentFormat", format, documentFormats()); err != nil {
		return nil, err
	}

	m.docMu.RLock()
	defer m.docMu.RUnlock()

	d, err := m.lookupDocument(ref.Name)
	if err != nil {
		return nil, err
	}

	v, err := d.resolveVersion(ref.Version, ref.VersionName)
	if err != nil {
		return nil, err
	}

	outFormat := v.format
	if format != "" && v.format != ssmdriver.FormatText {
		outFormat = format
	}

	content, err := convertContent(v.content, v.format, outFormat)
	if err != nil {
		return nil, err
	}

	return &ssmdriver.DocumentContent{
		Name: d.name, DisplayName: v.displayName, VersionName: v.versionName,
		DocumentVersion: strconv.Itoa(v.number), CreatedDate: v.created, Status: ssmdriver.StatusActive,
		Content: content, DocumentType: d.docType, DocumentFormat: outFormat,
	}, nil
}

// DescribeDocument describes one version (the default one unless ref names
// another).
func (m *Mock) DescribeDocument(_ context.Context, ref ssmdriver.DocumentRef) (*ssmdriver.DocumentDescription, error) {
	m.docMu.RLock()
	defer m.docMu.RUnlock()

	d, err := m.lookupDocument(ref.Name)
	if err != nil {
		return nil, err
	}

	v, err := d.resolveVersion(ref.Version, ref.VersionName)
	if err != nil {
		return nil, err
	}

	return d.describe(v), nil
}

// UpdateDocument adds a new latest version. The default version does not move.
func (m *Mock) UpdateDocument(_ context.Context, in *ssmdriver.UpdateDocumentInput) (*ssmdriver.DocumentDescription, error) {
	if err := validDocumentInput("", in.DocumentFormat, in.TargetType, in.VersionName); err != nil {
		return nil, err
	}

	m.docMu.Lock()
	defer m.docMu.Unlock()

	d, err := m.ownedDocument(in.Name)
	if err != nil {
		return nil, err
	}

	if in.Version != "" {
		v, verr := d.resolveVersion(in.Version, "")
		if verr != nil {
			return nil, verr
		}

		if v.number != d.latestVersion {
			return nil, ssmErrf(excInvalidDocumentVersion, errors.InvalidArgument,
				"Only the latest version of a document can be updated. The latest version is %d.", d.latestVersion)
		}
	}

	latest := d.versions[d.latestVersion]
	format := defaultFormat(in.DocumentFormat)

	meta, err := parseContent(in.Content, format, d.docType)
	if err != nil {
		return nil, err
	}

	if err := d.checkNewVersion(latest, meta, in.VersionName); err != nil {
		return nil, err
	}

	nv := &docVersion{
		number: d.nextVersion, versionName: in.VersionName, displayName: in.DisplayName,
		content: in.Content, format: format, targetType: in.TargetType,
		created: m.opts.Clock.Now().UTC(), meta: meta,
	}

	if nv.displayName == "" {
		nv.displayName = latest.displayName
	}

	if nv.targetType == "" {
		nv.targetType = latest.targetType
	}

	d.versions[nv.number] = nv
	d.latestVersion = nv.number
	d.nextVersion++

	return d.describe(nv), nil
}

// checkNewVersion applies the version quota and the duplicate rules.
func (d *document) checkNewVersion(latest *docVersion, meta *contentMeta, versionName string) error {
	if len(d.versions) >= maxDocumentVersions {
		return ssmErrf(excDocumentVersionLimitExceeded, errors.ResourceExhausted,
			"The document has too many versions. Delete one or more document versions and try again.")
	}

	if meta.hash == latest.meta.hash {
		return ssmErrf(excDuplicateDocumentContent, errors.AlreadyExists,
			"The content of the association document matches another document. "+
				"Change the content of the document and try again.")
	}

	if versionName != "" && d.versionNamed(versionName) != nil {
		return ssmErrf(excDuplicateDocumentVersionName, errors.AlreadyExists,
			"The version name %s has already been used in this document. Specify a different version name, and then try again.",
			versionName)
	}

	return nil
}

// DeleteDocument deletes the whole document, or one version when ref names
// one. A shared document and the default version can't be deleted.
func (m *Mock) DeleteDocument(_ context.Context, ref ssmdriver.DocumentRef) error {
	m.docMu.Lock()
	defer m.docMu.Unlock()

	d, err := m.ownedDocument(ref.Name)
	if err != nil {
		return err
	}

	if len(d.shares) > 0 {
		return ssmErrf(excInvalidDocumentOperation, errors.FailedPrecondition,
			"You must stop sharing the document before you can delete it.")
	}

	if ref.Version == "" && ref.VersionName == "" {
		m.documents.Delete(d.name)
		return nil
	}

	v, err := d.resolveVersion(ref.Version, ref.VersionName)
	if err != nil {
		return err
	}

	if v.number == d.defaultVersion {
		return ssmErrf(excInvalidDocumentOperation, errors.FailedPrecondition,
			"The default version of a document can't be deleted. Set a different default version first.")
	}

	delete(d.versions, v.number)

	if v.number == d.latestVersion {
		d.latestVersion = 0
		for n := range d.versions {
			d.latestVersion = max(d.latestVersion, n)
		}
	}

	return nil
}

// ListDocumentVersions lists every version, oldest first.
func (m *Mock) ListDocumentVersions(_ context.Context, name string) ([]ssmdriver.DocumentVersionInfo, error) {
	m.docMu.RLock()
	defer m.docMu.RUnlock()

	d, err := m.lookupDocument(name)
	if err != nil {
		return nil, err
	}

	out := make([]ssmdriver.DocumentVersionInfo, 0, len(d.versions))
	for _, v := range d.versions {
		out = append(out, ssmdriver.DocumentVersionInfo{
			Name: d.name, DisplayName: v.displayName, DocumentVersion: strconv.Itoa(v.number),
			VersionName: v.versionName, CreatedDate: v.created, IsDefaultVersion: v.number == d.defaultVersion,
			DocumentFormat: v.format, Status: ssmdriver.StatusActive,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		a, _ := strconv.Atoi(out[i].DocumentVersion)
		b, _ := strconv.Atoi(out[j].DocumentVersion)

		return a < b
	})

	return out, nil
}

// UpdateDocumentDefaultVersion moves the default version.
func (m *Mock) UpdateDocumentDefaultVersion(_ context.Context, name, version string) (*ssmdriver.DefaultVersionResult, error) {
	if err := validVersionNumber(version); err != nil {
		return nil, err
	}

	m.docMu.Lock()
	defer m.docMu.Unlock()

	d, err := m.ownedDocument(name)
	if err != nil {
		return nil, err
	}

	v, err := d.resolveVersion(version, "")
	if err != nil {
		return nil, err
	}

	d.defaultVersion = v.number

	return &ssmdriver.DefaultVersionResult{
		Name: d.name, DefaultVersion: strconv.Itoa(v.number), DefaultVersionName: v.versionName,
	}, nil
}
