package prismatic

import (
	"strings"
	"testing"
)

func TestNormalizeStatusEndpointID_InsertsDashesIntoBareHex(t *testing.T) {
	got := NormalizeStatusEndpointID("9ed63f8fb6fe4b9393917a3c4de8f1b4")
	want := "9ed63f8f-b6fe-4b93-9391-7a3c4de8f1b4"
	if got != want {
		t.Errorf("NormalizeStatusEndpointID = %q, want %q", got, want)
	}
}

func TestNormalizeStatusEndpointID_LeavesAlreadyDashedUUIDAlone(t *testing.T) {
	id := "9ed63f8f-b6fe-4b93-9391-7a3c4de8f1b4"
	if got := NormalizeStatusEndpointID(id); got != id {
		t.Errorf("NormalizeStatusEndpointID = %q, want unchanged %q", got, id)
	}
}

func TestNormalizeStatusEndpointID_LeavesPermissionLiteralsAlone(t *testing.T) {
	for _, literal := range []string{"USER", "GROUP", "GROUP_MEMBER", "OBJECT_ACCESS", "TAG_ACCESS~9ed63f8f-b6fe-4b93-9391-7a3c4de8f1b4"} {
		if got := NormalizeStatusEndpointID(literal); got != literal {
			t.Errorf("NormalizeStatusEndpointID(%q) = %q, want unchanged", literal, got)
		}
	}
}

func TestBuildStuckSyncCurls_BuildsOnePutPerRow(t *testing.T) {
	rows := []StuckSyncRow{
		{ObjectTypeID: "02ddb657-60c4-4011-9434-106f6bcafe39", SyncNumber: 1, StatusReason: "API_ERROR"},
		{ObjectTypeID: "Folder", SyncNumber: 1, StatusReason: "JOB_TIMEOUT"},
	}
	steps, err := BuildStuckSyncCurls("prod", "9ed63f8fb6fe4b9393917a3c4de8f1b4", rows, "user:tok")
	if err != nil {
		t.Fatalf("BuildStuckSyncCurls: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("len(steps) = %d, want 2", len(steps))
	}
	for _, want := range []string{
		"-u user:tok",
		"-X PUT",
		"https://api.getguru.com/api/v1/admin/sources/9ed63f8f-b6fe-4b93-9391-7a3c4de8f1b4/types/02ddb657-60c4-4011-9434-106f6bcafe39/status",
		`"statusAction": "FAIL"`,
		`"statusReason": "API_ERROR"`,
		`"syncNumber": 1`,
	} {
		if !strings.Contains(steps[0].Curl, want) {
			t.Errorf("step 0 missing %q:\n%s", want, steps[0].Curl)
		}
	}
	if !strings.Contains(steps[1].Curl, "/types/Folder/status") {
		t.Errorf("step 1 should keep the permission-literal objectTypeId unmangled, got: %s", steps[1].Curl)
	}
}

func TestBuildStuckSyncCurls_QAUsesQAHostAndPlaceholderCred(t *testing.T) {
	rows := []StuckSyncRow{{ObjectTypeID: "File", SyncNumber: 1, StatusReason: "API_ERROR"}}
	steps, err := BuildStuckSyncCurls("qa", "sc-157558", rows, "")
	if err != nil {
		t.Fatalf("BuildStuckSyncCurls: %v", err)
	}
	if !strings.Contains(steps[0].Curl, "-u user:token") {
		t.Errorf("expected the qa placeholder credential, got: %s", steps[0].Curl)
	}
	if !strings.Contains(steps[0].Curl, "https://qaapi.getguru.com/api/v1/admin/sources/sc-157558/types/File/status") {
		t.Errorf("expected the qa host + unmangled non-hex sourceId, got: %s", steps[0].Curl)
	}
}

func TestBuildStuckSyncCurls_IncludesDependentsAndErrorDetailsWhenSet(t *testing.T) {
	rows := []StuckSyncRow{{
		ObjectTypeID:           "File",
		SyncNumber:             2,
		StatusReason:           "UNKNOWN_ERROR",
		DependentObjectTypeIDs: []string{"OBJECT_ACCESS", "9ed63f8fb6fe4b9393917a3c4de8f1b4"},
		ErrorDetails:           "worker crashed mid-sync",
	}}
	steps, err := BuildStuckSyncCurls("prod", "sc-1", rows, "u:t")
	if err != nil {
		t.Fatalf("BuildStuckSyncCurls: %v", err)
	}
	curl := steps[0].Curl
	for _, want := range []string{
		`"dependentObjectTypeIds"`,
		`"OBJECT_ACCESS"`,
		"9ed63f8f-b6fe-4b93-9391-7a3c4de8f1b4",
		`"errorDetails": "worker crashed mid-sync"`,
	} {
		if !strings.Contains(curl, want) {
			t.Errorf("curl missing %q:\n%s", want, curl)
		}
	}
}

func TestBuildStuckSyncCurls_RejectsUnknownStatusReason(t *testing.T) {
	rows := []StuckSyncRow{{ObjectTypeID: "File", SyncNumber: 1, StatusReason: "INVALID_AUTHENTICATION"}}
	if _, err := BuildStuckSyncCurls("prod", "sc-1", rows, "u:t"); err == nil {
		t.Fatal("expected an error for a non-FAILED-mapping statusReason, got nil")
	}
}

func TestBuildStuckSyncCurls_RejectsMissingObjectTypeID(t *testing.T) {
	rows := []StuckSyncRow{{SyncNumber: 1, StatusReason: "API_ERROR"}}
	if _, err := BuildStuckSyncCurls("prod", "sc-1", rows, "u:t"); err == nil {
		t.Fatal("expected an error for a missing objectTypeId, got nil")
	}
}

func TestBuildStuckSyncCurls_RejectsEmptySourceIDOrNoRows(t *testing.T) {
	if _, err := BuildStuckSyncCurls("prod", "", []StuckSyncRow{{ObjectTypeID: "File", SyncNumber: 1, StatusReason: "API_ERROR"}}, "u:t"); err == nil {
		t.Fatal("expected an error for an empty sourceId, got nil")
	}
	if _, err := BuildStuckSyncCurls("prod", "sc-1", nil, "u:t"); err == nil {
		t.Fatal("expected an error for zero rows, got nil")
	}
}
