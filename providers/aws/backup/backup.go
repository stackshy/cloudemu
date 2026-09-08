// Package backup provides an in-memory mock of the AWS Backup control plane:
// backup vaults (with access policy, event notifications and Vault Lock),
// backup plans (with rule bodies and version history) and backup selections.
//
// The mock is control-plane only — there is NO backup-job / recovery-point data
// plane. Computed fields (vault and plan ARNs, plan id, per-version VersionId,
// selection id and creation timestamps) are minted once at create and stored,
// so repeated reads never drift. Rule, lifecycle, copy-action and
// selection-condition blocks round-trip verbatim. Vault Lock is a state
// machine: a lock created with ChangeableForDays (COMPLIANCE mode) fixes a
// future LockDate and becomes immutable once that date passes; a lock without
// one (GOVERNANCE mode) can be changed or removed at any time.
package backup

import (
	"crypto/rand"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/backup/driver"
)

// Compile-time check that Mock implements driver.Backup.
var _ driver.Backup = (*Mock)(nil)

// defaultMaxResults caps a page when the caller requests none.
const defaultMaxResults = 100

// versionIDBytes is the random byte count behind a base64-encoded VersionId.
const versionIDBytes = 24

// ARN resource-kind markers and the service name.
const (
	serviceName = "backup"
	kindVault   = "backup-vault"
	kindPlan    = "backup-plan"
)

// Mock is an in-memory implementation of the AWS Backup control plane. Vaults
// are keyed by name, plans by plan id and selections by selection id.
type Mock struct {
	vaults     *memstore.Store[driver.Vault]
	plans      *memstore.Store[driver.Plan]
	selections *memstore.Store[driver.Selection]
	opts       *config.Options
}

// New creates a new AWS Backup mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		vaults:     memstore.New[driver.Vault](),
		plans:      memstore.New[driver.Plan](),
		selections: memstore.New[driver.Selection](),
		opts:       opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// vaultARN mints the stable ARN reported for a backup vault.
func (m *Mock) vaultARN(name string) string {
	return idgen.AWSARN(serviceName, m.opts.Region, m.opts.AccountID, kindVault+":"+name)
}

// planARN mints the stable ARN reported for a backup plan.
func (m *Mock) planARN(id string) string {
	return idgen.AWSARN(serviceName, m.opts.Region, m.opts.AccountID, kindPlan+":"+id)
}

// mintVersionID returns a fresh base64-encoded VersionId. AWS Backup VersionIds
// are random, immutable, UTF-8 strings; the mock draws random bytes and encodes
// them, falling back to a counter-derived value if the random source fails so
// the value is always non-empty and unique.
func mintVersionID() string {
	b := make([]byte, versionIDBytes)
	if _, err := rand.Read(b); err != nil {
		return base64.StdEncoding.EncodeToString([]byte(idgen.GenerateID("v")))
	}

	return base64.StdEncoding.EncodeToString(b)
}

// resourceRefFromARN extracts the (kind, key) pair from a backup vault or plan
// ARN. A vault ARN of the form arn:aws:backup:{region}:{acct}:backup-vault:{name}
// yields (backup-vault, name); a plan ARN yields (backup-plan, id).
func resourceRefFromARN(arn string) (kind, key string) {
	const arnParts = 6

	if !strings.Contains(arn, ":"+serviceName+":") {
		return "", ""
	}

	parts := strings.SplitN(arn, ":", arnParts+1)
	if len(parts) <= arnParts {
		return "", ""
	}

	resource := parts[arnParts-1]
	if resource != kindVault && resource != kindPlan {
		return "", ""
	}

	return resource, parts[arnParts]
}

// paginate returns the offset window and next token for a slice of length n,
// honoring an opaque numeric offset token.
func paginate(n int, page driver.Page) (start, end int, next string) {
	start = decodeToken(page.NextToken)
	if start > n {
		start = n
	}

	limit := int(page.MaxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	end = start + limit
	if end >= n {
		return start, n, ""
	}

	return start, end, encodeToken(end)
}

func encodeToken(offset int) string {
	return strconv.Itoa(offset)
}

func decodeToken(token string) int {
	if token == "" {
		return 0
	}

	n, err := strconv.Atoi(token)
	if err != nil || n < 0 {
		return 0
	}

	return n
}

// --- deep-copy helpers: reads must never alias stored state ---

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}

	return append([]string(nil), in...)
}

func copyInt64(in *int64) *int64 {
	if in == nil {
		return nil
	}

	v := *in

	return &v
}

func copyBool(in *bool) *bool {
	if in == nil {
		return nil
	}

	v := *in

	return &v
}

func copyTime(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}

	v := *in

	return &v
}

func copyLifecycle(in *driver.Lifecycle) *driver.Lifecycle {
	if in == nil {
		return nil
	}

	return &driver.Lifecycle{
		MoveToColdStorageAfterDays:          copyInt64(in.MoveToColdStorageAfterDays),
		DeleteAfterDays:                     copyInt64(in.DeleteAfterDays),
		OptInToArchiveForSupportedResources: copyBool(in.OptInToArchiveForSupportedResources),
	}
}

func copyCopyActions(in []driver.CopyAction) []driver.CopyAction {
	if in == nil {
		return nil
	}

	out := make([]driver.CopyAction, len(in))
	for i := range in {
		out[i] = driver.CopyAction{
			DestinationBackupVaultArn: in[i].DestinationBackupVaultArn,
			Lifecycle:                 copyLifecycle(in[i].Lifecycle),
		}
	}

	return out
}

func copyRules(in []driver.Rule) []driver.Rule {
	if in == nil {
		return nil
	}

	out := make([]driver.Rule, len(in))

	for i := range in {
		r := in[i]
		r.Lifecycle = copyLifecycle(in[i].Lifecycle)
		r.RecoveryPointTags = copyTags(in[i].RecoveryPointTags)
		r.CopyActions = copyCopyActions(in[i].CopyActions)
		r.StartWindowMinutes = copyInt64(in[i].StartWindowMinutes)
		r.CompletionWindowMinutes = copyInt64(in[i].CompletionWindowMinutes)
		r.EnableContinuousBackup = copyBool(in[i].EnableContinuousBackup)
		out[i] = r
	}

	return out
}

func copyAdvancedSettings(in []driver.AdvancedBackupSetting) []driver.AdvancedBackupSetting {
	if in == nil {
		return nil
	}

	out := make([]driver.AdvancedBackupSetting, len(in))
	for i := range in {
		out[i] = driver.AdvancedBackupSetting{
			ResourceType:  in[i].ResourceType,
			BackupOptions: copyTags(in[i].BackupOptions),
		}
	}

	return out
}

func copyPlanBody(in *driver.PlanBody) driver.PlanBody {
	return driver.PlanBody{
		BackupPlanName:         in.BackupPlanName,
		Rules:                  copyRules(in.Rules),
		AdvancedBackupSettings: copyAdvancedSettings(in.AdvancedBackupSettings),
	}
}

func copyVersions(in []driver.PlanVersion) []driver.PlanVersion {
	out := make([]driver.PlanVersion, len(in))
	for i := range in {
		out[i] = driver.PlanVersion{
			VersionID:    in[i].VersionID,
			CreationDate: in[i].CreationDate,
			Body:         copyPlanBody(&in[i].Body),
		}
	}

	return out
}

// copyPlan returns an alias-free copy of a plan.
func copyPlan(p *driver.Plan) driver.Plan {
	out := *p
	out.Tags = copyTags(p.Tags)
	out.Versions = copyVersions(p.Versions)

	return out
}

func copyConditionParams(in []driver.ConditionParameter) []driver.ConditionParameter {
	if in == nil {
		return nil
	}

	return append([]driver.ConditionParameter(nil), in...)
}

func copyConditions(in *driver.Conditions) *driver.Conditions {
	if in == nil {
		return nil
	}

	return &driver.Conditions{
		StringEquals:    copyConditionParams(in.StringEquals),
		StringLike:      copyConditionParams(in.StringLike),
		StringNotEquals: copyConditionParams(in.StringNotEquals),
		StringNotLike:   copyConditionParams(in.StringNotLike),
	}
}

func copySelectionBody(in *driver.SelectionBody) driver.SelectionBody {
	out := *in
	out.Resources = copyStrings(in.Resources)
	out.NotResources = copyStrings(in.NotResources)
	out.Conditions = copyConditions(in.Conditions)

	if in.ListOfTags != nil {
		out.ListOfTags = append([]driver.ConditionTag(nil), in.ListOfTags...)
	}

	return out
}

// copySelection returns an alias-free copy of a selection.
func copySelection(s *driver.Selection) driver.Selection {
	out := *s
	out.Body = copySelectionBody(&s.Body)

	return out
}

// copyVault returns an alias-free copy of a vault.
func copyVault(v *driver.Vault) driver.Vault {
	out := *v
	out.Tags = copyTags(v.Tags)
	out.BackupVaultEvents = copyStrings(v.BackupVaultEvents)
	out.MinRetentionDays = copyInt64(v.MinRetentionDays)
	out.MaxRetentionDays = copyInt64(v.MaxRetentionDays)
	out.LockDate = copyTime(v.LockDate)

	return out
}
