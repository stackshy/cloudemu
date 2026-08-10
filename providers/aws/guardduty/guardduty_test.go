package guardduty_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/guardduty"
	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

func newMock() *guardduty.Mock {
	return guardduty.New(config.NewOptions())
}

// mustDetector creates an enabled detector and returns its ID.
func mustDetector(t *testing.T, m *guardduty.Mock) string {
	t.Helper()

	det, err := m.CreateDetector(context.Background(), driver.CreateDetectorInput{Enable: true})
	if err != nil {
		t.Fatalf("CreateDetector: %v", err)
	}

	return det.ID
}

func TestDetectorCRUD(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	det, err := m.CreateDetector(ctx, driver.CreateDetectorInput{
		Enable: true,
		Tags:   map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("CreateDetector: %v", err)
	}

	if det.ID == "" || det.Status != driver.DetectorStatusEnabled {
		t.Fatalf("unexpected detector: %+v", det)
	}

	got, err := m.GetDetector(ctx, det.ID)
	if err != nil {
		t.Fatalf("GetDetector: %v", err)
	}

	if got.Tags["env"] != "prod" {
		t.Fatalf("tags not reflected: %+v", got.Tags)
	}

	disable := false
	if err := m.UpdateDetector(ctx, driver.UpdateDetectorInput{DetectorID: det.ID, Enable: &disable}); err != nil {
		t.Fatalf("UpdateDetector: %v", err)
	}

	got, _ = m.GetDetector(ctx, det.ID)
	if got.Status != driver.DetectorStatusDisabled {
		t.Fatalf("status = %s, want DISABLED", got.Status)
	}

	ids, _, err := m.ListDetectors(ctx, driver.Page{})
	if err != nil || len(ids) != 1 || ids[0] != det.ID {
		t.Fatalf("ListDetectors: %v ids=%v", err, ids)
	}

	if err := m.DeleteDetector(ctx, det.ID); err != nil {
		t.Fatalf("DeleteDetector: %v", err)
	}

	if _, err := m.GetDetector(ctx, det.ID); !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFoundException after delete, got %v", err)
	}
}

func TestGetDetectorMissing(t *testing.T) {
	m := newMock()

	_, err := m.GetDetector(context.Background(), "nope")
	if !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFoundException, got %v", err)
	}
}

func TestCreateDetectorOnePerAccount(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, err := m.CreateDetector(ctx, driver.CreateDetectorInput{Enable: true}); err != nil {
		t.Fatalf("first CreateDetector: %v", err)
	}

	// GuardDuty allows only one detector per account per region.
	_, err := m.CreateDetector(ctx, driver.CreateDetectorInput{Enable: true})
	if !isException(err, driver.ExBadRequest) {
		t.Fatalf("second CreateDetector = %v, want BadRequestException", err)
	}
}

func TestCreateDetectorInvalidFrequency(t *testing.T) {
	m := newMock()

	_, err := m.CreateDetector(context.Background(), driver.CreateDetectorInput{
		Enable: true, FindingPublishingFrequency: "BOGUS",
	})
	if !isException(err, driver.ExBadRequest) {
		t.Fatalf("invalid frequency = %v, want BadRequestException", err)
	}
}

func TestCreateFilterRequiresCriteria(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)

	_, err := m.CreateFilter(ctx, driver.CreateFilterInput{DetectorID: id, Name: "nocrit"})
	if !isException(err, driver.ExBadRequest) {
		t.Fatalf("filter without findingCriteria = %v, want BadRequestException", err)
	}
}

func TestIPSetCRUD(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)

	setID, err := m.CreateIPSet(ctx, driver.CreateIPSetInput{
		DetectorID: id, Name: "list", Format: "TXT", Location: "s3://b/k", Activate: true,
	})
	if err != nil {
		t.Fatalf("CreateIPSet: %v", err)
	}

	got, err := m.GetIPSet(ctx, id, setID)
	if err != nil || got.Status != driver.SetStatusActive || got.Name != "list" {
		t.Fatalf("GetIPSet: %v %+v", err, got)
	}

	newName := "renamed"
	if err := m.UpdateIPSet(ctx, driver.UpdateIPSetInput{DetectorID: id, IPSetID: setID, Name: &newName}); err != nil {
		t.Fatalf("UpdateIPSet: %v", err)
	}

	got, _ = m.GetIPSet(ctx, id, setID)
	if got.Name != "renamed" {
		t.Fatalf("name not updated: %+v", got)
	}

	ids, _, err := m.ListIPSets(ctx, id, driver.Page{})
	if err != nil || len(ids) != 1 {
		t.Fatalf("ListIPSets: %v ids=%v", err, ids)
	}

	if err := m.DeleteIPSet(ctx, id, setID); err != nil {
		t.Fatalf("DeleteIPSet: %v", err)
	}

	if _, err := m.GetIPSet(ctx, id, setID); !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFoundException, got %v", err)
	}
}

func TestIPSetOnMissingDetector(t *testing.T) {
	m := newMock()

	_, err := m.CreateIPSet(context.Background(), driver.CreateIPSetInput{
		DetectorID: "missing", Name: "n", Format: "TXT", Location: "s3://b/k",
	})
	if !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFoundException, got %v", err)
	}
}

func TestIPSetValidation(t *testing.T) {
	m := newMock()
	id := mustDetector(t, m)

	_, err := m.CreateIPSet(context.Background(), driver.CreateIPSetInput{DetectorID: id, Format: "TXT", Location: "x"})
	if !isException(err, driver.ExBadRequest) {
		t.Fatalf("want BadRequestException for missing name, got %v", err)
	}
}

func TestFilterCRUDAndDuplicate(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)

	crit := json.RawMessage(`{"Criterion":{"severity":{"Eq":["8"]}}}`)

	name, err := m.CreateFilter(ctx, driver.CreateFilterInput{
		DetectorID: id, Name: "f1", Action: "ARCHIVE", Rank: 1, FindingCriteria: crit,
	})
	if err != nil || name != "f1" {
		t.Fatalf("CreateFilter: %v name=%s", err, name)
	}

	// Duplicate name -> ConflictException.
	_, err = m.CreateFilter(ctx, driver.CreateFilterInput{DetectorID: id, Name: "f1", FindingCriteria: crit})
	if !isException(err, driver.ExConflict) {
		t.Fatalf("want ConflictException on duplicate filter, got %v", err)
	}

	got, err := m.GetFilter(ctx, id, "f1")
	if err != nil || got.Action != "ARCHIVE" {
		t.Fatalf("GetFilter: %v %+v", err, got)
	}

	if err := m.DeleteFilter(ctx, id, "f1"); err != nil {
		t.Fatalf("DeleteFilter: %v", err)
	}

	if err := m.DeleteFilter(ctx, id, "f1"); !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFoundException on second delete, got %v", err)
	}
}

func TestThreatIntelSetCRUD(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)

	setID, err := m.CreateThreatIntelSet(ctx, driver.CreateThreatIntelSetInput{
		DetectorID: id, Name: "tis", Format: "TXT", Location: "s3://b/k",
	})
	if err != nil {
		t.Fatalf("CreateThreatIntelSet: %v", err)
	}

	if _, err := m.GetThreatIntelSet(ctx, id, setID); err != nil {
		t.Fatalf("GetThreatIntelSet: %v", err)
	}

	if err := m.DeleteThreatIntelSet(ctx, id, setID); err != nil {
		t.Fatalf("DeleteThreatIntelSet: %v", err)
	}
}

func TestEntitySetsCRUD(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)

	teID, err := m.CreateThreatEntitySet(ctx, driver.CreateThreatEntitySetInput{
		DetectorID: id, Name: "te", Format: "TXT", Location: "s3://b/k", Activate: true,
	})
	if err != nil {
		t.Fatalf("CreateThreatEntitySet: %v", err)
	}

	te, err := m.GetThreatEntitySet(ctx, id, teID)
	if err != nil || te.CreatedAt.IsZero() {
		t.Fatalf("GetThreatEntitySet: %v %+v", err, te)
	}

	trID, err := m.CreateTrustedEntitySet(ctx, driver.CreateTrustedEntitySetInput{
		DetectorID: id, Name: "tr", Format: "TXT", Location: "s3://b/k",
	})
	if err != nil {
		t.Fatalf("CreateTrustedEntitySet: %v", err)
	}

	if _, err := m.GetTrustedEntitySet(ctx, id, trID); err != nil {
		t.Fatalf("GetTrustedEntitySet: %v", err)
	}
}

// TestDeleteDetectorCascade verifies deleting a detector removes its children so
// no orphan survives to be read.
func TestDeleteDetectorCascade(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)

	setID, _ := m.CreateIPSet(ctx, driver.CreateIPSetInput{
		DetectorID: id, Name: "n", Format: "TXT", Location: "x",
	})
	_, _ = m.CreateFilter(ctx, driver.CreateFilterInput{
		DetectorID: id, Name: "f", FindingCriteria: json.RawMessage(`{"Criterion":{}}`),
	})

	if err := m.DeleteDetector(ctx, id); err != nil {
		t.Fatalf("DeleteDetector: %v", err)
	}

	// The parent detector is gone, so child reads return NotFound (via the
	// detector lookup), proving no orphan is reachable.
	if _, err := m.GetIPSet(ctx, id, setID); !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFoundException for orphaned child, got %v", err)
	}
}

// TestNoAliasIPSet mutates a returned IPSet's Tags map and asserts the stored
// copy is untouched (run under -race by CI).
func TestNoAliasIPSet(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)

	setID, _ := m.CreateIPSet(ctx, driver.CreateIPSetInput{
		DetectorID: id, Name: "n", Format: "TXT", Location: "x",
		Tags: map[string]string{"k": "v"},
	})

	got, _ := m.GetIPSet(ctx, id, setID)
	got.Tags["k"] = "MUTATED"
	got.Tags["injected"] = "x"

	again, _ := m.GetIPSet(ctx, id, setID)
	if again.Tags["k"] != "v" || len(again.Tags) != 1 {
		t.Fatalf("stored IPSet was aliased/mutated: %+v", again.Tags)
	}
}

// TestNoAliasDetector proves the returned detector's Features slice and Tags map
// don't alias stored state.
func TestNoAliasDetector(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	det, _ := m.CreateDetector(ctx, driver.CreateDetectorInput{
		Enable: true,
		Tags:   map[string]string{"k": "v"},
	})

	got, _ := m.GetDetector(ctx, det.ID)
	got.Tags["k"] = "MUTATED"

	again, _ := m.GetDetector(ctx, det.ID)
	if again.Tags["k"] != "v" {
		t.Fatalf("stored detector tags aliased: %+v", again.Tags)
	}
}

// jsonMap unmarshals a raw JSON body into a generic map for assertions.
func jsonMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}

	return out
}

func TestMembersLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)

	// Create two members plus a re-create that must be unprocessed.
	body, err := m.CreateMembers(ctx, id, []byte(`{"accountDetails":[`+
		`{"accountId":"111111111111","email":"a@x.com"},`+
		`{"accountId":"222222222222","email":"b@x.com"}]}`))
	if err != nil {
		t.Fatalf("CreateMembers: %v", err)
	}

	if up := jsonMap(t, body)["unprocessedAccounts"].([]any); len(up) != 0 {
		t.Fatalf("expected no unprocessed on create, got %v", up)
	}

	// Invite -> status INVITED.
	if _, err = m.InviteMembers(ctx, id, []byte(`{"accountIds":["111111111111"]}`)); err != nil {
		t.Fatalf("InviteMembers: %v", err)
	}

	// Start monitoring -> ENABLED; an unknown account is unprocessed.
	body, _ = m.StartMonitoringMembers(ctx, id, []byte(`{"accountIds":["111111111111","999999999999"]}`))
	if up := jsonMap(t, body)["unprocessedAccounts"].([]any); len(up) != 1 {
		t.Fatalf("expected 1 unprocessed for unknown account, got %v", up)
	}

	// Get reflects the ENABLED status and unknown-account unprocessed.
	body, _ = m.GetMembers(ctx, id, []byte(`{"accountIds":["111111111111","999999999999"]}`))
	got := jsonMap(t, body)
	members := got["members"].([]any)
	if len(members) != 1 || members[0].(map[string]any)["relationshipStatus"] != "ENABLED" {
		t.Fatalf("unexpected GetMembers: %v", got)
	}

	if len(got["unprocessedAccounts"].([]any)) != 1 {
		t.Fatalf("expected 1 unprocessed in GetMembers")
	}

	// List returns both.
	body, _ = m.ListMembers(ctx, id, driver.Page{})
	if len(jsonMap(t, body)["members"].([]any)) != 2 {
		t.Fatalf("ListMembers should return 2 members")
	}

	// Delete one.
	if _, err = m.DeleteMembers(ctx, id, []byte(`{"accountIds":["222222222222"]}`)); err != nil {
		t.Fatalf("DeleteMembers: %v", err)
	}

	body, _ = m.ListMembers(ctx, id, driver.Page{})
	if len(jsonMap(t, body)["members"].([]any)) != 1 {
		t.Fatalf("ListMembers should return 1 after delete")
	}
}

func TestMembersOnMissingDetector(t *testing.T) {
	m := newMock()

	_, err := m.CreateMembers(context.Background(), "missing", []byte(`{"accountDetails":[]}`))
	if !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFoundException, got %v", err)
	}
}

func TestInvitationsAcceptAndAdminLink(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)

	invID, err := m.AddPendingInvitationForTest(id, "333333333333")
	if err != nil {
		t.Fatalf("AddPendingInvitationForTest: %v", err)
	}

	// Count and list see the pending invitation.
	body, _ := m.GetInvitationsCount(ctx)
	if jsonMap(t, body)["invitationsCount"].(float64) != 1 {
		t.Fatalf("expected 1 invitation")
	}

	// Accepting a non-existent invitation is a BadRequest.
	if _, err = m.AcceptInvitation(ctx, id, []byte(`{"masterId":"444444444444"}`)); !isException(err, driver.ExBadRequest) {
		t.Fatalf("want BadRequest on unknown invitation, got %v", err)
	}

	// Accept via master naming establishes the admin link.
	if _, err = m.AcceptInvitation(ctx, id, []byte(`{"masterId":"333333333333","invitationId":"`+invID+`"}`)); err != nil {
		t.Fatalf("AcceptInvitation: %v", err)
	}

	// Both get-master and get-administrator read the same link.
	body, _ = m.GetMasterAccount(ctx, id)
	master := jsonMap(t, body)["master"].(map[string]any)
	if master["accountId"] != "333333333333" {
		t.Fatalf("master link wrong: %v", master)
	}

	body, _ = m.GetAdministratorAccount(ctx, id)
	admin := jsonMap(t, body)["administrator"].(map[string]any)
	if admin["accountId"] != "333333333333" {
		t.Fatalf("administrator link wrong: %v", admin)
	}

	// Disassociate clears it.
	if _, err = m.DisassociateFromMasterAccount(ctx, id); err != nil {
		t.Fatalf("DisassociateFromMasterAccount: %v", err)
	}

	body, _ = m.GetAdministratorAccount(ctx, id)
	if _, has := jsonMap(t, body)["administrator"]; has {
		t.Fatalf("administrator link should be gone")
	}
}

func TestDeclineInvitations(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)

	if _, err := m.AddPendingInvitationForTest(id, "555555555555"); err != nil {
		t.Fatalf("AddPendingInvitationForTest: %v", err)
	}

	body, _ := m.DeclineInvitations(ctx, []byte(`{"accountIds":["555555555555","666666666666"]}`))
	if len(jsonMap(t, body)["unprocessedAccounts"].([]any)) != 1 {
		t.Fatalf("expected 1 unprocessed for unknown inviter")
	}

	body, _ = m.GetInvitationsCount(ctx)
	if jsonMap(t, body)["invitationsCount"].(float64) != 0 {
		t.Fatalf("expected 0 invitations after decline")
	}
}

func TestOrganizationAdminAccounts(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, err := m.EnableOrganizationAdminAccount(ctx, []byte(`{"adminAccountId":"777777777777"}`)); err != nil {
		t.Fatalf("EnableOrganizationAdminAccount: %v", err)
	}

	// Idempotent re-enable.
	if _, err := m.EnableOrganizationAdminAccount(ctx, []byte(`{"adminAccountId":"777777777777"}`)); err != nil {
		t.Fatalf("re-enable: %v", err)
	}

	body, _ := m.ListOrganizationAdminAccounts(ctx, driver.Page{})
	if len(jsonMap(t, body)["adminAccounts"].([]any)) != 1 {
		t.Fatalf("expected 1 admin account")
	}

	// Missing adminAccountId is a BadRequest.
	if _, err := m.EnableOrganizationAdminAccount(ctx, []byte(`{}`)); !isException(err, driver.ExBadRequest) {
		t.Fatalf("want BadRequest for missing adminAccountId, got %v", err)
	}

	if _, err := m.DisableOrganizationAdminAccount(ctx, []byte(`{"adminAccountId":"777777777777"}`)); err != nil {
		t.Fatalf("DisableOrganizationAdminAccount: %v", err)
	}

	body, _ = m.ListOrganizationAdminAccounts(ctx, driver.Page{})
	if len(jsonMap(t, body)["adminAccounts"].([]any)) != 0 {
		t.Fatalf("expected 0 admin accounts after disable")
	}
}

func TestOrganizationConfiguration(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)

	if _, err := m.UpdateOrganizationConfiguration(ctx, id,
		[]byte(`{"autoEnable":true,"autoEnableOrganizationMembers":"ALL"}`)); err != nil {
		t.Fatalf("UpdateOrganizationConfiguration: %v", err)
	}

	body, _ := m.DescribeOrganizationConfiguration(ctx, id, driver.Page{})
	got := jsonMap(t, body)
	if got["autoEnable"] != true || got["autoEnableOrganizationMembers"] != "ALL" {
		t.Fatalf("org config not reflected: %v", got)
	}
}

func TestPublishingDestinationLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)

	body, err := m.CreatePublishingDestination(ctx, id,
		[]byte(`{"destinationType":"S3","destinationProperties":{"destinationArn":"arn:aws:s3:::b"}}`))
	if err != nil {
		t.Fatalf("CreatePublishingDestination: %v", err)
	}

	destID := jsonMap(t, body)["destinationId"].(string)
	if destID == "" {
		t.Fatal("empty destinationId")
	}

	// First describe transitions PENDING_VERIFICATION -> PUBLISHING.
	body, _ = m.DescribePublishingDestination(ctx, id, destID)
	if jsonMap(t, body)["status"] != "PUBLISHING" {
		t.Fatalf("expected PUBLISHING after describe, got %v", jsonMap(t, body)["status"])
	}

	// Missing ARN is a BadRequest.
	if _, err = m.CreatePublishingDestination(ctx, id, []byte(`{"destinationProperties":{}}`)); !isException(err, driver.ExBadRequest) {
		t.Fatalf("want BadRequest for missing ARN, got %v", err)
	}

	body, _ = m.ListPublishingDestinations(ctx, id, driver.Page{})
	if len(jsonMap(t, body)["destinations"].([]any)) != 1 {
		t.Fatalf("expected 1 destination")
	}

	if _, err = m.DeletePublishingDestination(ctx, id, destID); err != nil {
		t.Fatalf("DeletePublishingDestination: %v", err)
	}

	if _, err = m.DescribePublishingDestination(ctx, id, destID); !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFoundException after delete, got %v", err)
	}
}

// TestNoAliasOrgConfig proves the stored org-config feature slice is not aliased
// by a caller's request slice (run under -race).
func TestNoAliasOrgConfig(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)

	if _, err := m.UpdateOrganizationConfiguration(ctx, id,
		[]byte(`{"features":[{"name":"S3_DATA_EVENTS"}]}`)); err != nil {
		t.Fatalf("UpdateOrganizationConfiguration: %v", err)
	}

	body, _ := m.DescribeOrganizationConfiguration(ctx, id, driver.Page{})
	feats := jsonMap(t, body)["features"].([]any)
	feats[0] = "MUTATED"

	body, _ = m.DescribeOrganizationConfiguration(ctx, id, driver.Page{})
	again := jsonMap(t, body)["features"].([]any)
	if _, ok := again[0].(map[string]any); !ok {
		t.Fatalf("stored org-config features aliased: %v", again)
	}
}

// findingIDsFromList unmarshals a ListFindings body into its findingIds slice.
func findingIDsFromList(t *testing.T, raw []byte) []string {
	t.Helper()

	ids := jsonMap(t, raw)["findingIds"].([]any)
	out := make([]string, 0, len(ids))

	for _, v := range ids {
		out = append(out, v.(string))
	}

	return out
}

// TestFindingsLifecycle covers create-sample -> list -> get -> archive ->
// list(excluded by criteria) -> get(still returns archived) -> unarchive.
func TestFindingsLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)
	mb := bodyFn(t)

	if _, err := m.CreateSampleFindings(ctx, id, nil); err != nil {
		t.Fatalf("CreateSampleFindings: %v", err)
	}

	all := findingIDsFromList(t, mb(m.ListFindings(ctx, id, nil)))
	if len(all) != defaultSampleFindingCount {
		t.Fatalf("ListFindings = %d ids, want %d", len(all), defaultSampleFindingCount)
	}

	// GetFindings returns full objects for the ids.
	got := jsonMap(t, mb(m.GetFindings(ctx, id,
		[]byte(`{"findingIds":["`+all[0]+`"]}`))))["findings"].([]any)
	if len(got) != 1 {
		t.Fatalf("GetFindings = %d, want 1", len(got))
	}

	// Archive the first finding.
	if _, err := m.ArchiveFindings(ctx, id,
		[]byte(`{"findingIds":["`+all[0]+`"]}`)); err != nil {
		t.Fatalf("ArchiveFindings: %v", err)
	}

	// A criteria of service.archived=false must EXCLUDE the archived one.
	nonArchived := findingIDsFromList(t, mb(m.ListFindings(ctx, id,
		[]byte(`{"findingCriteria":{"criterion":{"service.archived":{"eq":["false"]}}}}`))))
	if containsStr(nonArchived, all[0]) {
		t.Fatalf("archived finding %s still listed with archived=false criteria", all[0])
	}

	if len(nonArchived) != len(all)-1 {
		t.Fatalf("non-archived list = %d, want %d", len(nonArchived), len(all)-1)
	}

	// GetFindings still returns the archived finding by id.
	got = jsonMap(t, mb(m.GetFindings(ctx, id,
		[]byte(`{"findingIds":["`+all[0]+`"]}`))))["findings"].([]any)
	if len(got) != 1 {
		t.Fatalf("GetFindings after archive = %d, want 1", len(got))
	}

	// Unarchive restores it to the archived=false listing.
	if _, err := m.UnarchiveFindings(ctx, id,
		[]byte(`{"findingIds":["`+all[0]+`"]}`)); err != nil {
		t.Fatalf("UnarchiveFindings: %v", err)
	}

	nonArchived = findingIDsFromList(t, mb(m.ListFindings(ctx, id,
		[]byte(`{"findingCriteria":{"criterion":{"service.archived":{"eq":["false"]}}}}`))))
	if !containsStr(nonArchived, all[0]) {
		t.Fatalf("unarchived finding %s not listed with archived=false criteria", all[0])
	}
}

// TestFindingsCriteriaIncludeExclude verifies a severity criterion both includes
// matching findings and excludes non-matching ones.
func TestFindingsCriteriaIncludeExclude(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)
	mb := bodyFn(t)

	// Trojan -> severity 8, UnauthorizedAccess -> 5, Recon (PortProbe) -> 2.
	if _, err := m.CreateSampleFindings(ctx, id, []byte(`{"findingTypes":[`+
		`"Trojan:EC2/DNSDataExfiltration","Recon:EC2/PortProbeUnprotectedPort"]}`)); err != nil {
		t.Fatalf("CreateSampleFindings: %v", err)
	}

	// severity >= 8 must include only the Trojan finding.
	hi := findingIDsFromList(t, mb(m.ListFindings(ctx, id,
		[]byte(`{"findingCriteria":{"criterion":{"severity":{"gte":8}}}}`))))
	if len(hi) != 1 {
		t.Fatalf("severity>=8 = %d ids, want 1", len(hi))
	}

	// severity < 8 must exclude the Trojan finding, leaving one.
	lo := findingIDsFromList(t, mb(m.ListFindings(ctx, id,
		[]byte(`{"findingCriteria":{"criterion":{"severity":{"lt":8}}}}`))))
	if len(lo) != 1 || lo[0] == hi[0] {
		t.Fatalf("severity<8 = %v, want the non-Trojan finding", lo)
	}
}

// TestFindingsStatistics verifies COUNT_BY_SEVERITY and SEVERITY grouping counts.
func TestFindingsStatistics(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)
	mb := bodyFn(t)

	if _, err := m.CreateSampleFindings(ctx, id, nil); err != nil {
		t.Fatalf("CreateSampleFindings: %v", err)
	}

	stats := jsonMap(t, mb(m.GetFindingsStatistics(ctx, id,
		[]byte(`{"findingStatisticTypes":["COUNT_BY_SEVERITY"],"groupBy":"SEVERITY"}`))))["findingStatistics"].(map[string]any)

	cbs := stats["countBySeverity"].(map[string]any)
	total := 0.0

	for _, v := range cbs {
		total += v.(float64)
	}

	if int(total) != defaultSampleFindingCount {
		t.Fatalf("countBySeverity totals %v, want %d", total, defaultSampleFindingCount)
	}

	if grp := stats["groupedBySeverity"].([]any); len(grp) == 0 {
		t.Fatalf("groupedBySeverity empty")
	}
}

// TestFindingsPagination verifies a page limit returns a nextToken that resumes.
func TestFindingsPagination(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)
	mb := bodyFn(t)

	if _, err := m.CreateSampleFindings(ctx, id, nil); err != nil {
		t.Fatalf("CreateSampleFindings: %v", err)
	}

	first := jsonMap(t, mb(m.ListFindings(ctx, id, []byte(`{"maxResults":2}`))))
	if len(first["findingIds"].([]any)) != 2 {
		t.Fatalf("first page = %v, want 2", first["findingIds"])
	}

	tok, ok := first["nextToken"].(string)
	if !ok || tok == "" {
		t.Fatalf("expected nextToken on first page, got %v", first["nextToken"])
	}

	second := jsonMap(t, mb(m.ListFindings(ctx, id,
		[]byte(`{"maxResults":2,"nextToken":"`+tok+`"}`))))
	if len(second["findingIds"].([]any)) != 1 {
		t.Fatalf("second page = %v, want 1", second["findingIds"])
	}
}

// TestFindingsOnMissingDetector verifies findings ops require an existing detector.
func TestFindingsOnMissingDetector(t *testing.T) {
	m := newMock()

	if _, err := m.CreateSampleFindings(context.Background(), "nope", nil); !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFoundException, got %v", err)
	}
}

// TestNoAliasFindings ensures GetFindings results never alias stored state (-race).
func TestNoAliasFindings(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)
	mb := bodyFn(t)

	if _, err := m.CreateSampleFindings(ctx, id, nil); err != nil {
		t.Fatalf("CreateSampleFindings: %v", err)
	}

	ids := findingIDsFromList(t, mb(m.ListFindings(ctx, id, nil)))
	body := mb(m.GetFindings(ctx, id, []byte(`{"findingIds":["`+ids[0]+`"]}`)))
	f := jsonMap(t, body)["findings"].([]any)[0].(map[string]any)
	f["service"].(map[string]any)["archived"] = "MUTATED"

	body = mb(m.GetFindings(ctx, id, []byte(`{"findingIds":["`+ids[0]+`"]}`)))
	again := jsonMap(t, body)["findings"].([]any)[0].(map[string]any)
	if _, ok := again["service"].(map[string]any)["archived"].(bool); !ok {
		t.Fatalf("stored finding aliased: %v", again["service"])
	}
}

// TestCoverageListAndStats verifies coverage listing, filtering, and statistics.
func TestCoverageListAndStats(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)
	mb := bodyFn(t)

	all := jsonMap(t, mb(m.ListCoverage(ctx, id, nil)))["resources"].([]any)
	if len(all) != 3 {
		t.Fatalf("ListCoverage = %d resources, want 3", len(all))
	}

	// A RESOURCE_TYPE=EKS filter must return exactly one resource.
	eks := jsonMap(t, mb(m.ListCoverage(ctx, id,
		[]byte(`{"filterCriteria":{"filterCriterion":[{"criterionKey":"RESOURCE_TYPE","filterCondition":{"equals":["EKS"]}}]}}`))))["resources"].([]any)
	if len(eks) != 1 {
		t.Fatalf("EKS-filtered coverage = %d, want 1", len(eks))
	}

	stats := jsonMap(t, mb(m.GetCoverageStatistics(ctx, id,
		[]byte(`{"statisticsType":["COUNT_BY_COVERAGE_STATUS","COUNT_BY_RESOURCE_TYPE"]}`))))["coverageStatistics"].(map[string]any)
	if stats["countByCoverageStatus"].(map[string]any)["HEALTHY"].(float64) != 3 {
		t.Fatalf("countByCoverageStatus HEALTHY != 3: %v", stats)
	}

	if len(stats["countByResourceType"].(map[string]any)) != 3 {
		t.Fatalf("countByResourceType != 3 types: %v", stats["countByResourceType"])
	}
}

// TestUsageStatistics verifies each UsageStatisticType returns its own block.
func TestUsageStatistics(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)
	mb := bodyFn(t)

	byAcct := jsonMap(t, mb(m.GetUsageStatistics(ctx, id,
		[]byte(`{"usageStatisticsType":"SUM_BY_ACCOUNT"}`))))["usageStatistics"].(map[string]any)
	if _, ok := byAcct["sumByAccount"]; !ok {
		t.Fatalf("SUM_BY_ACCOUNT missing sumByAccount: %v", byAcct)
	}

	byFeat := jsonMap(t, mb(m.GetUsageStatistics(ctx, id,
		[]byte(`{"usageStatisticsType":"SUM_BY_FEATURES"}`))))["usageStatistics"].(map[string]any)
	feats := byFeat["sumByFeature"].([]any)
	if len(feats) == 0 {
		t.Fatalf("SUM_BY_FEATURES returned no features")
	}

	first := feats[0].(map[string]any)["total"].(map[string]any)
	if first["unit"].(string) != "USD" {
		t.Fatalf("usage unit = %v, want USD", first["unit"])
	}
}

// TestFreeTrialDays verifies per-account free-trial features carry remaining days.
func TestFreeTrialDays(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	id := mustDetector(t, m)
	mb := bodyFn(t)

	accounts := jsonMap(t, mb(m.GetRemainingFreeTrialDays(ctx, id,
		[]byte(`{"accountIds":["111111111111"]}`))))["accounts"].([]any)
	if len(accounts) != 1 {
		t.Fatalf("free-trial accounts = %d, want 1", len(accounts))
	}

	acct := accounts[0].(map[string]any)
	if acct["accountId"].(string) != "111111111111" {
		t.Fatalf("accountId = %v", acct["accountId"])
	}

	feats := acct["features"].([]any)
	if len(feats) == 0 {
		t.Fatalf("no free-trial features")
	}

	if feats[0].(map[string]any)["freeTrialDaysRemaining"].(float64) != 30 {
		t.Fatalf("freeTrialDaysRemaining = %v, want 30", feats[0])
	}
}

// bodyFn returns a helper that unwraps an (body, error) op result, failing the
// test on error. It is a closure so the two-value op call can be its sole
// argument (Go forbids mixing a multi-value call with extra args).
func bodyFn(t *testing.T) func(json.RawMessage, error) json.RawMessage {
	t.Helper()

	return func(body json.RawMessage, err error) json.RawMessage {
		if err != nil {
			t.Fatalf("op returned error: %v", err)
		}

		return body
	}
}

// containsStr reports whether s is in list (test-local, mirrors the provider's).
func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}

	return false
}

// defaultSampleFindingCount is the number of findings CreateSampleFindings mints
// when the caller requests no specific types (kept in sync with the provider's
// defaultSampleFindingTypes).
const defaultSampleFindingCount = 3

// isException reports whether err is a driver.APIError with the given exception.
func isException(err error, exception string) bool {
	var apiErr *driver.APIError

	return errors.As(err, &apiErr) && apiErr.Exception == exception
}

// --- Malware protection plans ---

func TestMalwareProtectionPlanCRUD(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	body := bodyFn(t)

	created := body(m.CreateMalwareProtectionPlan(ctx, json.RawMessage(`{
		"role":"arn:aws:iam::123456789012:role/gd",
		"protectedResource":{"s3Bucket":{"bucketName":"b"}},
		"actions":{"tagging":{"status":"ENABLED"}},
		"tags":{"env":"prod"}
	}`)))

	var cr struct {
		ID string `json:"malwareProtectionPlanId"`
	}
	if err := json.Unmarshal(created, &cr); err != nil || cr.ID == "" {
		t.Fatalf("create response: %s err %v", created, err)
	}

	got := body(m.GetMalwareProtectionPlan(ctx, cr.ID))

	var gr map[string]any
	if err := json.Unmarshal(got, &gr); err != nil {
		t.Fatalf("get unmarshal: %v", err)
	}

	if gr["status"] != "ACTIVE" {
		t.Fatalf("status = %v, want ACTIVE", gr["status"])
	}

	if gr["role"] != "arn:aws:iam::123456789012:role/gd" {
		t.Fatalf("role = %v", gr["role"])
	}

	if gr["tags"].(map[string]any)["env"] != "prod" {
		t.Fatalf("tags = %v", gr["tags"])
	}

	if _, ok := gr["createdAt"].(float64); !ok {
		t.Fatalf("createdAt not a number: %v", gr["createdAt"])
	}

	// Update role, then confirm.
	body(m.UpdateMalwareProtectionPlan(ctx, cr.ID, json.RawMessage(`{"role":"arn:aws:iam::123456789012:role/new"}`)))

	got = body(m.GetMalwareProtectionPlan(ctx, cr.ID))
	_ = json.Unmarshal(got, &gr)
	if gr["role"] != "arn:aws:iam::123456789012:role/new" {
		t.Fatalf("role after update = %v", gr["role"])
	}

	// List.
	listed := body(m.ListMalwareProtectionPlans(ctx, driver.Page{}))

	var lr struct {
		Plans []map[string]any `json:"malwareProtectionPlans"`
	}
	_ = json.Unmarshal(listed, &lr)
	if len(lr.Plans) != 1 || lr.Plans[0]["malwareProtectionPlanId"] != cr.ID {
		t.Fatalf("list = %s", listed)
	}

	// Delete, then Get is not-found.
	body(m.DeleteMalwareProtectionPlan(ctx, cr.ID))

	if _, err := m.GetMalwareProtectionPlan(ctx, cr.ID); !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFound after delete, got %v", err)
	}
}

func TestMalwareProtectionPlanErrors(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, err := m.CreateMalwareProtectionPlan(ctx, json.RawMessage(`{"protectedResource":{}}`)); !isException(err, driver.ExBadRequest) {
		t.Fatalf("want BadRequest for missing role, got %v", err)
	}

	if _, err := m.CreateMalwareProtectionPlan(ctx, json.RawMessage(`{"role":"r"}`)); !isException(err, driver.ExBadRequest) {
		t.Fatalf("want BadRequest for missing protectedResource, got %v", err)
	}

	if _, err := m.GetMalwareProtectionPlan(ctx, "nope"); !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFound, got %v", err)
	}

	if _, err := m.UpdateMalwareProtectionPlan(ctx, "nope", nil); !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFound on update, got %v", err)
	}

	if _, err := m.DeleteMalwareProtectionPlan(ctx, "nope"); !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFound on delete, got %v", err)
	}
}

// --- Malware scans ---

func TestMalwareScanLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	body := bodyFn(t)
	id := mustDetector(t, m)

	started := body(m.StartMalwareScan(ctx, json.RawMessage(`{"resourceArn":"arn:aws:s3:::bucket"}`)))

	var sr struct {
		ScanID string `json:"scanId"`
	}
	if err := json.Unmarshal(started, &sr); err != nil || sr.ScanID == "" {
		t.Fatalf("start response: %s err %v", started, err)
	}

	got := body(m.GetMalwareScan(ctx, sr.ScanID))

	var gr map[string]any
	_ = json.Unmarshal(got, &gr)
	if gr["scanStatus"] != "COMPLETED" {
		t.Fatalf("scanStatus = %v", gr["scanStatus"])
	}

	if gr["resourceArn"] != "arn:aws:s3:::bucket" {
		t.Fatalf("resourceArn = %v", gr["resourceArn"])
	}

	// List with SCAN_STATUS filter. The SDK nests the criterion list under
	// "filterCriterion" (singular), matching the wire shape the mock parses.
	listed := body(m.ListMalwareScans(ctx, json.RawMessage(`{
		"filterCriteria":{"filterCriterion":[{"criterionKey":"SCAN_STATUS","filterCondition":{"equalsValue":"COMPLETED"}}]}
	}`)))

	var lr struct {
		Scans []map[string]any `json:"scans"`
	}
	_ = json.Unmarshal(listed, &lr)
	if len(lr.Scans) != 1 {
		t.Fatalf("list filtered = %s", listed)
	}

	// Filter that matches nothing.
	listed = body(m.ListMalwareScans(ctx, json.RawMessage(`{
		"filterCriteria":{"filterCriterion":[{"criterionKey":"SCAN_STATUS","filterCondition":{"equalsValue":"FAILED"}}]}
	}`)))
	_ = json.Unmarshal(listed, &lr)
	if len(lr.Scans) != 0 {
		t.Fatalf("list non-matching = %s", listed)
	}

	// Describe (Scan shape, needs a detector).
	desc := body(m.DescribeMalwareScans(ctx, id, nil))
	_ = json.Unmarshal(desc, &lr)
	if len(lr.Scans) != 1 || lr.Scans[0]["scanId"] != sr.ScanID {
		t.Fatalf("describe = %s", desc)
	}

	if _, err := m.GetMalwareScan(ctx, "nope"); !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFound, got %v", err)
	}

	if _, err := m.DescribeMalwareScans(ctx, "nope", nil); !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFound for unknown detector, got %v", err)
	}

	if _, err := m.StartMalwareScan(ctx, json.RawMessage(`{}`)); !isException(err, driver.ExBadRequest) {
		t.Fatalf("want BadRequest for missing resourceArn, got %v", err)
	}
}

func TestMalwareScanPaginationAndSort(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	body := bodyFn(t)

	for i := 0; i < 3; i++ {
		body(m.StartMalwareScan(ctx, json.RawMessage(`{"resourceArn":"arn:aws:s3:::b"}`)))
	}

	first := body(m.ListMalwareScans(ctx, json.RawMessage(`{"maxResults":2}`)))

	var fr struct {
		Scans     []map[string]any `json:"scans"`
		NextToken string           `json:"nextToken"`
	}
	_ = json.Unmarshal(first, &fr)
	if len(fr.Scans) != 2 || fr.NextToken == "" {
		t.Fatalf("first page = %s", first)
	}

	second := body(m.ListMalwareScans(ctx, json.RawMessage(`{"maxResults":2,"nextToken":"`+fr.NextToken+`"}`)))

	var srp struct {
		Scans     []map[string]any `json:"scans"`
		NextToken string           `json:"nextToken"`
	}
	_ = json.Unmarshal(second, &srp)
	if len(srp.Scans) != 1 || srp.NextToken != "" {
		t.Fatalf("second page = %s", second)
	}

	// Bad token.
	if _, err := m.ListMalwareScans(ctx, json.RawMessage(`{"nextToken":"bogus"}`)); !isException(err, driver.ExBadRequest) {
		t.Fatalf("want BadRequest for bogus token, got %v", err)
	}
}

func TestMalwareScanSettings(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	body := bodyFn(t)
	id := mustDetector(t, m)

	body(m.UpdateMalwareScanSettings(ctx, id, json.RawMessage(`{
		"ebsSnapshotPreservation":"RETENTION_WITH_FINDING",
		"scanResourceCriteria":{"include":{"EC2_INSTANCE_TAG":{"mapEquals":[{"key":"k"}]}}}
	}`)))

	got := body(m.GetMalwareScanSettings(ctx, id))

	var gr map[string]any
	_ = json.Unmarshal(got, &gr)
	if gr["ebsSnapshotPreservation"] != "RETENTION_WITH_FINDING" {
		t.Fatalf("ebsSnapshotPreservation = %v", gr["ebsSnapshotPreservation"])
	}

	if _, ok := gr["scanResourceCriteria"]; !ok {
		t.Fatalf("scanResourceCriteria missing: %s", got)
	}

	if _, err := m.GetMalwareScanSettings(ctx, "nope"); !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFound, got %v", err)
	}
}

func TestSendObjectMalwareScan(t *testing.T) {
	m := newMock()

	out, err := m.SendObjectMalwareScan(context.Background(), json.RawMessage(`{"s3Object":{"bucketName":"b","objectKey":"k","eTag":"e"}}`))
	if err != nil {
		t.Fatalf("SendObjectMalwareScan: %v", err)
	}

	if string(out) != "{}" {
		t.Fatalf("SendObjectMalwareScan body = %s, want {}", out)
	}
}

// --- Tags ---

func TestTagsDetector(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	body := bodyFn(t)
	id := mustDetector(t, m)

	arn := "arn:aws:guardduty:us-east-1:123456789012:detector/" + id

	body(m.TagResource(ctx, arn, json.RawMessage(`{"tags":{"a":"1","b":"2"}}`)))

	listed := body(m.ListTagsForResource(ctx, arn))

	var lr struct {
		Tags map[string]string `json:"tags"`
	}
	_ = json.Unmarshal(listed, &lr)
	if lr.Tags["a"] != "1" || lr.Tags["b"] != "2" {
		t.Fatalf("tags = %v", lr.Tags)
	}

	body(m.UntagResource(ctx, arn, []string{"a"}))

	var lr2 struct {
		Tags map[string]string `json:"tags"`
	}
	listed = body(m.ListTagsForResource(ctx, arn))
	_ = json.Unmarshal(listed, &lr2)
	if _, ok := lr2.Tags["a"]; ok || lr2.Tags["b"] != "2" {
		t.Fatalf("after untag = %v", lr2.Tags)
	}

	// Tags also visible on GetDetector.
	det, _ := m.GetDetector(ctx, id)
	if det.Tags["b"] != "2" {
		t.Fatalf("detector tags = %v", det.Tags)
	}
}

func TestTagsPlanAndChild(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	body := bodyFn(t)
	id := mustDetector(t, m)

	// IP set child.
	setID, err := m.CreateIPSet(ctx, driver.CreateIPSetInput{
		DetectorID: id, Name: "s", Format: "TXT", Location: "https://x/y", Activate: true,
	})
	if err != nil {
		t.Fatalf("CreateIPSet: %v", err)
	}

	ipArn := "arn:aws:guardduty:us-east-1:123456789012:detector/" + id + "/ipset/" + setID
	body(m.TagResource(ctx, ipArn, json.RawMessage(`{"tags":{"x":"y"}}`)))

	listed := body(m.ListTagsForResource(ctx, ipArn))

	var lr struct {
		Tags map[string]string `json:"tags"`
	}
	_ = json.Unmarshal(listed, &lr)
	if lr.Tags["x"] != "y" {
		t.Fatalf("ipset tags = %v", lr.Tags)
	}

	// Malware protection plan.
	created := body(m.CreateMalwareProtectionPlan(ctx, json.RawMessage(`{"role":"r","protectedResource":{"s3Bucket":{}}}`)))

	var cr struct {
		ID string `json:"malwareProtectionPlanId"`
	}
	_ = json.Unmarshal(created, &cr)

	planArn := "arn:aws:guardduty:us-east-1:123456789012:malware-protection-plan/" + cr.ID
	body(m.TagResource(ctx, planArn, json.RawMessage(`{"tags":{"p":"q"}}`)))

	listed = body(m.ListTagsForResource(ctx, planArn))
	_ = json.Unmarshal(listed, &lr)
	if lr.Tags["p"] != "q" {
		t.Fatalf("plan tags = %v", lr.Tags)
	}
}

func TestTagsErrors(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	// Non-GuardDuty ARN.
	if _, err := m.ListTagsForResource(ctx, "arn:aws:s3:::bucket"); !isException(err, driver.ExBadRequest) {
		t.Fatalf("want BadRequest for non-guardduty ARN, got %v", err)
	}

	// GuardDuty ARN with unknown resource kind.
	if _, err := m.TagResource(ctx, "arn:aws:guardduty:us-east-1:123456789012:widget/1", json.RawMessage(`{"tags":{"a":"b"}}`)); !isException(err, driver.ExBadRequest) {
		t.Fatalf("want BadRequest for unknown kind, got %v", err)
	}

	// Well-formed detector ARN but detector does not exist.
	arn := "arn:aws:guardduty:us-east-1:123456789012:detector/deadbeef"
	if _, err := m.ListTagsForResource(ctx, arn); !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFound for missing detector, got %v", err)
	}

	if _, err := m.TagResource(ctx, arn, json.RawMessage(`{"tags":{"a":"b"}}`)); !isException(err, driver.ExResourceNotFound) {
		t.Fatalf("want ResourceNotFound tagging missing detector, got %v", err)
	}

	// TagResource with empty tags.
	id := mustDetector(t, m)
	good := "arn:aws:guardduty:us-east-1:123456789012:detector/" + id
	if _, err := m.TagResource(ctx, good, json.RawMessage(`{"tags":{}}`)); !isException(err, driver.ExBadRequest) {
		t.Fatalf("want BadRequest for empty tags, got %v", err)
	}
}

// TestMalwarePlanGetNoAlias confirms a Get returns a deep copy: mutating the
// returned tags map must not affect the stored plan.
func TestMalwarePlanGetNoAlias(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	body := bodyFn(t)

	created := body(m.CreateMalwareProtectionPlan(ctx, json.RawMessage(`{"role":"r","protectedResource":{"s3Bucket":{}},"tags":{"k":"v"}}`)))

	var cr struct {
		ID string `json:"malwareProtectionPlanId"`
	}
	_ = json.Unmarshal(created, &cr)

	got := body(m.GetMalwareProtectionPlan(ctx, cr.ID))

	var gr struct {
		Tags map[string]string `json:"tags"`
	}
	_ = json.Unmarshal(got, &gr)
	gr.Tags["k"] = "mutated"

	// Re-read: store must be unchanged.
	got2 := body(m.GetMalwareProtectionPlan(ctx, cr.ID))
	_ = json.Unmarshal(got2, &gr)
	if gr.Tags["k"] != "v" {
		t.Fatalf("stored tags aliased: %v", gr.Tags)
	}
}

// TestTagListNoAlias confirms mutating a returned tags map does not affect the
// stored resource.
func TestTagListNoAlias(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	body := bodyFn(t)
	id := mustDetector(t, m)

	arn := "arn:aws:guardduty:us-east-1:123456789012:detector/" + id
	body(m.TagResource(ctx, arn, json.RawMessage(`{"tags":{"k":"v"}}`)))

	listed := body(m.ListTagsForResource(ctx, arn))

	var lr struct {
		Tags map[string]string `json:"tags"`
	}
	_ = json.Unmarshal(listed, &lr)
	lr.Tags["k"] = "mutated"

	det, _ := m.GetDetector(ctx, id)
	if det.Tags["k"] != "v" {
		t.Fatalf("stored detector tags aliased: %v", det.Tags)
	}
}
