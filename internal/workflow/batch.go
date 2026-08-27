package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/redaction"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/store"
)

type SegmentBatchInput struct {
	ExpectedVersion int64              `json:"expectedVersion"`
	ActionKey       string             `json:"actionKey"`
	Segments        []SegmentBatchItem `json:"segments"`
}

type SegmentBatchItem struct {
	SegmentID        string `json:"segmentId"`
	SpeakerCode      string `json:"speakerCode"`
	SourceText       string `json:"sourceText"`
	ExpectedRevision int    `json:"expectedRevision"`
}

type SegmentBatchResult struct {
	ArchiveVersion int64                     `json:"archiveVersion"`
	Results        []archive.SegmentMutation `json:"results"`
}

type MarkCandidate struct {
	ID          string                    `json:"id"`
	SegmentID   string                    `json:"segmentId"`
	StartOffset int                       `json:"startOffset"`
	EndOffset   int                       `json:"endOffset"`
	Category    archive.MarkCategory      `json:"category"`
	Strategy    archive.RedactionStrategy `json:"strategy"`
	Replacement string                    `json:"replacement"`
}

type MarkPreflightInput struct {
	ExpectedVersion int64           `json:"expectedVersion"`
	Marks           []MarkCandidate `json:"marks"`
}

type MarkCandidatePreview struct {
	ID          string `json:"id"`
	SegmentID   string `json:"segmentId"`
	StartOffset int    `json:"startOffset"`
	EndOffset   int    `json:"endOffset"`
	Original    string `json:"original"`
	Redacted    string `json:"redacted"`
}

type MarkPreflight struct {
	ArchiveVersion  int64                  `json:"archiveVersion"`
	ConsentRevision int                    `json:"consentRevision"`
	Previews        []MarkCandidatePreview `json:"previews"`
	Errors          []archive.BatchError   `json:"errors"`
	Digest          string                 `json:"digest,omitempty"`
}

type MarkBatchInput struct {
	ExpectedVersion int64           `json:"expectedVersion"`
	PreflightDigest string          `json:"preflightDigest"`
	ActionKey       string          `json:"actionKey"`
	Marks           []MarkCandidate `json:"marks"`
}

type MarkBatchResult struct {
	ArchiveVersion  int64                     `json:"archiveVersion"`
	PreflightDigest string                    `json:"preflightDigest"`
	Marks           []archive.SensitivityMark `json:"marks"`
}

type ReviewBatchInput struct {
	ExpectedVersion int64                    `json:"expectedVersion"`
	ActionKey       string                   `json:"actionKey"`
	Decisions       []archive.ReviewDecision `json:"decisions"`
}

type ReviewBatchResult struct {
	ArchiveVersion   int64    `json:"archiveVersion"`
	ApprovedCount    int      `json:"approvedCount"`
	RejectedCount    int      `json:"rejectedCount"`
	AffectedSegments []string `json:"affectedSegments"`
}

func (s *Service) UpsertSegments(id string, input SegmentBatchInput, actor string) (SegmentBatchResult, error) {
	var prior SegmentBatchResult
	if ok, err := s.idempotentResult(id, input.ActionKey, &prior); err != nil || ok {
		return prior, err
	}
	if err := validateActor(actor); err != nil {
		return prior, err
	}
	if len(input.Segments) == 0 {
		return prior, &archive.FieldError{Field: "segments", Message: "批量段落不能为空"}
	}
	value, err := s.repository.Load(id)
	if err != nil {
		return prior, err
	}
	if value.Version != input.ExpectedVersion {
		return prior, fmt.Errorf("%w: 预期 %d，当前 %d", archive.ErrVersionConflict, input.ExpectedVersion, value.Version)
	}
	items := make([]archive.TranscriptSegment, 0, len(input.Segments))
	for _, item := range input.Segments {
		items = append(items, archive.TranscriptSegment{SegmentID: item.SegmentID, SpeakerCode: item.SpeakerCode, SourceText: item.SourceText, Revision: item.ExpectedRevision})
	}
	results, err := value.UpsertSegments(items, s.now().UTC())
	if err != nil {
		return prior, err
	}
	counts := map[string]int{}
	for _, result := range results {
		counts[result.Result]++
	}
	prior = SegmentBatchResult{ArchiveVersion: input.ExpectedVersion + 1, Results: results}
	data, _ := json.Marshal(prior)
	detail := fmt.Sprintf("批量段落：新增 %d、修订 %d、未变化 %d", counts["added"], counts["revised"], counts["unchanged"])
	_, err = s.repository.Commit(store.CommitRequest{Archive: value, ExpectedVersion: input.ExpectedVersion, Actor: actor, Action: "segments.batch_upserted", Detail: detail, ActionKey: input.ActionKey, Result: data, At: s.now().UTC()})
	return prior, err
}

func (s *Service) PreflightMarks(id string, input MarkPreflightInput) (MarkPreflight, error) {
	if len(input.Marks) == 0 {
		return MarkPreflight{}, &archive.FieldError{Field: "marks", Message: "候选标注不能为空"}
	}
	value, err := s.repository.Load(id)
	if err != nil {
		return MarkPreflight{}, err
	}
	if value.Version != input.ExpectedVersion {
		return MarkPreflight{}, fmt.Errorf("%w: 预期 %d，当前 %d", archive.ErrVersionConflict, input.ExpectedVersion, value.Version)
	}
	result := MarkPreflight{ArchiveVersion: value.Version, ConsentRevision: value.ConsentRevision, Previews: []MarkCandidatePreview{}, Errors: []archive.BatchError{}}
	seen := map[string]int{}
	for index, candidate := range input.Marks {
		candidate.ID, candidate.SegmentID = strings.TrimSpace(candidate.ID), strings.TrimSpace(candidate.SegmentID)
		if candidate.ID == "" {
			result.Errors = append(result.Errors, batchMarkError(index, candidate, "id", "required", "标注编号不能为空"))
			continue
		}
		if first, ok := seen[candidate.ID]; ok {
			result.Errors = append(result.Errors, batchMarkError(index, candidate, "id", "duplicate", fmt.Sprintf("标注编号与第 %d 条重复", first+1)))
			continue
		}
		seen[candidate.ID] = index
		segment, ok := value.Segment(candidate.SegmentID)
		if !ok {
			result.Errors = append(result.Errors, batchMarkError(index, candidate, "segmentId", "not_found", "标注引用的段落不存在"))
			continue
		}
		runes := []rune(segment.SourceText)
		if candidate.StartOffset < 0 || candidate.EndOffset <= candidate.StartOffset || candidate.EndOffset > len(runes) {
			result.Errors = append(result.Errors, batchMarkError(index, candidate, "offset", "range_invalid", fmt.Sprintf("标注范围必须在 0 到 %d 个字符内", len(runes))))
			continue
		}
		after := candidate.Replacement
		if candidate.Strategy == archive.StrategyDelete {
			after = ""
		}
		result.Previews = append(result.Previews, MarkCandidatePreview{ID: candidate.ID, SegmentID: candidate.SegmentID, StartOffset: candidate.StartOffset, EndOffset: candidate.EndOffset, Original: string(runes[candidate.StartOffset:candidate.EndOffset]), Redacted: after})
		for _, existing := range value.Marks {
			if existing.ID == candidate.ID {
				result.Errors = append(result.Errors, batchMarkError(index, candidate, "id", "duplicate", "标注编号已存在"))
			}
			if existing.SegmentID == candidate.SegmentID && rangesOverlap(candidate.StartOffset, candidate.EndOffset, existing.StartOffset, existing.EndOffset) {
				result.Errors = append(result.Errors, batchMarkError(index, candidate, "offset", "overlap_existing", "标注范围与已有标注重叠"))
			}
		}
		for otherIndex := 0; otherIndex < index; otherIndex++ {
			other := input.Marks[otherIndex]
			if other.SegmentID == candidate.SegmentID && rangesOverlap(candidate.StartOffset, candidate.EndOffset, other.StartOffset, other.EndOffset) {
				result.Errors = append(result.Errors, batchMarkError(index, candidate, "offset", "overlap_batch", "标注范围与批次内其他标注重叠"))
			}
		}
	}
	clone := clone(value)
	marks := candidatesToMarks(input.Marks)
	if err := clone.AddMarks(marks, time.Time{}); err != nil {
		if len(result.Errors) == 0 {
			result.Errors = append(result.Errors, archive.BatchError{Field: "marks", Code: "invalid", Message: err.Error()})
		}
	} else {
		assessment := redaction.AssessPolicy(clone)
		candidateIndex := map[string]int{}
		for index, candidate := range input.Marks {
			candidateIndex[candidate.ID] = index
		}
		for _, issue := range assessment.Issues {
			if index, ok := candidateIndex[issue.MarkID]; ok {
				result.Errors = append(result.Errors, archive.BatchError{Index: index, SegmentID: issue.SegmentID, MarkID: issue.MarkID, Field: "strategy", Code: issue.Code, Message: issue.Message})
			}
		}
	}
	sort.Slice(result.Previews, func(i, j int) bool {
		if result.Previews[i].SegmentID != result.Previews[j].SegmentID {
			return result.Previews[i].SegmentID < result.Previews[j].SegmentID
		}
		return result.Previews[i].StartOffset < result.Previews[j].StartOffset
	})
	sort.Slice(result.Errors, func(i, j int) bool {
		if result.Errors[i].SegmentID != result.Errors[j].SegmentID {
			return result.Errors[i].SegmentID < result.Errors[j].SegmentID
		}
		if result.Errors[i].Index != result.Errors[j].Index {
			return result.Errors[i].Index < result.Errors[j].Index
		}
		return result.Errors[i].Code < result.Errors[j].Code
	})
	if len(result.Errors) == 0 {
		data, _ := json.Marshal(struct {
			ArchiveVersion  int64                  `json:"archiveVersion"`
			ConsentRevision int                    `json:"consentRevision"`
			Marks           []MarkCandidate        `json:"marks"`
			Previews        []MarkCandidatePreview `json:"previews"`
		}{result.ArchiveVersion, result.ConsentRevision, input.Marks, result.Previews})
		sum := sha256.Sum256(data)
		result.Digest = hex.EncodeToString(sum[:])
	}
	return result, nil
}

func (s *Service) CommitMarks(id string, input MarkBatchInput, actor string) (MarkBatchResult, error) {
	var prior MarkBatchResult
	if ok, err := s.idempotentResult(id, input.ActionKey, &prior); err != nil || ok {
		return prior, err
	}
	if err := validateActor(actor); err != nil {
		return prior, err
	}
	preflight, err := s.PreflightMarks(id, MarkPreflightInput{ExpectedVersion: input.ExpectedVersion, Marks: input.Marks})
	if err != nil {
		return prior, err
	}
	if len(preflight.Errors) > 0 {
		return prior, &archive.BatchValidationError{Kind: "marks", Items: preflight.Errors}
	}
	if input.PreflightDigest == "" || input.PreflightDigest != preflight.Digest {
		return prior, &archive.FieldError{Field: "preflightDigest", Message: "预检摘要已失效，请重新预检"}
	}
	value, err := s.repository.Load(id)
	if err != nil {
		return prior, err
	}
	marks := candidatesToMarks(input.Marks)
	if err := value.AddMarks(marks, s.now().UTC()); err != nil {
		return prior, err
	}
	prior = MarkBatchResult{ArchiveVersion: input.ExpectedVersion + 1, PreflightDigest: preflight.Digest, Marks: append([]archive.SensitivityMark(nil), marks...)}
	for index := range prior.Marks {
		prior.Marks[index].ReviewStatus = archive.ReviewPending
	}
	data, _ := json.Marshal(prior)
	_, err = s.repository.Commit(store.CommitRequest{Archive: value, ExpectedVersion: input.ExpectedVersion, Actor: actor, Action: "marks.batch_added", Detail: fmt.Sprintf("批量提交 %d 项敏感标注", len(marks)), ActionKey: input.ActionKey, Result: data, At: s.now().UTC()})
	return prior, err
}

func (s *Service) ReviewBatch(id string, input ReviewBatchInput, actor string) (ReviewBatchResult, error) {
	var prior ReviewBatchResult
	if ok, err := s.idempotentResult(id, input.ActionKey, &prior); err != nil || ok {
		return prior, err
	}
	if err := validateActor(actor); err != nil {
		return prior, err
	}
	if len(input.Decisions) == 0 {
		return prior, &archive.FieldError{Field: "decisions", Message: "批量裁决不能为空"}
	}
	value, err := s.repository.Load(id)
	if err != nil {
		return prior, err
	}
	if value.Version != input.ExpectedVersion {
		return prior, fmt.Errorf("%w: 预期 %d，当前 %d", archive.ErrVersionConflict, input.ExpectedVersion, value.Version)
	}
	preview, err := s.redactor.Generate(value)
	if err != nil {
		return prior, err
	}
	if preview.Report.BlockingCount > 0 {
		return prior, &archive.FieldError{Field: "risks", Message: "当前脱敏差异已失效：" + preview.Report.Risks[0].Message}
	}
	approved, rejected, affected, err := value.ReviewMarks(input.Decisions, s.now().UTC())
	if err != nil {
		return prior, err
	}
	prior = ReviewBatchResult{ArchiveVersion: input.ExpectedVersion + 1, ApprovedCount: approved, RejectedCount: rejected, AffectedSegments: affected}
	data, _ := json.Marshal(prior)
	detail := fmt.Sprintf("批量复核：确认 %d、退回 %d；受影响段落：%s", approved, rejected, strings.Join(affected, "、"))
	_, err = s.repository.Commit(store.CommitRequest{Archive: value, ExpectedVersion: input.ExpectedVersion, Actor: actor, Action: "reviews.batch_decided", Detail: detail, ActionKey: input.ActionKey, Result: data, At: s.now().UTC()})
	return prior, err
}

func (s *Service) idempotentResult(id, actionKey string, target any) (bool, error) {
	if actionKey == "" {
		return false, nil
	}
	event, ok, err := s.repository.FindAction(id, actionKey)
	if err != nil || !ok {
		return ok, err
	}
	if err := json.Unmarshal(event.Result, target); err != nil {
		return false, fmt.Errorf("解析重复动作结果: %w", err)
	}
	return true, nil
}

func clone(value *archive.InterviewArchive) *archive.InterviewArchive {
	data, _ := json.Marshal(value)
	var result archive.InterviewArchive
	_ = json.Unmarshal(data, &result)
	result.Normalize()
	return &result
}
func candidatesToMarks(values []MarkCandidate) []archive.SensitivityMark {
	result := make([]archive.SensitivityMark, 0, len(values))
	for _, value := range values {
		result = append(result, archive.SensitivityMark{ID: value.ID, SegmentID: value.SegmentID, StartOffset: value.StartOffset, EndOffset: value.EndOffset, Category: value.Category, Strategy: value.Strategy, Replacement: value.Replacement})
	}
	return result
}
func rangesOverlap(startA, endA, startB, endB int) bool { return startA < endB && startB < endA }
func batchMarkError(index int, candidate MarkCandidate, field, code, message string) archive.BatchError {
	return archive.BatchError{Index: index, SegmentID: candidate.SegmentID, MarkID: candidate.ID, Field: field, Code: code, Message: message}
}
