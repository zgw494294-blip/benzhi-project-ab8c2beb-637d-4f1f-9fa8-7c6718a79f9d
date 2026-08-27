package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
)

const FormatVersion = "oral-history-release-manifest-v1"

type ReleaseManifest struct {
	FormatVersion  string         `json:"formatVersion"`
	ID             string         `json:"id"`
	ArchiveID      string         `json:"archiveId"`
	FrozenVersion  int64          `json:"frozenVersion"`
	ContentDigest  string         `json:"contentDigest"`
	ConsentDigest  string         `json:"consentDigest"`
	AuditDigest    string         `json:"auditDigest"`
	ManifestDigest string         `json:"manifestDigest"`
	ApprovedBy     string         `json:"approvedBy"`
	ApprovedAt     time.Time      `json:"approvedAt"`
	ContentSummary ContentSummary `json:"contentSummary"`
	ConsentSummary ConsentSummary `json:"consentSummary"`
	AuditSummary   AuditSummary   `json:"auditSummary"`
}

type ContentSummary struct {
	SubjectCode   string          `json:"subjectCode"`
	InterviewDate string          `json:"interviewDate"`
	Purpose       string          `json:"purpose"`
	Segments      []FrozenSegment `json:"segments"`
}

type FrozenSegment struct {
	SegmentID     string `json:"segmentId"`
	Revision      int    `json:"revision"`
	SpeakerCode   string `json:"speakerCode"`
	SanitizedText string `json:"sanitizedText"`
}

type ConsentSummary struct {
	AllowedUses      []string `json:"allowedUses"`
	RestrictedTopics []string `json:"restrictedTopics"`
	NameDisclosure   string   `json:"nameDisclosure"`
	SealedUntil      string   `json:"sealedUntil"`
}

type AuditSummary struct {
	EventCount    int       `json:"eventCount"`
	FirstEventAt  time.Time `json:"firstEventAt"`
	LastEventAt   time.Time `json:"lastEventAt"`
	LastEventHash string    `json:"lastEventHash"`
}

func Generate(id, approver string, at time.Time, a *archive.InterviewArchive, events []archive.AuditEvent) (ReleaseManifest, error) {
	if a.Status != archive.StatusPendingApproval && a.Status != archive.StatusPublished {
		return ReleaseManifest{}, errors.New("只有待批准或刚完成发布的冻结档案才能签发清单")
	}
	validFrozenVersion := a.FrozenVersion == a.Version
	if a.Status == archive.StatusPublished {
		validFrozenVersion = a.FrozenVersion+1 == a.Version
	}
	if a.FrozenVersion <= 0 || !validFrozenVersion {
		return ReleaseManifest{}, fmt.Errorf("冻结版本 %d 与当前版本 %d 不匹配", a.FrozenVersion, a.Version)
	}
	if strings.TrimSpace(id) == "" || strings.TrimSpace(approver) == "" {
		return ReleaseManifest{}, errors.New("清单编号和批准人不能为空")
	}
	if a.Consent == nil {
		return ReleaseManifest{}, errors.New("授权摘要缺失")
	}
	if a.Consent.SealedUntil != "" {
		sealedUntil, err := time.Parse("2006-01-02", a.Consent.SealedUntil)
		if err != nil {
			return ReleaseManifest{}, errors.New("封存截止日期格式无效")
		}
		approvalDate := time.Date(at.UTC().Year(), at.UTC().Month(), at.UTC().Day(), 0, 0, 0, 0, time.UTC)
		if approvalDate.Before(sealedUntil) {
			return ReleaseManifest{}, fmt.Errorf("档案封存至 %s，当前不能批准发布", a.Consent.SealedUntil)
		}
	}
	for _, segment := range a.Segments {
		if !segment.Locked || segment.SanitizedText == "" {
			return ReleaseManifest{}, fmt.Errorf("段落 %s 尚未冻结或缺少脱敏文本", segment.SegmentID)
		}
	}
	for _, mark := range a.Marks {
		if mark.ReviewStatus != archive.ReviewApproved {
			return ReleaseManifest{}, errors.New("仍有复核项未通过")
		}
	}
	content := ContentSummary{SubjectCode: a.SubjectCode, InterviewDate: a.InterviewDate, Purpose: a.Purpose, Segments: []FrozenSegment{}}
	for _, s := range a.Segments {
		content.Segments = append(content.Segments, FrozenSegment{SegmentID: s.SegmentID, Revision: s.Revision, SpeakerCode: s.SpeakerCode, SanitizedText: s.SanitizedText})
	}
	sort.Slice(content.Segments, func(i, j int) bool { return content.Segments[i].SegmentID < content.Segments[j].SegmentID })
	consent := ConsentSummary{AllowedUses: sorted(a.Consent.AllowedUses), RestrictedTopics: sorted(a.Consent.RestrictedTopics), NameDisclosure: string(a.Consent.NameDisclosure), SealedUntil: a.Consent.SealedUntil}
	audit := summarizeAudit(events)
	m := ReleaseManifest{FormatVersion: FormatVersion, ID: id, ArchiveID: a.ID, FrozenVersion: a.FrozenVersion, ApprovedBy: strings.TrimSpace(approver), ApprovedAt: at.UTC(), ContentSummary: content, ConsentSummary: consent, AuditSummary: audit}
	m.ContentDigest = digest(content)
	m.ConsentDigest = digest(consent)
	m.AuditDigest = digest(audit)
	m.ManifestDigest = digest(signable(m))
	return m, nil
}

func Verify(m ReleaseManifest) error {
	if m.FormatVersion != FormatVersion {
		return fmt.Errorf("不支持的清单格式 %q", m.FormatVersion)
	}
	if m.ID == "" || m.ArchiveID == "" || m.FrozenVersion <= 0 || m.ApprovedBy == "" || m.ApprovedAt.IsZero() {
		return errors.New("清单必填字段不完整")
	}
	if len(m.ContentSummary.Segments) == 0 {
		return errors.New("清单不包含任何冻结段落")
	}
	seenSegments := map[string]bool{}
	for _, segment := range m.ContentSummary.Segments {
		if segment.SegmentID == "" || segment.Revision <= 0 || segment.SpeakerCode == "" || segment.SanitizedText == "" {
			return errors.New("清单包含字段不完整的冻结段落")
		}
		if seenSegments[segment.SegmentID] {
			return fmt.Errorf("清单包含重复段落 %s", segment.SegmentID)
		}
		seenSegments[segment.SegmentID] = true
	}
	if len(m.ConsentSummary.AllowedUses) == 0 || m.ConsentSummary.NameDisclosure == "" {
		return errors.New("清单授权摘要不完整")
	}
	if !strictlySorted(m.ConsentSummary.AllowedUses) {
		return errors.New("允许用途未按规范顺序排列或存在重复")
	}
	if !strictlySorted(m.ConsentSummary.RestrictedTopics) {
		return errors.New("禁用主题未按规范顺序排列或存在重复")
	}
	for index := 1; index < len(m.ContentSummary.Segments); index++ {
		if m.ContentSummary.Segments[index-1].SegmentID >= m.ContentSummary.Segments[index].SegmentID {
			return errors.New("冻结段落未按稳定编号规范排列")
		}
	}
	if m.AuditSummary.EventCount <= 0 || m.AuditSummary.FirstEventAt.IsZero() || m.AuditSummary.LastEventAt.IsZero() || m.AuditSummary.LastEventHash == "" {
		return errors.New("清单审计摘要不完整")
	}
	if m.AuditSummary.LastEventAt.Before(m.AuditSummary.FirstEventAt) {
		return errors.New("清单审计时间范围倒置")
	}
	for name, value := range map[string]string{"contentDigest": m.ContentDigest, "consentDigest": m.ConsentDigest, "auditDigest": m.AuditDigest, "manifestDigest": m.ManifestDigest} {
		if !validDigest(value) {
			return fmt.Errorf("%s 不是有效 SHA-256 十六进制摘要", name)
		}
	}
	if got := digest(m.ContentSummary); got != m.ContentDigest {
		return fmt.Errorf("内容摘要不匹配：计算值 %s", got)
	}
	if got := digest(m.ConsentSummary); got != m.ConsentDigest {
		return fmt.Errorf("授权摘要不匹配：计算值 %s", got)
	}
	if got := digest(m.AuditSummary); got != m.AuditDigest {
		return fmt.Errorf("审计摘要不匹配：计算值 %s", got)
	}
	if got := digest(signable(m)); got != m.ManifestDigest {
		return fmt.Errorf("清单总摘要不匹配：计算值 %s", got)
	}
	return nil
}

func VerifyAgainst(m ReleaseManifest, a *archive.InterviewArchive) error {
	if err := Verify(m); err != nil {
		return err
	}
	if a.ID != m.ArchiveID || a.FrozenVersion != m.FrozenVersion {
		return errors.New("清单归属或冻结版本与档案不匹配")
	}
	if a.Status != archive.StatusPublished || a.ManifestID != m.ID {
		return errors.New("档案尚未以此清单发布")
	}
	return nil
}

func Marshal(m ReleaseManifest) ([]byte, error) {
	if err := Verify(m); err != nil {
		return nil, err
	}
	return json.MarshalIndent(m, "", "  ")
}

func Parse(data []byte) (ReleaseManifest, error) {
	var m ReleaseManifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("解析发布清单: %w", err)
	}
	if err := Verify(m); err != nil {
		return m, err
	}
	return m, nil
}

type signableManifest struct {
	FormatVersion string    `json:"formatVersion"`
	ID            string    `json:"id"`
	ArchiveID     string    `json:"archiveId"`
	FrozenVersion int64     `json:"frozenVersion"`
	ContentDigest string    `json:"contentDigest"`
	ConsentDigest string    `json:"consentDigest"`
	AuditDigest   string    `json:"auditDigest"`
	ApprovedBy    string    `json:"approvedBy"`
	ApprovedAt    time.Time `json:"approvedAt"`
}

func signable(m ReleaseManifest) signableManifest {
	return signableManifest{m.FormatVersion, m.ID, m.ArchiveID, m.FrozenVersion, m.ContentDigest, m.ConsentDigest, m.AuditDigest, m.ApprovedBy, m.ApprovedAt}
}

func summarizeAudit(events []archive.AuditEvent) AuditSummary {
	if len(events) == 0 {
		return AuditSummary{}
	}
	ordered := append([]archive.AuditEvent(nil), events...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Sequence < ordered[j].Sequence })
	return AuditSummary{EventCount: len(ordered), FirstEventAt: ordered[0].At.UTC(), LastEventAt: ordered[len(ordered)-1].At.UTC(), LastEventHash: ordered[len(ordered)-1].Hash}
}

func sorted(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
func digest(v any) string {
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func strictlySorted(values []string) bool {
	for index, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
		if index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}
