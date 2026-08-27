package archive

import (
	"errors"
	"testing"
	"time"
)

func TestArchiveStateAndAffectedReviewFlow(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	a, err := NewInterview("arc-1", "P-01", "2025-12-01", "学术研究", "整理员", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SubmitForRedaction(now); err == nil {
		t.Fatal("缺少授权和段落时不应允许提交")
	}
	if err := a.SetConsent(ConsentScope{AllowedUses: []string{"馆内研究"}, NameDisclosure: DisclosurePseudonym, RecordedBy: "整理员"}, now); err != nil {
		t.Fatal(err)
	}
	if err := a.UpsertSegment("S001", "P-01", "张三住在北京。", 0, now); err != nil {
		t.Fatal(err)
	}
	if err := a.UpsertSegment("S002", "P-01", "联系电话123。", 0, now); err != nil {
		t.Fatal(err)
	}
	if err := a.AddMark(SensitivityMark{ID: "M1", SegmentID: "S001", StartOffset: 0, EndOffset: 2, Category: CategoryIdentity, Strategy: StrategyReplace, Replacement: "某人"}, now); err != nil {
		t.Fatal(err)
	}
	if err := a.AddMark(SensitivityMark{ID: "M2", SegmentID: "S002", StartOffset: 4, EndOffset: 7, Category: CategoryContact, Strategy: StrategyDelete}, now); err != nil {
		t.Fatal(err)
	}
	if err := a.SubmitForRedaction(now); err != nil {
		t.Fatal(err)
	}
	results := map[string]ProcessingDigest{"S001": {SegmentID: "S001", Revision: 1, Digest: "a"}, "S002": {SegmentID: "S002", Revision: 1, Digest: "b"}}
	texts := map[string]string{"S001": "某人住在北京。", "S002": "联系电话。"}
	if err := a.ApplySanitized(results, texts, now); err != nil {
		t.Fatal(err)
	}
	if err := a.ReviewMark("M1", true, "", now); err != nil {
		t.Fatal(err)
	}
	if err := a.ReviewMark("M2", false, "号码仍可识别", now); err != nil {
		t.Fatal(err)
	}
	if a.Status != StatusNeedsChanges || !a.Affected["S002"] {
		t.Fatalf("退回范围错误: %#v", a.Affected)
	}
	if err := a.UpsertSegment("S001", "P-01", "试图修改", 1, now); err == nil {
		t.Fatal("不应修改未受影响段落")
	}
	if err := a.UpsertSegment("S002", "P-01", "联系电话456。", 1, now); err != nil {
		t.Fatal(err)
	}
	if err := a.SubmitForRedaction(now); err != nil {
		t.Fatal(err)
	}
	results["S002"] = ProcessingDigest{SegmentID: "S002", Revision: 2, Digest: "c"}
	texts["S002"] = "联系电话。"
	if err := a.ApplySanitized(results, texts, now); err != nil {
		t.Fatal(err)
	}
	if a.Marks[0].ReviewStatus != ReviewApproved {
		t.Fatal("未受影响段落的通过结果应保留")
	}
	if a.Marks[1].ReviewStatus != ReviewPending {
		t.Fatal("受影响段落应重新复核")
	}
}

func TestRevisionConflictAndFreezeGate(t *testing.T) {
	now := time.Now()
	a, _ := NewInterview("arc-2", "P-02", "2025-01-01", "研究", "甲", now)
	_ = a.SetConsent(ConsentScope{AllowedUses: []string{"研究"}, NameDisclosure: DisclosureForbidden, RecordedBy: "甲"}, now)
	_ = a.UpsertSegment("S1", "P-02", "文本", 0, now)
	if err := a.UpsertSegment("S1", "P-02", "新文本", 0, now); err == nil {
		t.Fatal("过期修订号应被拒绝")
	}
	if err := a.Freeze(now); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("意外错误: %v", err)
	}
}
