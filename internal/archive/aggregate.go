package archive

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

func NewInterview(id, subjectCode, interviewDate, purpose, curator string, now time.Time) (*InterviewArchive, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, &FieldError{Field: "id", Message: "档案编号不能为空"}
	}
	if strings.TrimSpace(subjectCode) == "" {
		return nil, &FieldError{Field: "subjectCode", Message: "受访者代号不能为空"}
	}
	if _, err := time.Parse("2006-01-02", interviewDate); err != nil {
		return nil, &FieldError{Field: "interviewDate", Message: "采访日期必须为 YYYY-MM-DD"}
	}
	if strings.TrimSpace(purpose) == "" {
		return nil, &FieldError{Field: "purpose", Message: "用途说明不能为空"}
	}
	if strings.TrimSpace(curator) == "" {
		return nil, &FieldError{Field: "curator", Message: "整理员不能为空"}
	}
	return &InterviewArchive{
		ID: id, SubjectCode: strings.TrimSpace(subjectCode), InterviewDate: interviewDate,
		Purpose: strings.TrimSpace(purpose), Curator: strings.TrimSpace(curator), Status: StatusDraft,
		CreatedAt: now.UTC(), UpdatedAt: now.UTC(), Segments: []TranscriptSegment{},
		Marks: []SensitivityMark{}, Affected: map[string]bool{}, ProcessedDigests: map[string]ProcessingDigest{},
	}, nil
}

func (a *InterviewArchive) Normalize() {
	if a.Segments == nil {
		a.Segments = []TranscriptSegment{}
	}
	if a.Marks == nil {
		a.Marks = []SensitivityMark{}
	}
	if a.Affected == nil {
		a.Affected = map[string]bool{}
	}
	if a.ProcessedDigests == nil {
		a.ProcessedDigests = map[string]ProcessingDigest{}
	}
	if a.ConsentHistory == nil {
		a.ConsentHistory = []ConsentRevision{}
	}
	if a.ConsentImpacts == nil {
		a.ConsentImpacts = []ConsentImpact{}
	}
	if a.Consent != nil && a.ConsentRevision == 0 {
		a.ConsentRevision = 1
		a.ConsentHistory = []ConsentRevision{{Revision: 1, Effective: true, ChangedBy: a.Consent.RecordedBy, ChangedAt: a.Consent.RecordedAt, Snapshot: *a.Consent, Changes: []FieldChange{}}}
	}
}

func (a *InterviewArchive) SetConsent(c ConsentScope, now time.Time) error {
	return a.SetConsentRevision(c, a.ConsentRevision, nil, now)
}

func (a *InterviewArchive) SetConsentRevision(c ConsentScope, expectedRevision int, impacts []ConsentImpact, now time.Time) error {
	if a.Status != StatusDraft && a.Status != StatusNeedsChanges {
		return &StateError{Status: a.Status, Action: "登记授权"}
	}
	if expectedRevision != a.ConsentRevision {
		return &ConsentConflictError{CurrentRevision: a.ConsentRevision, ChangedFields: a.ConsentChangedFieldsSince(expectedRevision)}
	}
	c.ArchiveID = a.ID
	if len(nonblank(c.AllowedUses)) == 0 {
		return &FieldError{Field: "allowedUses", Message: "至少登记一项允许用途"}
	}
	if !validDisclosure(c.NameDisclosure) {
		return &FieldError{Field: "nameDisclosure", Message: "姓名披露规则无效"}
	}
	if strings.TrimSpace(c.RecordedBy) == "" {
		return &FieldError{Field: "recordedBy", Message: "授权记录人不能为空"}
	}
	if c.SealedUntil != "" {
		if _, err := time.Parse("2006-01-02", c.SealedUntil); err != nil {
			return &FieldError{Field: "sealedUntil", Message: "封存截止日期必须为 YYYY-MM-DD"}
		}
	}
	c.AllowedUses = unique(nonblank(c.AllowedUses))
	c.RestrictedTopics = unique(nonblank(c.RestrictedTopics))
	c.RecordedBy = strings.TrimSpace(c.RecordedBy)
	c.RecordedAt = now.UTC()
	changes := consentChanges(a.Consent, c)
	for i := range a.ConsentHistory {
		a.ConsentHistory[i].Effective = false
	}
	a.ConsentRevision++
	revision := ConsentRevision{Revision: a.ConsentRevision, Effective: true, ChangedBy: c.RecordedBy, ChangedAt: c.RecordedAt, Snapshot: c, Changes: changes}
	a.ConsentHistory = append([]ConsentRevision{revision}, a.ConsentHistory...)
	a.Consent = &c
	a.ConsentImpacts = append([]ConsentImpact(nil), impacts...)
	for _, impact := range impacts {
		if a.Status == StatusNeedsChanges && impact.SegmentID != "" {
			a.Affected[impact.SegmentID] = true
		}
	}
	a.touch(now)
	return nil
}

func (a *InterviewArchive) ConsentChangedFieldsSince(revision int) []string {
	seen := map[string]bool{}
	for _, item := range a.ConsentHistory {
		if item.Revision <= revision {
			continue
		}
		for _, change := range item.Changes {
			seen[change.Field] = true
		}
	}
	fields := make([]string, 0, len(seen))
	for field := range seen {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

func (a *InterviewArchive) UpsertSegment(segmentID, speaker, text string, expectedRevision int, now time.Time) error {
	if a.Status != StatusDraft && a.Status != StatusPendingRedaction && a.Status != StatusNeedsChanges {
		return &StateError{Status: a.Status, Action: "修订转写段落"}
	}
	segmentID, speaker, text = strings.TrimSpace(segmentID), strings.TrimSpace(speaker), strings.TrimSpace(text)
	if segmentID == "" {
		return &FieldError{Field: "segmentId", Message: "稳定段落编号不能为空"}
	}
	if speaker == "" {
		return &FieldError{Field: "speakerCode", Message: "说话人代号不能为空"}
	}
	if text == "" {
		return &FieldError{Field: "sourceText", Message: "转写文本不能为空"}
	}
	if a.Status == StatusNeedsChanges && !a.Affected[segmentID] {
		return &FieldError{Field: "segmentId", Message: "退回后只能修改受影响段落"}
	}
	idx := a.segmentIndex(segmentID)
	if idx < 0 {
		if expectedRevision != 0 {
			return &FieldError{Field: "expectedRevision", Message: "新段落的预期修订号必须为 0"}
		}
		a.Segments = append(a.Segments, TranscriptSegment{ArchiveID: a.ID, SegmentID: segmentID, Revision: 1, SourceText: text, SpeakerCode: speaker})
	} else {
		s := &a.Segments[idx]
		if s.Locked {
			return &FieldError{Field: "segmentId", Message: "冻结段落禁止修改"}
		}
		if expectedRevision != s.Revision {
			return &FieldError{Field: "expectedRevision", Message: fmt.Sprintf("修订号已变化，当前为 %d", s.Revision)}
		}
		if s.SourceText == text && s.SpeakerCode == speaker {
			return nil
		}
		s.Revision++
		s.SourceText, s.SpeakerCode, s.SanitizedText, s.SubmittedAt = text, speaker, "", nil
		delete(a.ProcessedDigests, segmentID)
		for i := range a.Marks {
			if a.Marks[i].SegmentID == segmentID {
				a.Marks[i].ReviewStatus = ReviewPending
				a.Marks[i].ReviewReason = ""
			}
		}
	}
	sort.Slice(a.Segments, func(i, j int) bool { return a.Segments[i].SegmentID < a.Segments[j].SegmentID })
	a.touch(now)
	return nil
}

func (a *InterviewArchive) UpsertSegments(inputs []TranscriptSegment, now time.Time) ([]SegmentMutation, error) {
	seen := map[string]int{}
	batchErrors := []BatchError{}
	for index, input := range inputs {
		id := strings.TrimSpace(input.SegmentID)
		if first, ok := seen[id]; ok {
			batchErrors = append(batchErrors, BatchError{Index: index, SegmentID: id, Field: "segmentId", Code: "duplicate", Message: fmt.Sprintf("段落编号与第 %d 条重复", first+1)})
			continue
		}
		seen[id] = index
		probe := cloneForMutation(a)
		if err := probe.UpsertSegment(input.SegmentID, input.SpeakerCode, input.SourceText, input.Revision, now); err != nil {
			field, message := "segment", err.Error()
			var fieldErr *FieldError
			if errors.As(err, &fieldErr) {
				field, message = fieldErr.Field, fieldErr.Message
			}
			batchErrors = append(batchErrors, BatchError{Index: index, SegmentID: input.SegmentID, Field: field, Code: "invalid", Message: message})
		}
	}
	if len(batchErrors) > 0 {
		sort.Slice(batchErrors, func(i, j int) bool {
			if batchErrors[i].SegmentID != batchErrors[j].SegmentID {
				return batchErrors[i].SegmentID < batchErrors[j].SegmentID
			}
			return batchErrors[i].Index < batchErrors[j].Index
		})
		return nil, &BatchValidationError{Kind: "segments", Items: batchErrors}
	}
	clone := cloneForMutation(a)
	results := make([]SegmentMutation, 0, len(inputs))
	for _, input := range inputs {
		before, exists := clone.Segment(input.SegmentID)
		if err := clone.UpsertSegment(input.SegmentID, input.SpeakerCode, input.SourceText, input.Revision, now); err != nil {
			return nil, err
		}
		after, _ := clone.Segment(strings.TrimSpace(input.SegmentID))
		result := "added"
		if exists {
			result = "unchanged"
			if before.SourceText != after.SourceText || before.SpeakerCode != after.SpeakerCode {
				result = "revised"
			}
		}
		results = append(results, SegmentMutation{SegmentID: after.SegmentID, Result: result, Revision: after.Revision})
	}
	*a = clone
	sort.Slice(results, func(i, j int) bool { return results[i].SegmentID < results[j].SegmentID })
	return results, nil
}

func (a *InterviewArchive) AddMark(m SensitivityMark, now time.Time) error {
	if a.Status != StatusDraft && a.Status != StatusPendingRedaction && a.Status != StatusNeedsChanges {
		return &StateError{Status: a.Status, Action: "添加敏感标注"}
	}
	if strings.TrimSpace(m.ID) == "" {
		return &FieldError{Field: "id", Message: "标注编号不能为空"}
	}
	if a.markIndex(m.ID) >= 0 {
		return &FieldError{Field: "id", Message: "标注编号已存在"}
	}
	idx := a.segmentIndex(m.SegmentID)
	if idx < 0 {
		return &FieldError{Field: "segmentId", Message: "标注引用的段落不存在"}
	}
	if a.Status == StatusNeedsChanges && !a.Affected[m.SegmentID] {
		return &FieldError{Field: "segmentId", Message: "退回后只能标注受影响段落"}
	}
	if !validCategory(m.Category) {
		return &FieldError{Field: "category", Message: "敏感类别无效"}
	}
	if !validStrategy(m.Strategy) {
		return &FieldError{Field: "strategy", Message: "处置方式无效"}
	}
	length := utf8.RuneCountInString(a.Segments[idx].SourceText)
	if m.StartOffset < 0 || m.EndOffset <= m.StartOffset || m.EndOffset > length {
		return &FieldError{Field: "offset", Message: fmt.Sprintf("标注范围必须在 0 到 %d 个字符内", length)}
	}
	if m.Strategy != StrategyDelete && strings.TrimSpace(m.Replacement) == "" {
		return &FieldError{Field: "replacement", Message: "替换或泛化必须填写处置文本"}
	}
	if m.Strategy == StrategyDelete {
		m.Replacement = ""
	}
	m.ReviewStatus, m.ReviewReason = ReviewPending, ""
	a.Marks = append(a.Marks, m)
	sort.Slice(a.Marks, func(i, j int) bool {
		if a.Marks[i].SegmentID == a.Marks[j].SegmentID {
			return a.Marks[i].StartOffset < a.Marks[j].StartOffset
		}
		return a.Marks[i].SegmentID < a.Marks[j].SegmentID
	})
	a.touch(now)
	return nil
}

func (a *InterviewArchive) AddMarks(marks []SensitivityMark, now time.Time) error {
	clone := *a
	clone.Marks = append([]SensitivityMark(nil), a.Marks...)
	clone.Affected = copyBoolMap(a.Affected)
	for _, mark := range marks {
		if err := clone.AddMark(mark, now); err != nil {
			return err
		}
	}
	*a = clone
	return nil
}

func (a *InterviewArchive) RemoveMark(markID string, now time.Time) error {
	if a.Status != StatusDraft && a.Status != StatusPendingRedaction && a.Status != StatusNeedsChanges {
		return &StateError{Status: a.Status, Action: "删除敏感标注"}
	}
	idx := a.markIndex(markID)
	if idx < 0 {
		return &FieldError{Field: "markId", Message: "标注不存在"}
	}
	if a.Status == StatusNeedsChanges && !a.Affected[a.Marks[idx].SegmentID] {
		return &FieldError{Field: "markId", Message: "只能删除受影响段落的标注"}
	}
	a.Marks = append(a.Marks[:idx], a.Marks[idx+1:]...)
	a.touch(now)
	return nil
}

func (a *InterviewArchive) SubmitForRedaction(now time.Time) error {
	if a.Status != StatusDraft && a.Status != StatusNeedsChanges {
		return &StateError{Status: a.Status, Action: "提交净化"}
	}
	if items := a.CompletenessIssues(); len(items) > 0 {
		return &FieldError{Field: "archive", Message: items[0].Message}
	}
	a.Status = StatusPendingRedaction
	a.touch(now)
	return nil
}

func (a *InterviewArchive) ApplySanitized(results map[string]ProcessingDigest, texts map[string]string, now time.Time) error {
	if a.Status != StatusPendingRedaction {
		return &StateError{Status: a.Status, Action: "生成脱敏稿"}
	}
	for i := range a.Segments {
		s := &a.Segments[i]
		result, ok := results[s.SegmentID]
		if !ok || result.Revision != s.Revision {
			return &FieldError{Field: "segments", Message: "每个当前段落都必须生成脱敏结果"}
		}
		s.SanitizedText = texts[s.SegmentID]
		t := now.UTC()
		s.SubmittedAt = &t
		a.ProcessedDigests[s.SegmentID] = result
	}
	for i := range a.Marks {
		if a.Affected[a.Marks[i].SegmentID] || a.Marks[i].ReviewStatus == ReviewRejected {
			a.Marks[i].ReviewStatus = ReviewPending
			a.Marks[i].ReviewReason = ""
		}
	}
	a.Affected = map[string]bool{}
	a.Status = StatusPendingReview
	a.touch(now)
	return nil
}

func (a *InterviewArchive) ReviewMark(markID string, approved bool, reason string, now time.Time) error {
	if a.Status != StatusPendingReview {
		return &StateError{Status: a.Status, Action: "复核标注"}
	}
	idx := a.markIndex(markID)
	if idx < 0 {
		return &FieldError{Field: "markId", Message: "标注不存在"}
	}
	m := &a.Marks[idx]
	if m.ReviewStatus == ReviewApproved && approved {
		return nil
	}
	if approved {
		m.ReviewStatus, m.ReviewReason = ReviewApproved, ""
	} else {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return &FieldError{Field: "reason", Message: "退回必须填写结构化理由"}
		}
		m.ReviewStatus, m.ReviewReason = ReviewRejected, reason
		a.Affected[m.SegmentID] = true
		a.Status = StatusNeedsChanges
	}
	a.touch(now)
	return nil
}

func (a *InterviewArchive) ReviewMarks(decisions []ReviewDecision, now time.Time) (int, int, []string, error) {
	if a.Status != StatusPendingReview {
		return 0, 0, nil, &StateError{Status: a.Status, Action: "批量复核标注"}
	}
	seen := map[string]bool{}
	affected := map[string]bool{}
	for index, decision := range decisions {
		if seen[decision.MarkID] {
			return 0, 0, nil, &BatchValidationError{Kind: "reviews", Items: []BatchError{{Index: index, MarkID: decision.MarkID, Field: "markId", Code: "duplicate", Message: "批次内标注编号重复"}}}
		}
		seen[decision.MarkID] = true
		idx := a.markIndex(decision.MarkID)
		if idx < 0 {
			return 0, 0, nil, &BatchValidationError{Kind: "reviews", Items: []BatchError{{Index: index, MarkID: decision.MarkID, Field: "markId", Code: "not_found", Message: "标注不存在"}}}
		}
		if a.Marks[idx].ReviewStatus != ReviewPending {
			return 0, 0, nil, &BatchValidationError{Kind: "reviews", Items: []BatchError{{Index: index, MarkID: decision.MarkID, Field: "markId", Code: "not_pending", Message: "标注不是待复核项"}}}
		}
		if !decision.Approved && strings.TrimSpace(decision.Reason) == "" {
			return 0, 0, nil, &BatchValidationError{Kind: "reviews", Items: []BatchError{{Index: index, MarkID: decision.MarkID, Field: "reason", Code: "required", Message: "退回必须填写结构化理由"}}}
		}
	}
	approved, rejected := 0, 0
	for _, decision := range decisions {
		mark := &a.Marks[a.markIndex(decision.MarkID)]
		if decision.Approved {
			mark.ReviewStatus, mark.ReviewReason = ReviewApproved, ""
			approved++
		} else {
			mark.ReviewStatus, mark.ReviewReason = ReviewRejected, strings.TrimSpace(decision.Reason)
			affected[mark.SegmentID] = true
			rejected++
		}
	}
	segments := make([]string, 0, len(affected))
	if rejected > 0 {
		for segmentID := range affected {
			a.Affected[segmentID] = true
			segments = append(segments, segmentID)
		}
		a.Status = StatusNeedsChanges
	}
	sort.Strings(segments)
	a.touch(now)
	return approved, rejected, segments, nil
}

func (a *InterviewArchive) Freeze(now time.Time) error {
	if a.Status != StatusPendingReview {
		return &StateError{Status: a.Status, Action: "冻结版本"}
	}
	for _, m := range a.Marks {
		if m.ReviewStatus != ReviewApproved {
			return &FieldError{Field: "reviews", Message: "仍有敏感标注未通过复核"}
		}
	}
	for i := range a.Segments {
		a.Segments[i].Locked = true
	}
	a.FrozenVersion = a.Version + 1
	a.Status = StatusPendingApproval
	a.touch(now)
	return nil
}

func (a *InterviewArchive) Publish(manifestID, approver string, now time.Time) error {
	if a.Status != StatusPendingApproval {
		return &StateError{Status: a.Status, Action: "批准发布"}
	}
	if strings.TrimSpace(approver) == "" {
		return &FieldError{Field: "approvedBy", Message: "发布负责人不能为空"}
	}
	if strings.TrimSpace(manifestID) == "" {
		return &FieldError{Field: "manifestId", Message: "发布清单编号不能为空"}
	}
	t := now.UTC()
	a.ApprovedBy, a.ApprovedAt, a.ManifestID, a.Status = strings.TrimSpace(approver), &t, manifestID, StatusPublished
	a.touch(now)
	return nil
}

func (a *InterviewArchive) CompletenessIssues() []PendingItem {
	var result []PendingItem
	if a.Consent == nil {
		result = append(result, PendingItem{Kind: "consent", Message: "尚未登记授权约束"})
	} else {
		if len(nonblank(a.Consent.AllowedUses)) == 0 {
			result = append(result, PendingItem{Kind: "consent", Message: "授权缺少允许用途"})
		}
		if !validDisclosure(a.Consent.NameDisclosure) {
			result = append(result, PendingItem{Kind: "consent", Message: "授权缺少姓名披露规则"})
		}
	}
	if len(a.Segments) == 0 {
		result = append(result, PendingItem{Kind: "segment", Message: "至少需要一个转写段落"})
	}
	for _, s := range a.Segments {
		if strings.TrimSpace(s.SourceText) == "" {
			result = append(result, PendingItem{Kind: "segment", SegmentID: s.SegmentID, Message: "转写文本为空"})
		}
	}
	return result
}

func (a *InterviewArchive) PendingReviews() []PendingItem {
	var result []PendingItem
	for _, m := range a.Marks {
		if m.ReviewStatus != ReviewApproved {
			result = append(result, PendingItem{Kind: "review", SegmentID: m.SegmentID, MarkID: m.ID, Message: "敏感处置尚未通过复核"})
		}
	}
	return result
}

func (a *InterviewArchive) Segment(id string) (TranscriptSegment, bool) {
	i := a.segmentIndex(id)
	if i < 0 {
		return TranscriptSegment{}, false
	}
	return a.Segments[i], true
}

func (a *InterviewArchive) MarksFor(segmentID string) []SensitivityMark {
	var out []SensitivityMark
	for _, m := range a.Marks {
		if m.SegmentID == segmentID {
			out = append(out, m)
		}
	}
	return out
}

func (a *InterviewArchive) segmentIndex(id string) int {
	for i := range a.Segments {
		if a.Segments[i].SegmentID == id {
			return i
		}
	}
	return -1
}
func (a *InterviewArchive) markIndex(id string) int {
	for i := range a.Marks {
		if a.Marks[i].ID == id {
			return i
		}
	}
	return -1
}
func (a *InterviewArchive) touch(now time.Time) { a.UpdatedAt = now.UTC() }
