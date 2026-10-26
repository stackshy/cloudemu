package codeartifact

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// EventBridge identity of CodeArtifact's native package version events. See
// "CodeArtifact event format and example" in the CodeArtifact user guide.
const (
	eventSource             = "aws.codeartifact"
	eventPackageVersionDesc = "CodeArtifact Package Version State Change"

	operationCreated = "Created"
	operationUpdated = "Updated"
	operationDeleted = "Deleted"

	dedupBytes = 24
)

// versionChanges says what a package version event changed.
type versionChanges struct {
	AssetsAdded     int
	AssetsRemoved   int
	AssetsUpdated   int
	MetadataUpdated bool
	StatusChanged   bool
}

type versionEventChanges struct {
	AssetsAdded     int  `json:"assetsAdded"`
	AssetsRemoved   int  `json:"assetsRemoved"`
	AssetsUpdated   int  `json:"assetsUpdated"`
	MetadataUpdated bool `json:"metadataUpdated"`
	StatusChanged   bool `json:"statusChanged"`
}

// versionEventDetail is the detail of a "CodeArtifact Package Version State
// Change" event, field for field as the user guide documents it.
type versionEventDetail struct {
	DomainName              string              `json:"domainName"`
	DomainOwner             string              `json:"domainOwner"`
	RepositoryName          string              `json:"repositoryName"`
	RepositoryAdministrator string              `json:"repositoryAdministrator"`
	PackageFormat           string              `json:"packageFormat"`
	PackageNamespace        *string             `json:"packageNamespace"`
	PackageName             string              `json:"packageName"`
	PackageVersion          string              `json:"packageVersion"`
	PackageVersionState     string              `json:"packageVersionState"`
	PackageVersionRevision  string              `json:"packageVersionRevision"`
	Changes                 versionEventChanges `json:"changes"`
	OperationType           string              `json:"operationType"`
	SequenceNumber          int64               `json:"sequenceNumber"`
	EventDeduplicationID    string              `json:"eventDeduplicationId"`
}

// versionEvent is a package version event built under the lock and published
// after it is released.
type versionEvent struct {
	detail   versionEventDetail
	resource string
}

// packageARN mints the ARN a package version event reports as its resource:
// package/<domain>/<repository>/<format>/<namespace>/<name>.
func (m *Mock) packageARN(owner string, p *packageRecord) string {
	return idgen.AWSARN(serviceName, m.opts.Region, owner,
		"package/"+packageKey(p.Domain, p.Repository, p.Format, p.Namespace, p.Name))
}

// newVersionEventLocked builds the event for a change to a package version and
// advances the package's sequence number. The caller holds mu.
func (m *Mock) newVersionEventLocked(repo *driver.Repository, p *packageRecord, v *versionRecord,
	operation string, ch versionChanges) versionEvent {
	p.Seq++

	var ns *string
	if p.Namespace != "" {
		ns = &p.Namespace
	}

	return versionEvent{
		resource: m.packageARN(repo.DomainOwner, p),
		detail: versionEventDetail{
			DomainName:              repo.DomainName,
			DomainOwner:             repo.DomainOwner,
			RepositoryName:          repo.Name,
			RepositoryAdministrator: repo.AdministratorAccount,
			PackageFormat:           p.Format,
			PackageNamespace:        ns,
			PackageName:             p.Name,
			PackageVersion:          v.Version,
			PackageVersionState:     v.Status,
			PackageVersionRevision:  v.Revision,
			Changes:                 versionEventChanges(ch),
			OperationType:           operation,
			SequenceNumber:          p.Seq,
			EventDeduplicationID:    newDedupID(),
		},
	}
}

func newDedupID() string {
	raw := make([]byte, dedupBytes)
	if _, err := rand.Read(raw); err != nil {
		return idgen.UUID()
	}

	return base64.StdEncoding.EncodeToString(raw)
}

// publishVersionEvents puts events on the default bus. It must run without the
// Mock lock held: delivery runs rule targets synchronously, and a target may call
// back into CodeArtifact.
func (m *Mock) publishVersionEvents(ctx context.Context, events []versionEvent) {
	for i := range events {
		m.events.Emit(ctx, eventSource, eventPackageVersionDesc, events[i].detail, events[i].resource)
	}
}

// sortedVersions returns a package's versions ordered by version string.
func sortedVersions(p *packageRecord) []*versionRecord {
	out := make([]*versionRecord, 0, len(p.Versions))
	for _, v := range p.Versions {
		out = append(out, v)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })

	return out
}
