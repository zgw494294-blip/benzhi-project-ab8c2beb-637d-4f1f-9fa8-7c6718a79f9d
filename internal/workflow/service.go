package workflow

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/manifest"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/redaction"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/store"
)

type Clock func() time.Time

type Service struct {
	repository store.Repository
	redactor   *redaction.Engine
	now        Clock
}

type CreateArchiveInput struct {
	ID            string `json:"id"`
	SubjectCode   string `json:"subjectCode"`
	InterviewDate string `json:"interviewDate"`
	Purpose       string `json:"purpose"`
	Curator       string `json:"curator"`
	ActionKey     string `json:"actionKey"`
}

type ConsentInput struct {
	ExpectedVersion         int64                  `json:"expectedVersion"`
	ExpectedConsentRevision int                    `json:"expectedConsentRevision"`
	AllowedUses             []string               `json:"allowedUses"`
	RestrictedTopics        []string               `json:"restrictedTopics"`
	NameDisclosure          archive.NameDisclosure `json:"nameDisclosure"`
	SealedUntil             string                 `json:"sealedUntil"`
	RecordedBy              string                 `json:"recordedBy"`
	ActionKey               string                 `json:"actionKey"`
}

type SegmentInput struct {
	ExpectedVersion  int64  `json:"expectedVersion"`
	ExpectedRevision int    `json:"expectedRevision"`
	SegmentID        string `json:"segmentId"`
	SpeakerCode      string `json:"speakerCode"`
	SourceText       string `json:"sourceText"`
	ActionKey        string `json:"actionKey"`
}

type MarkInput struct {
	ExpectedVersion int64                     `json:"expectedVersion"`
	ID              string                    `json:"id"`
	SegmentID       string                    `json:"segmentId"`
	StartOffset     int                       `json:"startOffset"`
	EndOffset       int                       `json:"endOffset"`
	Category        archive.MarkCategory      `json:"category"`
	Strategy        archive.RedactionStrategy `json:"strategy"`
	Replacement     string                    `json:"replacement"`
	ActionKey       string                    `json:"actionKey"`
}

type VersionInput struct {
	ExpectedVersion int64  `json:"expectedVersion"`
	ActionKey       string `json:"actionKey"`
}

type ReviewInput struct {
	ExpectedVersion int64  `json:"expectedVersion"`
	MarkID          string `json:"markId"`
	Approved        bool   `json:"approved"`
	Reason          string `json:"reason"`
	ActionKey       string `json:"actionKey"`
}

type ApprovalInput struct {
	ExpectedVersion int64  `json:"expectedVersion"`
	ApprovedBy      string `json:"approvedBy"`
	ActionKey       string `json:"actionKey"`
}

type ArchiveView struct {
	Archive       *archive.InterviewArchive `json:"archive"`
	Preview       redaction.Preview         `json:"preview"`
	Audit         []archive.AuditEvent      `json:"audit"`
	Manifest      *manifest.ReleaseManifest `json:"manifest,omitempty"`
	Gates         WorkflowGates             `json:"gates"`
	AuditOverview AuditOverview             `json:"auditOverview"`
}

type WorkflowGates struct {
	CanEditConsent      bool                  `json:"canEditConsent"`
	CanEditSegments     bool                  `json:"canEditSegments"`
	CanSubmit           bool                  `json:"canSubmit"`
	CanGenerate         bool                  `json:"canGenerate"`
	CanReview           bool                  `json:"canReview"`
	CanFreeze           bool                  `json:"canFreeze"`
	CanApprove          bool                  `json:"canApprove"`
	CanDownloadManifest bool                  `json:"canDownloadManifest"`
	Pending             []archive.PendingItem `json:"pending"`
	Warnings            []string              `json:"warnings"`
}

func New(repository store.Repository, redactor *redaction.Engine, clock Clock) *Service {
	if redactor == nil {
		redactor = redaction.New()
	}
	if clock == nil {
		clock = time.Now
	}
	return &Service{repository: repository, redactor: redactor, now: clock}
}

func (s *Service) Create(input CreateArchiveInput, actor string) (*archive.InterviewArchive, error) {
	if strings.TrimSpace(input.ID) == "" {
		input.ID = newID("arc")
	}
	if prior, ok, err := s.idempotent(input.ID, input.ActionKey); err != nil {
		return nil, err
	} else if ok {
		return prior, nil
	}
	now := s.now().UTC()
	value, err := archive.NewInterview(input.ID, input.SubjectCode, input.InterviewDate, input.Purpose, input.Curator, now)
	if err != nil {
		return nil, err
	}
	if err := validateActor(actor); err != nil {
		return nil, err
	}
	event, commitErr := s.repository.Commit(store.CommitRequest{Archive: value, ExpectedVersion: 0, Actor: actor, Action: "archive.created", Detail: "建立访谈档案", ActionKey: input.ActionKey, At: now})
	if commitErr != nil {
		if prior, ok, err := s.idempotent(input.ID, input.ActionKey); err != nil {
			return nil, err
		} else if ok {
			return prior, nil
		}
		return nil, commitErr
	}
	if event.ArchiveVersion != 1 {
		if prior, ok, err := s.idempotent(input.ID, input.ActionKey); err != nil {
			return nil, err
		} else if ok {
			return prior, nil
		}
	}
	return value, nil
}

func (s *Service) List() ([]*archive.InterviewArchive, error) { return s.repository.List() }

func (s *Service) Get(id string) (ArchiveView, error) {
	value, err := s.repository.Load(id)
	if err != nil {
		return ArchiveView{}, err
	}
	preview, previewErr := s.redactor.Generate(value)
	if previewErr != nil {
		preview = redaction.Preview{Segments: []redaction.SegmentResult{}, Issues: []redaction.Issue{{Code: "preview_unavailable", Message: previewErr.Error()}}}
	}
	events, integrity, err := s.repository.AuditDiagnostics(id)
	if err != nil {
		return ArchiveView{}, err
	}
	overview := auditOverview(integrity, len(events))
	view := ArchiveView{Archive: value, Preview: preview, Audit: events, AuditOverview: overview, Gates: buildGates(value, preview, s.now().UTC())}
	if !integrity.Intact {
		view.Gates.CanFreeze, view.Gates.CanApprove = false, false
		view.Gates.Pending = append(view.Gates.Pending, archive.PendingItem{Kind: "audit_integrity", Message: integrity.Reason})
	}
	if value.ManifestID != "" {
		data, loadErr := s.repository.LoadArtifact(id, value.ManifestID)
		if loadErr != nil {
			return ArchiveView{}, loadErr
		}
		m, parseErr := manifest.Parse(data)
		if parseErr != nil {
			return ArchiveView{}, parseErr
		}
		view.Manifest = &m
		view.AuditOverview.ManifestStatus = compareManifestAudit(m, integrity)
	}
	return view, nil
}

func (s *Service) SetConsent(id string, input ConsentInput, actor string) (*archive.InterviewArchive, error) {
	if err := validateActor(actor); err != nil {
		return nil, err
	}
	if prior, ok, err := s.idempotent(id, input.ActionKey); err != nil {
		return nil, err
	} else if ok {
		return prior, nil
	}
	value, err := s.repository.Load(id)
	if err != nil {
		return nil, err
	}
	if value.Version != input.ExpectedVersion || value.ConsentRevision != input.ExpectedConsentRevision {
		if prior, ok, err := s.idempotent(id, input.ActionKey); err != nil {
			return nil, err
		} else if ok {
			return prior, nil
		}
		return nil, &archive.ConsentConflictError{CurrentRevision: value.ConsentRevision, ChangedFields: value.ConsentChangedFieldsSince(input.ExpectedConsentRevision)}
	}
	now := s.now().UTC()
	consent := archive.ConsentScope{AllowedUses: input.AllowedUses, RestrictedTopics: input.RestrictedTopics, NameDisclosure: input.NameDisclosure, SealedUntil: input.SealedUntil, RecordedBy: input.RecordedBy}
	if err := value.SetConsentRevision(consent, input.ExpectedConsentRevision, nil, now); err != nil {
		return nil, err
	}
	if len(value.ConsentHistory) > 0 {
		value.ConsentHistory[0].ChangedBy = strings.TrimSpace(actor)
	}
	s.refreshConsentImpacts(value)
	changes := []string{}
	if len(value.ConsentHistory) > 0 {
		for _, change := range value.ConsentHistory[0].Changes {
			changes = append(changes, change.Field)
		}
	}
	detail := fmt.Sprintf("保存授权修订 %d；变化字段：%s", value.ConsentRevision, strings.Join(changes, "、"))
	event, commitErr := s.repository.Commit(store.CommitRequest{Archive: value, ExpectedVersion: input.ExpectedVersion, Actor: actor, Action: "consent.revised", Detail: detail, ActionKey: input.ActionKey, At: now})
	if commitErr != nil {
		if prior, ok, err := s.idempotent(id, input.ActionKey); err != nil {
			return nil, err
		} else if ok {
			return prior, nil
		}
		return nil, commitErr
	}
	if event.ArchiveVersion != input.ExpectedVersion+1 {
		if prior, ok, err := s.idempotent(id, input.ActionKey); err != nil {
			return nil, err
		} else if ok {
			return prior, nil
		}
	}
	return value, nil
}

func (s *Service) UpsertSegment(id string, input SegmentInput, actor string) (*archive.InterviewArchive, error) {
	detail := fmt.Sprintf("修订段落 %s", input.SegmentID)
	return s.change(id, input.ExpectedVersion, input.ActionKey, actor, "segment.upserted", detail, func(value *archive.InterviewArchive, now time.Time) error {
		return value.UpsertSegment(input.SegmentID, input.SpeakerCode, input.SourceText, input.ExpectedRevision, now)
	})
}

func (s *Service) AddMark(id string, input MarkInput, actor string) (*archive.InterviewArchive, error) {
	if input.ID == "" {
		input.ID = newID("mark")
	}
	detail := fmt.Sprintf("标注段落 %s 的敏感内容", input.SegmentID)
	return s.change(id, input.ExpectedVersion, input.ActionKey, actor, "mark.added", detail, func(value *archive.InterviewArchive, now time.Time) error {
		return value.AddMark(archive.SensitivityMark{ID: input.ID, SegmentID: input.SegmentID, StartOffset: input.StartOffset, EndOffset: input.EndOffset, Category: input.Category, Strategy: input.Strategy, Replacement: input.Replacement}, now)
	})
}

func (s *Service) RemoveMark(id, markID string, input VersionInput, actor string) (*archive.InterviewArchive, error) {
	return s.change(id, input.ExpectedVersion, input.ActionKey, actor, "mark.removed", "删除敏感标注 "+markID, func(value *archive.InterviewArchive, now time.Time) error { return value.RemoveMark(markID, now) })
}

func (s *Service) SubmitForRedaction(id string, input VersionInput, actor string) (*archive.InterviewArchive, error) {
	return s.change(id, input.ExpectedVersion, input.ActionKey, actor, "redaction.submitted", "材料完整性与当前授权修订检查通过并提交净化", func(value *archive.InterviewArchive, now time.Time) error {
		preview, err := s.redactor.Generate(value)
		if err != nil {
			return err
		}
		if preview.Report.BlockingCount > 0 {
			return &archive.FieldError{Field: "consentImpacts", Message: preview.Report.Risks[0].Message}
		}
		value.ConsentImpacts = []archive.ConsentImpact{}
		return value.SubmitForRedaction(now)
	})
}

func (s *Service) GenerateRedaction(id string, input VersionInput, actor string) (*archive.InterviewArchive, error) {
	return s.change(id, input.ExpectedVersion, input.ActionKey, actor, "redaction.generated", "确定性生成脱敏稿并进入复核", func(value *archive.InterviewArchive, now time.Time) error {
		preview, err := s.redactor.Generate(value)
		if err != nil {
			return err
		}
		if preview.Report.BlockingCount > 0 {
			return &archive.FieldError{Field: "risks", Message: preview.Report.Risks[0].Message}
		}
		digests := map[string]archive.ProcessingDigest{}
		texts := map[string]string{}
		for _, result := range preview.Segments {
			digests[result.SegmentID] = archive.ProcessingDigest{SegmentID: result.SegmentID, Revision: result.Revision, Digest: result.Digest}
			texts[result.SegmentID] = result.Sanitized
		}
		return value.ApplySanitized(digests, texts, now)
	})
}

func (s *Service) Review(id string, input ReviewInput, actor string) (*archive.InterviewArchive, error) {
	action, detail := "review.approved", "复核通过标注 "+input.MarkID
	if !input.Approved {
		action, detail = "review.rejected", "退回标注 "+input.MarkID+"："+strings.TrimSpace(input.Reason)
	}
	return s.change(id, input.ExpectedVersion, input.ActionKey, actor, action, detail, func(value *archive.InterviewArchive, now time.Time) error {
		return value.ReviewMark(input.MarkID, input.Approved, input.Reason, now)
	})
}

func (s *Service) Freeze(id string, input VersionInput, actor string) (*archive.InterviewArchive, error) {
	if err := s.requireAuditIntegrity(id); err != nil {
		return nil, err
	}
	return s.change(id, input.ExpectedVersion, input.ActionKey, actor, "archive.frozen", "全部复核通过，冻结待发布版本", func(value *archive.InterviewArchive, now time.Time) error { return value.Freeze(now) })
}

func (s *Service) Approve(id string, input ApprovalInput, actor string) (manifest.ReleaseManifest, error) {
	if err := validateActor(actor); err != nil {
		return manifest.ReleaseManifest{}, err
	}
	if prior, ok, err := s.idempotent(id, input.ActionKey); err != nil {
		return manifest.ReleaseManifest{}, err
	} else if ok && prior.ManifestID != "" {
		return s.Manifest(id)
	}
	value, err := s.repository.Load(id)
	if err != nil {
		return manifest.ReleaseManifest{}, err
	}
	if value.Version != input.ExpectedVersion {
		if prior, ok, err := s.idempotent(id, input.ActionKey); err != nil {
			return manifest.ReleaseManifest{}, err
		} else if ok && prior.ManifestID != "" {
			return s.Manifest(id)
		}
		return manifest.ReleaseManifest{}, archive.ErrVersionConflict
	}
	if err := s.requireAuditIntegrity(id); err != nil {
		return manifest.ReleaseManifest{}, err
	}
	now := s.now().UTC()
	manifestID := newID("manifest")
	if err := value.Publish(manifestID, input.ApprovedBy, now); err != nil {
		return manifest.ReleaseManifest{}, err
	}
	result, _ := json.Marshal(struct {
		ManifestID string `json:"manifestId"`
	}{manifestID})
	commitEvent, commitErr := s.repository.Commit(store.CommitRequest{Archive: value, ExpectedVersion: input.ExpectedVersion, Actor: actor, Action: "release.approved", Detail: "批准发布并签发可验证清单", ActionKey: input.ActionKey, Result: result, At: now})
	if commitErr != nil {
		if prior, ok, err := s.idempotent(id, input.ActionKey); err != nil {
			return manifest.ReleaseManifest{}, err
		} else if ok && prior.ManifestID != "" {
			return s.Manifest(id)
		}
		return manifest.ReleaseManifest{}, commitErr
	}
	if commitEvent.ArchiveVersion != input.ExpectedVersion+1 {
		if prior, ok, err := s.idempotent(id, input.ActionKey); err != nil {
			return manifest.ReleaseManifest{}, err
		} else if ok && prior.ManifestID != "" {
			return s.Manifest(id)
		}
	}
	committed, err := s.repository.Load(id)
	if err != nil {
		return manifest.ReleaseManifest{}, err
	}
	events, err := s.repository.Audit(id)
	if err != nil {
		return manifest.ReleaseManifest{}, err
	}
	m, err := manifest.Generate(manifestID, input.ApprovedBy, now, committed, events)
	if err != nil {
		return manifest.ReleaseManifest{}, err
	}
	data, err := manifest.Marshal(m)
	if err != nil {
		return manifest.ReleaseManifest{}, err
	}
	if err := s.repository.SaveArtifact(id, manifestID, data); err != nil {
		return manifest.ReleaseManifest{}, err
	}
	return m, nil
}

func (s *Service) Manifest(id string) (manifest.ReleaseManifest, error) {
	value, err := s.repository.Load(id)
	if err != nil {
		return manifest.ReleaseManifest{}, err
	}
	if value.ManifestID == "" {
		return manifest.ReleaseManifest{}, archive.ErrNotFound
	}
	data, err := s.repository.LoadArtifact(id, value.ManifestID)
	if err != nil {
		return manifest.ReleaseManifest{}, err
	}
	result, err := manifest.Parse(data)
	if err != nil {
		return manifest.ReleaseManifest{}, err
	}
	if err := manifest.VerifyAgainst(result, value); err != nil {
		return manifest.ReleaseManifest{}, err
	}
	return result, nil
}

func (s *Service) VerifyManifest(data []byte) (manifest.ReleaseManifest, error) {
	return manifest.Parse(data)
}

func (s *Service) change(id string, expected int64, actionKey, actor, action, detail string, apply func(*archive.InterviewArchive, time.Time) error) (*archive.InterviewArchive, error) {
	if err := validateActor(actor); err != nil {
		return nil, err
	}
	if prior, ok, err := s.idempotent(id, actionKey); err != nil {
		return nil, err
	} else if ok {
		return prior, nil
	}
	value, err := s.repository.Load(id)
	if err != nil {
		return nil, err
	}
	if value.Version != expected {
		if prior, ok, err := s.idempotent(id, actionKey); err != nil {
			return nil, err
		} else if ok {
			return prior, nil
		}
		return nil, fmt.Errorf("%w: 预期 %d，当前 %d", archive.ErrVersionConflict, expected, value.Version)
	}
	now := s.now().UTC()
	if err := apply(value, now); err != nil {
		return nil, err
	}
	s.refreshConsentImpacts(value)
	event, commitErr := s.repository.Commit(store.CommitRequest{Archive: value, ExpectedVersion: expected, Actor: actor, Action: action, Detail: detail, ActionKey: actionKey, At: now})
	if commitErr != nil {
		if prior, ok, err := s.idempotent(id, actionKey); err != nil {
			return nil, err
		} else if ok {
			return prior, nil
		}
		return nil, commitErr
	}
	if event.ArchiveVersion != expected+1 {
		if prior, ok, err := s.idempotent(id, actionKey); err != nil {
			return nil, err
		} else if ok {
			return prior, nil
		}
	}
	return value, nil
}

func (s *Service) refreshConsentImpacts(value *archive.InterviewArchive) {
	assessment := redaction.AssessPolicy(value)
	impacts := make([]archive.ConsentImpact, 0, len(assessment.Issues))
	for _, issue := range assessment.Issues {
		if issue.SegmentID == "" {
			continue
		}
		impacts = append(impacts, archive.ConsentImpact{SegmentID: issue.SegmentID, MarkID: issue.MarkID, Code: issue.Code, Message: issue.Message})
		if value.Status == archive.StatusNeedsChanges {
			value.Affected[issue.SegmentID] = true
		}
	}
	value.ConsentImpacts = impacts
}

func (s *Service) requireAuditIntegrity(id string) error {
	_, integrity, err := s.repository.AuditDiagnostics(id)
	if err != nil {
		return err
	}
	if !integrity.Intact {
		return &archive.FieldError{Field: "audit", Message: "审计链完整性校验失败：" + integrity.Reason}
	}
	return nil
}

func (s *Service) idempotent(id, actionKey string) (*archive.InterviewArchive, bool, error) {
	if actionKey == "" {
		return nil, false, nil
	}
	event, ok, err := s.repository.FindAction(id, actionKey)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	value, decodeErr := store.DecodeArchiveResult(event)
	if decodeErr == nil {
		return value, true, nil
	}
	value, err = s.repository.Load(id)
	return value, true, err
}

func validateActor(actor string) error {
	if strings.TrimSpace(actor) == "" {
		return &archive.FieldError{Field: "actor", Message: "操作人不能为空"}
	}
	return nil
}

func buildGates(value *archive.InterviewArchive, preview redaction.Preview, now time.Time) WorkflowGates {
	gates := WorkflowGates{Pending: []archive.PendingItem{}, Warnings: []string{}}
	gates.CanEditConsent = value.Status == archive.StatusDraft || value.Status == archive.StatusNeedsChanges
	gates.CanEditSegments = value.Status == archive.StatusDraft || value.Status == archive.StatusPendingRedaction || value.Status == archive.StatusNeedsChanges
	gates.CanReview = value.Status == archive.StatusPendingReview
	gates.CanDownloadManifest = value.Status == archive.StatusPublished && value.ManifestID != ""
	completeness := value.CompletenessIssues()
	if value.Status == archive.StatusDraft || value.Status == archive.StatusNeedsChanges {
		gates.Pending = append(gates.Pending, completeness...)
		for _, impact := range value.ConsentImpacts {
			gates.Pending = append(gates.Pending, archive.PendingItem{Kind: impact.Code, SegmentID: impact.SegmentID, MarkID: impact.MarkID, Message: impact.Message})
		}
		gates.CanSubmit = len(completeness) == 0 && len(value.ConsentImpacts) == 0 && preview.Report.BlockingCount == 0
	}
	if value.Status == archive.StatusPendingRedaction {
		for _, issue := range preview.Issues {
			gates.Pending = append(gates.Pending, archive.PendingItem{Kind: issue.Code, SegmentID: issue.SegmentID, MarkID: issue.MarkID, Message: issue.Message})
		}
		gates.CanGenerate = preview.Report.BlockingCount == 0 && len(preview.Segments) == len(value.Segments)
	}
	if value.Status == archive.StatusPendingReview {
		pendingReviews := value.PendingReviews()
		gates.Pending = append(gates.Pending, pendingReviews...)
		gates.CanFreeze = len(pendingReviews) == 0
	}
	if value.Status == archive.StatusNeedsChanges {
		for segmentID := range value.Affected {
			gates.Pending = append(gates.Pending, archive.PendingItem{Kind: "returned_segment", SegmentID: segmentID, Message: "该段落被退回，修改后需再次生成并复核"})
		}
	}
	if value.Status == archive.StatusPendingApproval {
		gates.CanApprove = true
		if value.Consent != nil && value.Consent.SealedUntil != "" {
			sealed, err := time.Parse("2006-01-02", value.Consent.SealedUntil)
			today := dateOnly(now)
			if err == nil && today.Before(sealed) {
				gates.CanApprove = false
				gates.Pending = append(gates.Pending, archive.PendingItem{Kind: "sealed", Message: "档案仍在封存期，截止日期为 " + value.Consent.SealedUntil})
			}
		}
	}
	if value.Status == archive.StatusPublished {
		gates.Warnings = append(gates.Warnings, "已发布档案及冻结段落不可再修改")
	}
	return gates
}

func dateOnly(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}
func newID(prefix string) string {
	var raw [10]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	return prefix + "-" + hex.EncodeToString(raw[:])
}
