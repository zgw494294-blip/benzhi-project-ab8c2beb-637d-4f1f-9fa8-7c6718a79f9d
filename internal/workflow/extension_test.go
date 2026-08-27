package workflow

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/redaction"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/store"
)

func newExtensionService(t *testing.T) (*Service, *store.JSONStore, string) {
	t.Helper()
	root := t.TempDir()
	repository, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	return New(repository, redaction.New(), func() time.Time { return now }), repository, root
}

func createExtensionArchive(t *testing.T, service *Service, id string) *archive.InterviewArchive {
	t.Helper()
	value, err := service.Create(CreateArchiveInput{ID: id, SubjectCode: "P-1", InterviewDate: "2026-01-01", Purpose: "研究", Curator: "甲", ActionKey: id + "-create"}, "甲")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestConsentRevisionConflictAndTighteningImpact(t *testing.T) {
	service, repository, _ := newExtensionService(t)
	value := createExtensionArchive(t, service, "consent-revisions")
	value, err := service.SetConsent(value.ID, ConsentInput{ExpectedVersion: value.Version, ExpectedConsentRevision: 0, AllowedUses: []string{"研究"}, NameDisclosure: archive.DisclosureAllowed, RecordedBy: "授权员", ActionKey: "consent-1"}, "整理员甲")
	if err != nil {
		t.Fatal(err)
	}
	value, err = service.UpsertSegment(value.ID, SegmentInput{ExpectedVersion: value.Version, SegmentID: "S1", SpeakerCode: "P-1", SourceText: "李明讲述往事", ActionKey: "segment"}, "整理员甲")
	if err != nil {
		t.Fatal(err)
	}
	value, err = service.AddMark(value.ID, MarkInput{ExpectedVersion: value.Version, ID: "M1", SegmentID: "S1", StartOffset: 0, EndOffset: 2, Category: archive.CategoryIdentity, Strategy: archive.StrategyGeneralize, Replacement: "一位邻居", ActionKey: "mark"}, "整理员甲")
	if err != nil {
		t.Fatal(err)
	}
	staleVersion := value.Version
	value, err = service.SetConsent(value.ID, ConsentInput{ExpectedVersion: value.Version, ExpectedConsentRevision: 1, AllowedUses: []string{"研究"}, NameDisclosure: archive.DisclosurePseudonym, RecordedBy: "授权员", ActionKey: "consent-2"}, "整理员乙")
	if err != nil {
		t.Fatal(err)
	}
	if value.ConsentRevision != 2 || len(value.ConsentHistory) != 2 || !value.ConsentHistory[0].Effective || len(value.ConsentImpacts) != 1 {
		t.Fatalf("授权修订或影响范围不完整: %#v", value)
	}
	if _, err := service.SubmitForRedaction(value.ID, VersionInput{ExpectedVersion: value.Version, ActionKey: "blocked-submit"}, "整理员乙"); err == nil {
		t.Fatal("授权收紧后的不合规标注不应允许提交")
	}
	before, _ := repository.Audit(value.ID)
	_, err = service.SetConsent(value.ID, ConsentInput{ExpectedVersion: staleVersion, ExpectedConsentRevision: 1, AllowedUses: []string{"展陈"}, NameDisclosure: archive.DisclosureAllowed, RecordedBy: "授权员", ActionKey: "stale-consent"}, "整理员甲")
	var conflict *archive.ConsentConflictError
	if !errors.As(err, &conflict) || conflict.CurrentRevision != 2 || len(conflict.ChangedFields) == 0 {
		t.Fatalf("授权并发冲突信息不足: %v %#v", err, conflict)
	}
	after, _ := repository.Audit(value.ID)
	if len(after) != len(before) {
		t.Fatal("冲突授权保存不应追加审计事件")
	}
}

func TestSegmentBatchIsAtomicSortedAndIdempotent(t *testing.T) {
	service, repository, _ := newExtensionService(t)
	value := createExtensionArchive(t, service, "segment-batch")
	result, err := service.UpsertSegments(value.ID, SegmentBatchInput{ExpectedVersion: value.Version, ActionKey: "batch-1", Segments: []SegmentBatchItem{{SegmentID: "S003", SpeakerCode: "P", SourceText: "三"}, {SegmentID: "S001", SpeakerCode: "P", SourceText: "一"}, {SegmentID: "S002", SpeakerCode: "P", SourceText: "二"}}}, "甲")
	if err != nil {
		t.Fatal(err)
	}
	if result.ArchiveVersion != 2 || result.Results[0].SegmentID != "S001" {
		t.Fatalf("批量结果未稳定排序: %#v", result)
	}
	stable, err := service.UpsertSegments(value.ID, SegmentBatchInput{ExpectedVersion: 1, ActionKey: "batch-1"}, "甲")
	if err != nil || stable.ArchiveVersion != result.ArchiveVersion {
		t.Fatalf("重复动作未复用首次结果: %#v %v", stable, err)
	}
	before, _ := repository.Audit(value.ID)
	_, err = service.UpsertSegments(value.ID, SegmentBatchInput{ExpectedVersion: result.ArchiveVersion, ActionKey: "batch-stale", Segments: []SegmentBatchItem{{SegmentID: "S001", SpeakerCode: "P", SourceText: "一改", ExpectedRevision: 1}, {SegmentID: "S002", SpeakerCode: "P", SourceText: "二改", ExpectedRevision: 0}}}, "甲")
	if err == nil {
		t.Fatal("存在过期修订号的批次应整体失败")
	}
	loaded, _ := repository.Load(value.ID)
	after, _ := repository.Audit(value.ID)
	if loaded.Segments[0].SourceText != "一" || loaded.Version != result.ArchiveVersion || len(after) != len(before) {
		t.Fatal("失败批次留下了部分变更")
	}
}

func TestMarkPreflightAndAtomicCommit(t *testing.T) {
	service, repository, _ := newExtensionService(t)
	value := createExtensionArchive(t, service, "mark-batch")
	value, _ = service.SetConsent(value.ID, ConsentInput{ExpectedVersion: value.Version, AllowedUses: []string{"研究"}, RestrictedTopics: []string{"病史"}, NameDisclosure: archive.DisclosurePseudonym, RecordedBy: "甲", ActionKey: "consent"}, "甲")
	segments, err := service.UpsertSegments(value.ID, SegmentBatchInput{ExpectedVersion: value.Version, ActionKey: "segments", Segments: []SegmentBatchItem{{SegmentID: "S1", SpeakerCode: "P", SourceText: "李明电话123"}, {SegmentID: "S2", SpeakerCode: "P", SourceText: "谈及病史"}}}, "甲")
	if err != nil {
		t.Fatal(err)
	}
	valid := []MarkCandidate{{ID: "M1", SegmentID: "S1", StartOffset: 0, EndOffset: 2, Category: archive.CategoryIdentity, Strategy: archive.StrategyReplace, Replacement: "P-1"}, {ID: "M2", SegmentID: "S1", StartOffset: 4, EndOffset: 7, Category: archive.CategoryContact, Strategy: archive.StrategyDelete}}
	preview, err := service.PreflightMarks(value.ID, MarkPreflightInput{ExpectedVersion: segments.ArchiveVersion, Marks: valid})
	if err != nil || preview.Digest == "" || len(preview.Previews) != 2 {
		t.Fatalf("合法批次预检失败: %#v %v", preview, err)
	}
	committed, err := service.CommitMarks(value.ID, MarkBatchInput{ExpectedVersion: segments.ArchiveVersion, PreflightDigest: preview.Digest, ActionKey: "marks", Marks: valid}, "甲")
	if err != nil || len(committed.Marks) != 2 {
		t.Fatalf("批量标注提交失败: %#v %v", committed, err)
	}
	before, _ := repository.Audit(value.ID)
	invalid := []MarkCandidate{{ID: "M3", SegmentID: "S1", StartOffset: 1, EndOffset: 3, Category: archive.CategoryIdentity, Strategy: archive.StrategyReplace, Replacement: "P-2"}, {ID: "M4", SegmentID: "S2", StartOffset: 2, EndOffset: 4, Category: archive.CategoryTopic, Strategy: archive.StrategyReplace, Replacement: "其他"}}
	failed, err := service.PreflightMarks(value.ID, MarkPreflightInput{ExpectedVersion: committed.ArchiveVersion, Marks: invalid})
	if err != nil || len(failed.Errors) < 2 || failed.Digest != "" {
		t.Fatalf("预检未同时定位重叠和策略错误: %#v %v", failed, err)
	}
	after, _ := repository.Audit(value.ID)
	if len(after) != len(before) {
		t.Fatal("只读预检不应写入审计事件")
	}
}

func TestAuditDiagnosticsCannotBeHiddenByFilter(t *testing.T) {
	service, _, root := newExtensionService(t)
	value := createExtensionArchive(t, service, "audit-diagnostic")
	path := filepath.Join(root, "audit", value.ID+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "建立访谈档案", "改动访谈档案", 1))
	if err := os.WriteFile(path, data, 0o640); err != nil {
		t.Fatal(err)
	}
	page, err := service.AuditTimeline(value.ID, AuditQuery{Actor: "不存在的操作人"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Overview.IntegrityStatus != "broken" || page.Overview.BrokenSequence != 1 || len(page.Events) != 0 {
		t.Fatalf("筛选掩盖了审计损坏: %#v", page)
	}
	if _, err := service.Freeze(value.ID, VersionInput{ExpectedVersion: value.Version, ActionKey: "freeze"}, "复核员"); err == nil {
		t.Fatal("审计损坏时不应允许冻结")
	}
}

func TestReviewQueueAndMixedBatchDecision(t *testing.T) {
	service, repository, _ := newExtensionService(t)
	value := createExtensionArchive(t, service, "review-batch")
	var err error
	value, err = service.SetConsent(value.ID, ConsentInput{ExpectedVersion: value.Version, AllowedUses: []string{"研究"}, NameDisclosure: archive.DisclosurePseudonym, RecordedBy: "甲", ActionKey: "consent"}, "甲")
	if err != nil {
		t.Fatal(err)
	}
	segments, err := service.UpsertSegments(value.ID, SegmentBatchInput{ExpectedVersion: value.Version, ActionKey: "segments", Segments: []SegmentBatchItem{{SegmentID: "S1", SpeakerCode: "P", SourceText: "李明到场"}, {SegmentID: "S2", SpeakerCode: "P", SourceText: "王芳到场"}}}, "甲")
	if err != nil {
		t.Fatal(err)
	}
	value, err = repository.Load(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	value, err = service.AddMark(value.ID, MarkInput{ExpectedVersion: segments.ArchiveVersion, ID: "M1", SegmentID: "S1", StartOffset: 0, EndOffset: 2, Category: archive.CategoryIdentity, Strategy: archive.StrategyReplace, Replacement: "P-1", ActionKey: "m1"}, "甲")
	if err != nil {
		t.Fatal(err)
	}
	value, err = service.AddMark(value.ID, MarkInput{ExpectedVersion: value.Version, ID: "M2", SegmentID: "S2", StartOffset: 0, EndOffset: 2, Category: archive.CategoryIdentity, Strategy: archive.StrategyReplace, Replacement: "P-2", ActionKey: "m2"}, "甲")
	if err != nil {
		t.Fatal(err)
	}
	value, err = service.SubmitForRedaction(value.ID, VersionInput{ExpectedVersion: value.Version, ActionKey: "submit"}, "甲")
	if err != nil {
		t.Fatal(err)
	}
	value, err = service.GenerateRedaction(value.ID, VersionInput{ExpectedVersion: value.Version, ActionKey: "generate"}, "甲")
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := service.ReviewTasks(ReviewTaskFilter{ArchiveStatus: archive.StatusPendingReview})
	if err != nil || len(tasks) != 2 || tasks[0].ArchiveID != value.ID || tasks[0].Original == "" {
		t.Fatalf("复核队列不完整: %#v %v", tasks, err)
	}
	result, err := service.ReviewBatch(value.ID, ReviewBatchInput{ExpectedVersion: value.Version, ActionKey: "decision", Decisions: []archive.ReviewDecision{{MarkID: "M1", Approved: true}, {MarkID: "M2", Reason: "代号与馆内映射冲突，需更换"}}}, "复核员")
	if err != nil {
		t.Fatal(err)
	}
	if result.ApprovedCount != 1 || result.RejectedCount != 1 || len(result.AffectedSegments) != 1 || result.AffectedSegments[0] != "S2" {
		t.Fatalf("批量裁决结果错误: %#v", result)
	}
	loaded, _ := repository.Load(value.ID)
	if loaded.Status != archive.StatusNeedsChanges || loaded.Marks[0].ReviewStatus != archive.ReviewApproved || loaded.Marks[1].ReviewStatus != archive.ReviewRejected || loaded.Affected["S1"] || !loaded.Affected["S2"] {
		t.Fatalf("批量裁决状态错误: %#v", loaded)
	}
	stable, err := service.ReviewBatch(value.ID, ReviewBatchInput{ExpectedVersion: value.Version, ActionKey: "decision"}, "复核员")
	if err != nil || stable.ArchiveVersion != result.ArchiveVersion {
		t.Fatalf("重复批量裁决未复用首次结果: %#v %v", stable, err)
	}
}
