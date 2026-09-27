package ssm

import (
	"fmt"
	"time"
)

// documentSnapshot mirrors a customer document. Derived metadata (hash,
// parameters, platform types) is rebuilt from the content on restore. The
// AWS-owned catalog is not saved: it is rebuilt by New.
type documentSnapshot struct {
	Name           string                    `json:"name"`
	DocType        string                    `json:"docType"`
	Owner          string                    `json:"owner"`
	Created        time.Time                 `json:"created"`
	Versions       []documentVersionSnapshot `json:"versions"`
	DefaultVersion int                       `json:"defaultVersion"`
	LatestVersion  int                       `json:"latestVersion"`
	NextVersion    int                       `json:"nextVersion"`
	Tags           map[string]string         `json:"tags,omitempty"`
	Shares         map[string]string         `json:"shares,omitempty"`
}

type documentVersionSnapshot struct {
	Number      int       `json:"number"`
	VersionName string    `json:"versionName,omitempty"`
	DisplayName string    `json:"displayName,omitempty"`
	Content     string    `json:"content"`
	Format      string    `json:"format"`
	TargetType  string    `json:"targetType,omitempty"`
	Created     time.Time `json:"created"`
}

func (m *Mock) snapshotDocuments() map[string]*documentSnapshot {
	m.docMu.RLock()
	defer m.docMu.RUnlock()

	if m.documents.Len() == 0 {
		return nil
	}

	out := make(map[string]*documentSnapshot, m.documents.Len())

	for name, d := range m.documents.All() {
		ds := &documentSnapshot{
			Name: d.name, DocType: d.docType, Owner: d.owner, Created: d.created,
			DefaultVersion: d.defaultVersion, LatestVersion: d.latestVersion, NextVersion: d.nextVersion,
			Tags: copyTags(d.tags), Shares: copyTags(d.shares),
		}

		for n := 1; n < d.nextVersion; n++ {
			v, ok := d.versions[n]
			if !ok {
				continue
			}

			ds.Versions = append(ds.Versions, documentVersionSnapshot{
				Number: v.number, VersionName: v.versionName, DisplayName: v.displayName,
				Content: v.content, Format: v.format, TargetType: v.targetType, Created: v.created,
			})
		}

		out[name] = ds
	}

	return out
}

func (m *Mock) restoreDocuments(saved map[string]*documentSnapshot) error {
	m.docMu.Lock()
	defer m.docMu.Unlock()

	for name, ds := range saved {
		d := &document{
			name: ds.Name, docType: ds.DocType, owner: ds.Owner, created: ds.Created,
			versions:       make(map[int]*docVersion, len(ds.Versions)),
			defaultVersion: ds.DefaultVersion, latestVersion: ds.LatestVersion, nextVersion: ds.NextVersion,
			tags: copyTags(ds.Tags), shares: copyTags(ds.Shares),
		}

		for _, vs := range ds.Versions {
			meta, err := parseContent(vs.Content, vs.Format, ds.DocType)
			if err != nil {
				return fmt.Errorf("ssm: restore document %s version %d: %w", name, vs.Number, err)
			}

			d.versions[vs.Number] = &docVersion{
				number: vs.Number, versionName: vs.VersionName, displayName: vs.DisplayName,
				content: vs.Content, format: vs.Format, targetType: vs.TargetType, created: vs.Created, meta: meta,
			}
		}

		m.documents.Set(name, d)
	}

	return nil
}
