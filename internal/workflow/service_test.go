package workflow

import (
	"errors"
	"testing"
	"time"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/redaction"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/store"
)

func TestWorkflowFullReleaseAndStableIdempotency(t *testing.T) {
	repository, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)
	service := New(repository, redaction.New(), func() time.Time { return now })
	value, err := service.Create(CreateArchiveInput{ID: "workflow-1", SubjectCode: "P-9", InterviewDate: "2026-01-02", Purpose: "研究", Curator: "整理员", ActionKey: "create-once"}, "整理员")
	if err != nil {
		t.Fatal(err)
	}
	if value.Version != 1 {
		t.Fatalf("建档版本为 %d", value.Version)
	}
	value, err = service.SetConsent(value.ID, ConsentInput{ExpectedVersion: value.Version, AllowedUses: []string{"研究"}, NameDisclosure: archive.DisclosurePseudonym, RecordedBy: "整理员", ActionKey: "consent-once"}, "整理员")
	if err != nil {
		t.Fatal(err)
	}
	stable, err := service.Create(CreateArchiveInput{ID: "workflow-1", SubjectCode: "会被忽略", InterviewDate: "2020-01-01", Purpose: "会被忽略", Curator: "乙", ActionKey: "create-once"}, "乙")
	if err != nil {
		t.Fatal(err)
	}
	if stable.Version != 1 || stable.SubjectCode != "P-9" {
		t.Fatalf("重复动作未返回首次稳定结果: %#v", stable)
	}
	value, err = service.UpsertSegment(value.ID, SegmentInput{ExpectedVersion: value.Version, ExpectedRevision: 0, SegmentID: "S1", SpeakerCode: "P-9", SourceText: "李明的电话是123。", ActionKey: "segment"}, "整理员")
	if err != nil {
		t.Fatal(err)
	}
	value, err = service.AddMark(value.ID, MarkInput{ExpectedVersion: value.Version, ID: "M1", SegmentID: "S1", StartOffset: 0, EndOffset: 2, Category: archive.CategoryIdentity, Strategy: archive.StrategyReplace, Replacement: "受访者", ActionKey: "mark-1"}, "整理员")
	if err != nil {
		t.Fatal(err)
	}
	value, err = service.AddMark(value.ID, MarkInput{ExpectedVersion: value.Version, ID: "M2", SegmentID: "S1", StartOffset: 6, EndOffset: 9, Category: archive.CategoryContact, Strategy: archive.StrategyDelete, ActionKey: "mark-2"}, "整理员")
	if err != nil {
		t.Fatal(err)
	}
	value, err = service.SubmitForRedaction(value.ID, VersionInput{ExpectedVersion: value.Version, ActionKey: "submit"}, "整理员")
	if err != nil {
		t.Fatal(err)
	}
	value, err = service.GenerateRedaction(value.ID, VersionInput{ExpectedVersion: value.Version, ActionKey: "generate"}, "整理员")
	if err != nil {
		t.Fatal(err)
	}
	for index, markID := range []string{"M1", "M2"} {
		value, err = service.Review(value.ID, ReviewInput{ExpectedVersion: value.Version, MarkID: markID, Approved: true, ActionKey: "review-" + markID}, "复核员")
		if err != nil {
			t.Fatalf("复核 %d: %v", index, err)
		}
	}
	value, err = service.Freeze(value.ID, VersionInput{ExpectedVersion: value.Version, ActionKey: "freeze"}, "复核员")
	if err != nil {
		t.Fatal(err)
	}
	m, err := service.Approve(value.ID, ApprovalInput{ExpectedVersion: value.Version, ApprovedBy: "发布负责人", ActionKey: "approve"}, "发布负责人")
	if err != nil {
		t.Fatal(err)
	}
	if m.ArchiveID != value.ID {
		t.Fatal("清单归属错误")
	}
	view, err := service.Get(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Archive.Status != archive.StatusPublished || view.Manifest == nil || !view.Gates.CanDownloadManifest {
		t.Fatalf("发布视图不完整: %#v", view.Gates)
	}
	if view.AuditOverview.ManifestStatus != "match" || view.AuditOverview.IntegrityStatus != "intact" {
		t.Fatalf("已发布档案的审计概览不一致: %#v", view.AuditOverview)
	}
	if _, err := service.UpsertSegment(value.ID, SegmentInput{ExpectedVersion: view.Archive.Version, ExpectedRevision: 1, SegmentID: "S1", SpeakerCode: "P-9", SourceText: "发布后修改"}, "整理员"); !errors.Is(err, archive.ErrInvalidState) {
		t.Fatalf("发布后修改应被拒绝: %v", err)
	}
}
