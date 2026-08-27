package archive

import "time"

type Status string

const (
	StatusDraft            Status = "draft"
	StatusPendingRedaction Status = "pending_redaction"
	StatusPendingReview    Status = "pending_review"
	StatusNeedsChanges     Status = "needs_changes"
	StatusPendingApproval  Status = "pending_approval"
	StatusPublished        Status = "published"
)

var statusLabels = map[Status]string{
	StatusDraft:            "草拟",
	StatusPendingRedaction: "待净化",
	StatusPendingReview:    "待复核",
	StatusNeedsChanges:     "需修改",
	StatusPendingApproval:  "待批准",
	StatusPublished:        "已发布",
}

func (s Status) Valid() bool {
	_, ok := statusLabels[s]
	return ok
}

func (s Status) Label() string { return statusLabels[s] }

type NameDisclosure string

const (
	DisclosureForbidden NameDisclosure = "forbidden"
	DisclosurePseudonym NameDisclosure = "pseudonym_only"
	DisclosureAllowed   NameDisclosure = "allowed"
)

type MarkCategory string

const (
	CategoryIdentity MarkCategory = "identity"
	CategoryLocation MarkCategory = "precise_location"
	CategoryContact  MarkCategory = "contact"
	CategoryTopic    MarkCategory = "restricted_topic"
)

type RedactionStrategy string

const (
	StrategyReplace    RedactionStrategy = "replace"
	StrategyGeneralize RedactionStrategy = "generalize"
	StrategyDelete     RedactionStrategy = "delete"
)

type ReviewStatus string

const (
	ReviewPending  ReviewStatus = "pending"
	ReviewApproved ReviewStatus = "approved"
	ReviewRejected ReviewStatus = "rejected"
)

type InterviewArchive struct {
	ID               string                      `json:"id"`
	SubjectCode      string                      `json:"subjectCode"`
	InterviewDate    string                      `json:"interviewDate"`
	Purpose          string                      `json:"purpose"`
	Curator          string                      `json:"curator"`
	Status           Status                      `json:"status"`
	Version          int64                       `json:"version"`
	CreatedAt        time.Time                   `json:"createdAt"`
	UpdatedAt        time.Time                   `json:"updatedAt"`
	Consent          *ConsentScope               `json:"consent,omitempty"`
	ConsentRevision  int                         `json:"consentRevision"`
	ConsentHistory   []ConsentRevision           `json:"consentHistory"`
	ConsentImpacts   []ConsentImpact             `json:"consentImpacts"`
	Segments         []TranscriptSegment         `json:"segments"`
	Marks            []SensitivityMark           `json:"marks"`
	Affected         map[string]bool             `json:"affected,omitempty"`
	FrozenVersion    int64                       `json:"frozenVersion,omitempty"`
	ApprovedBy       string                      `json:"approvedBy,omitempty"`
	ApprovedAt       *time.Time                  `json:"approvedAt,omitempty"`
	ManifestID       string                      `json:"manifestId,omitempty"`
	ProcessedDigests map[string]ProcessingDigest `json:"processedDigests,omitempty"`
}

type ConsentRevision struct {
	Revision  int           `json:"revision"`
	Effective bool          `json:"effective"`
	ChangedBy string        `json:"changedBy"`
	ChangedAt time.Time     `json:"changedAt"`
	Snapshot  ConsentScope  `json:"snapshot"`
	Changes   []FieldChange `json:"changes"`
}

type FieldChange struct {
	Field  string `json:"field"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

type ConsentImpact struct {
	SegmentID string `json:"segmentId"`
	MarkID    string `json:"markId,omitempty"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

type ConsentScope struct {
	ArchiveID        string         `json:"archiveId"`
	AllowedUses      []string       `json:"allowedUses"`
	RestrictedTopics []string       `json:"restrictedTopics"`
	NameDisclosure   NameDisclosure `json:"nameDisclosure"`
	SealedUntil      string         `json:"sealedUntil"`
	RecordedBy       string         `json:"recordedBy"`
	RecordedAt       time.Time      `json:"recordedAt"`
}

type TranscriptSegment struct {
	ArchiveID     string     `json:"archiveId"`
	SegmentID     string     `json:"segmentId"`
	Revision      int        `json:"revision"`
	SourceText    string     `json:"sourceText"`
	SanitizedText string     `json:"sanitizedText"`
	SpeakerCode   string     `json:"speakerCode"`
	SubmittedAt   *time.Time `json:"submittedAt,omitempty"`
	Locked        bool       `json:"locked"`
}

type SensitivityMark struct {
	ID           string            `json:"id"`
	SegmentID    string            `json:"segmentId"`
	StartOffset  int               `json:"startOffset"`
	EndOffset    int               `json:"endOffset"`
	Category     MarkCategory      `json:"category"`
	Strategy     RedactionStrategy `json:"strategy"`
	Replacement  string            `json:"replacement"`
	ReviewStatus ReviewStatus      `json:"reviewStatus"`
	ReviewReason string            `json:"reviewReason,omitempty"`
}

type ProcessingDigest struct {
	SegmentID string `json:"segmentId"`
	Revision  int    `json:"revision"`
	Digest    string `json:"digest"`
}

type PendingItem struct {
	Kind      string `json:"kind"`
	SegmentID string `json:"segmentId,omitempty"`
	MarkID    string `json:"markId,omitempty"`
	Message   string `json:"message"`
}

type BatchError struct {
	Index     int    `json:"index"`
	SegmentID string `json:"segmentId,omitempty"`
	MarkID    string `json:"markId,omitempty"`
	Field     string `json:"field"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

type SegmentMutation struct {
	SegmentID string `json:"segmentId"`
	Result    string `json:"result"`
	Revision  int    `json:"revision"`
}

type ReviewDecision struct {
	MarkID   string `json:"markId"`
	Approved bool   `json:"approved"`
	Reason   string `json:"reason,omitempty"`
}

type AuditEvent struct {
	Sequence       int64     `json:"sequence"`
	ArchiveID      string    `json:"archiveId"`
	At             time.Time `json:"at"`
	Actor          string    `json:"actor"`
	Action         string    `json:"action"`
	Detail         string    `json:"detail"`
	ArchiveVersion int64     `json:"archiveVersion"`
	ActionKey      string    `json:"actionKey,omitempty"`
	Result         []byte    `json:"result,omitempty"`
	PreviousHash   string    `json:"previousHash"`
	Hash           string    `json:"hash"`
}
