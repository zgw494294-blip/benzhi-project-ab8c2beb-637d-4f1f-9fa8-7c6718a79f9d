package redaction

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
)

type Engine struct{}

type Span struct {
	MarkID         string `json:"markId"`
	StartOffset    int    `json:"startOffset"`
	EndOffset      int    `json:"endOffset"`
	SanitizedStart int    `json:"sanitizedStart"`
	SanitizedEnd   int    `json:"sanitizedEnd"`
	Before         string `json:"before"`
	After          string `json:"after"`
	Category       string `json:"category"`
	Strategy       string `json:"strategy"`
}

type SegmentResult struct {
	SegmentID string `json:"segmentId"`
	Revision  int    `json:"revision"`
	Source    string `json:"source"`
	Sanitized string `json:"sanitized"`
	Spans     []Span `json:"spans"`
	Digest    string `json:"digest"`
}

type Issue struct {
	SegmentID   string `json:"segmentId"`
	MarkID      string `json:"markId,omitempty"`
	StartOffset int    `json:"startOffset,omitempty"`
	EndOffset   int    `json:"endOffset,omitempty"`
	Code        string `json:"code"`
	Message     string `json:"message"`
}

type Risk struct {
	Severity    string `json:"severity"`
	SegmentID   string `json:"segmentId"`
	MarkID      string `json:"markId,omitempty"`
	StartOffset int    `json:"startOffset,omitempty"`
	EndOffset   int    `json:"endOffset,omitempty"`
	Code        string `json:"code"`
	Message     string `json:"message"`
	Suggestion  string `json:"suggestion"`
}

type CoverageReport struct {
	ArchiveVersion  int64   `json:"archiveVersion"`
	ConsentRevision int     `json:"consentRevision"`
	Summary         Summary `json:"summary"`
	Risks           []Risk  `json:"risks"`
	BlockingCount   int     `json:"blockingCount"`
	Digest          string  `json:"digest"`
}

type Preview struct {
	Segments     []SegmentResult `json:"segments"`
	Issues       []Issue         `json:"issues"`
	PolicyDigest string          `json:"policyDigest"`
	Summary      Summary         `json:"summary"`
	Report       CoverageReport  `json:"report"`
	Digest       string          `json:"digest"`
}

func New() *Engine { return &Engine{} }

func (e *Engine) Generate(a *archive.InterviewArchive) (Preview, error) {
	if a.Consent == nil {
		return Preview{}, fmt.Errorf("授权约束尚未登记")
	}
	preview := Preview{Segments: []SegmentResult{}, Issues: []Issue{}}
	policy := AssessPolicy(a)
	preview.PolicyDigest = policy.Digest
	preview.Issues = append(preview.Issues, policy.Issues...)
	for _, segment := range a.Segments {
		result, issues := e.ProcessSegment(segment, a.MarksFor(segment.SegmentID))
		preview.Issues = append(preview.Issues, issues...)
		preview.Segments = append(preview.Segments, result)
	}
	sort.Slice(preview.Segments, func(i, j int) bool { return preview.Segments[i].SegmentID < preview.Segments[j].SegmentID })
	sort.Slice(preview.Issues, func(i, j int) bool {
		if preview.Issues[i].SegmentID == preview.Issues[j].SegmentID {
			return preview.Issues[i].MarkID < preview.Issues[j].MarkID
		}
		return preview.Issues[i].SegmentID < preview.Issues[j].SegmentID
	})
	preview.Summary = summarize(preview.Segments)
	preview.Report = buildReport(a, preview.Segments, preview.Issues, preview.Summary)
	canonical, err := json.Marshal(struct {
		Segments     []SegmentResult `json:"segments"`
		PolicyDigest string          `json:"policyDigest"`
		Summary      Summary         `json:"summary"`
		ReportDigest string          `json:"reportDigest"`
	}{preview.Segments, preview.PolicyDigest, preview.Summary, preview.Report.Digest})
	if err != nil {
		return Preview{}, err
	}
	sum := sha256.Sum256(canonical)
	preview.Digest = hex.EncodeToString(sum[:])
	return preview, nil
}

func buildReport(a *archive.InterviewArchive, segments []SegmentResult, issues []Issue, summary Summary) CoverageReport {
	report := CoverageReport{ArchiveVersion: a.Version, ConsentRevision: a.ConsentRevision, Summary: summary, Risks: []Risk{}}
	for _, issue := range issues {
		report.Risks = append(report.Risks, Risk{Severity: "blocking", SegmentID: issue.SegmentID, MarkID: issue.MarkID, StartOffset: issue.StartOffset, EndOffset: issue.EndOffset, Code: issue.Code, Message: issue.Message, Suggestion: suggestion(issue.Code)})
	}
	for _, segment := range segments {
		for _, span := range segment.Spans {
			before := strings.TrimSpace(span.Before)
			after := strings.TrimSpace(span.After)
			if before != "" && after != "" && strings.Contains(after, before) {
				report.Risks = append(report.Risks, Risk{Severity: "blocking", SegmentID: segment.SegmentID, MarkID: span.MarkID, StartOffset: span.StartOffset, EndOffset: span.EndOffset, Code: "sensitive_fragment_retained", Message: "处置文本仍包含原敏感片段", Suggestion: "改用不包含原文的代号、泛化文本或删除策略"})
			}
		}
		if digest, ok := a.ProcessedDigests[segment.SegmentID]; ok || segmentStored(a, segment.SegmentID) {
			if !ok || digest.Revision != segment.Revision || digest.Digest != segment.Digest {
				report.Risks = append(report.Risks, Risk{Severity: "blocking", SegmentID: segment.SegmentID, Code: "processing_revision_mismatch", Message: "段落修订与现有处理摘要不一致", Suggestion: "重新生成该段落的脱敏稿"})
			}
		}
	}
	sort.Slice(report.Risks, func(i, j int) bool {
		if report.Risks[i].SegmentID != report.Risks[j].SegmentID {
			return report.Risks[i].SegmentID < report.Risks[j].SegmentID
		}
		if report.Risks[i].StartOffset != report.Risks[j].StartOffset {
			return report.Risks[i].StartOffset < report.Risks[j].StartOffset
		}
		if report.Risks[i].MarkID != report.Risks[j].MarkID {
			return report.Risks[i].MarkID < report.Risks[j].MarkID
		}
		return report.Risks[i].Code < report.Risks[j].Code
	})
	for _, risk := range report.Risks {
		if risk.Severity == "blocking" {
			report.BlockingCount++
		}
	}
	payload := struct {
		ArchiveVersion  int64   `json:"archiveVersion"`
		ConsentRevision int     `json:"consentRevision"`
		Summary         Summary `json:"summary"`
		Risks           []Risk  `json:"risks"`
	}{report.ArchiveVersion, report.ConsentRevision, report.Summary, report.Risks}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	report.Digest = hex.EncodeToString(sum[:])
	return report
}

func segmentStored(a *archive.InterviewArchive, segmentID string) bool {
	segment, ok := a.Segment(segmentID)
	return ok && (segment.SanitizedText != "" || segment.SubmittedAt != nil)
}

func suggestion(code string) string {
	switch code {
	case "overlap", "range_invalid":
		return "修正 Unicode 字符范围，确保标注互不重叠且位于段落内"
	case "identity_strategy", "ineffective_replacement":
		return "改为不可识别代号或删除敏感身份"
	case "restricted_topic_retained":
		return "将禁用主题标注改为删除策略"
	default:
		return "检查标注与当前授权规则后重新生成脱敏稿"
	}
}

func (e *Engine) ProcessSegment(segment archive.TranscriptSegment, marks []archive.SensitivityMark) (SegmentResult, []Issue) {
	runes := []rune(segment.SourceText)
	ordered := append([]archive.SensitivityMark(nil), marks...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].StartOffset == ordered[j].StartOffset {
			return ordered[i].EndOffset < ordered[j].EndOffset
		}
		return ordered[i].StartOffset < ordered[j].StartOffset
	})
	result := SegmentResult{SegmentID: segment.SegmentID, Revision: segment.Revision, Source: segment.SourceText, Spans: []Span{}}
	issues := []Issue{}
	lastEnd := 0
	sanitizedPosition := 0
	var output strings.Builder
	for _, mark := range ordered {
		if mark.StartOffset < 0 || mark.EndOffset <= mark.StartOffset || mark.EndOffset > len(runes) {
			issues = append(issues, Issue{SegmentID: segment.SegmentID, MarkID: mark.ID, Code: "range_invalid", Message: fmt.Sprintf("标注范围越界，段落共有 %d 个字符", len(runes))})
			continue
		}
		if mark.StartOffset < lastEnd {
			issues = append(issues, Issue{SegmentID: segment.SegmentID, MarkID: mark.ID, Code: "overlap", Message: "敏感标注发生重叠"})
			continue
		}
		after, ok := disposition(mark)
		if !ok {
			issues = append(issues, Issue{SegmentID: segment.SegmentID, MarkID: mark.ID, Code: "undisposed", Message: "标注尚未配置有效处置方式"})
			continue
		}
		output.WriteString(string(runes[lastEnd:mark.StartOffset]))
		sanitizedPosition += mark.StartOffset - lastEnd
		before := string(runes[mark.StartOffset:mark.EndOffset])
		output.WriteString(after)
		afterLength := utf8.RuneCountInString(after)
		result.Spans = append(result.Spans, Span{MarkID: mark.ID, StartOffset: mark.StartOffset, EndOffset: mark.EndOffset, SanitizedStart: sanitizedPosition, SanitizedEnd: sanitizedPosition + afterLength, Before: before, After: after, Category: string(mark.Category), Strategy: string(mark.Strategy)})
		sanitizedPosition += afterLength
		lastEnd = mark.EndOffset
	}
	output.WriteString(string(runes[lastEnd:]))
	result.Sanitized = output.String()
	if !utf8.ValidString(result.Sanitized) {
		issues = append(issues, Issue{SegmentID: segment.SegmentID, Code: "invalid_utf8", Message: "脱敏结果不是有效 UTF-8 文本"})
	}
	canonical, _ := json.Marshal(struct {
		SegmentID string `json:"segmentId"`
		Revision  int    `json:"revision"`
		Sanitized string `json:"sanitized"`
		Spans     []Span `json:"spans"`
	}{result.SegmentID, result.Revision, result.Sanitized, result.Spans})
	sum := sha256.Sum256(canonical)
	result.Digest = hex.EncodeToString(sum[:])
	return result, issues
}

func disposition(mark archive.SensitivityMark) (string, bool) {
	switch mark.Strategy {
	case archive.StrategyDelete:
		return "", true
	case archive.StrategyReplace, archive.StrategyGeneralize:
		if strings.TrimSpace(mark.Replacement) == "" {
			return "", false
		}
		return mark.Replacement, true
	default:
		return "", false
	}
}
