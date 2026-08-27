package redaction

import (
	"testing"
	"time"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
)

func TestUnicodeDeterministicRedaction(t *testing.T) {
	a, _ := archive.NewInterview("a", "P", "2025-01-01", "研究", "甲", time.Now())
	_ = a.SetConsent(archive.ConsentScope{AllowedUses: []string{"研究"}, NameDisclosure: archive.DisclosurePseudonym, RecordedBy: "甲"}, time.Now())
	_ = a.UpsertSegment("S1", "P", "王芳住在上海市虹口区。", 0, time.Now())
	_ = a.AddMark(archive.SensitivityMark{ID: "M1", SegmentID: "S1", StartOffset: 0, EndOffset: 2, Category: archive.CategoryIdentity, Strategy: archive.StrategyReplace, Replacement: "受访者"}, time.Now())
	_ = a.AddMark(archive.SensitivityMark{ID: "M2", SegmentID: "S1", StartOffset: 4, EndOffset: 10, Category: archive.CategoryLocation, Strategy: archive.StrategyGeneralize, Replacement: "上海市某区"}, time.Now())
	first, err := New().Generate(a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New().Generate(a)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest {
		t.Fatal("同一输入摘要不稳定")
	}
	if got := first.Segments[0].Sanitized; got != "受访者住在上海市某区。" {
		t.Fatalf("脱敏稿错误: %q", got)
	}
}

func TestOverlapIsLocated(t *testing.T) {
	segment := archive.TranscriptSegment{SegmentID: "S1", Revision: 1, SourceText: "一二三四五"}
	marks := []archive.SensitivityMark{{ID: "M1", StartOffset: 0, EndOffset: 3, Strategy: archive.StrategyDelete}, {ID: "M2", StartOffset: 2, EndOffset: 4, Strategy: archive.StrategyDelete}}
	_, issues := New().ProcessSegment(segment, marks)
	if len(issues) != 1 || issues[0].Code != "overlap" || issues[0].MarkID != "M2" {
		t.Fatalf("重叠定位错误: %#v", issues)
	}
}

func TestCoverageReportLocatesResidualAndRevisionMismatch(t *testing.T) {
	a, _ := archive.NewInterview("risk", "P", "2025-01-01", "研究", "甲", time.Now())
	_ = a.SetConsent(archive.ConsentScope{AllowedUses: []string{"研究"}, NameDisclosure: archive.DisclosurePseudonym, RecordedBy: "甲"}, time.Now())
	_ = a.UpsertSegment("S1", "P", "李明讲述往事", 0, time.Now())
	_ = a.AddMark(archive.SensitivityMark{ID: "M1", SegmentID: "S1", StartOffset: 0, EndOffset: 2, Category: archive.CategoryIdentity, Strategy: archive.StrategyReplace, Replacement: "李明（代号）"}, time.Now())
	a.ProcessedDigests["S1"] = archive.ProcessingDigest{SegmentID: "S1", Revision: 0, Digest: "outdated"}
	first, err := New().Generate(a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New().Generate(a)
	if err != nil {
		t.Fatal(err)
	}
	if first.Report.Digest != second.Report.Digest || first.Report.BlockingCount != 2 {
		t.Fatalf("风险报告不稳定或未完整定位: %#v", first.Report)
	}
	if first.Report.Risks[0].SegmentID != "S1" || first.Report.Risks[0].Suggestion == "" {
		t.Fatalf("风险缺少定位与建议: %#v", first.Report.Risks)
	}
}
