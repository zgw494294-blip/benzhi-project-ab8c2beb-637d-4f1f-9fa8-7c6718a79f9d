package manifest

import (
	"testing"
	"time"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
)

func TestGenerateAndDetectTamper(t *testing.T) {
	now := time.Now().UTC()
	a, _ := archive.NewInterview("arc", "P", "2025-01-01", "研究", "甲", now)
	a.Version, a.FrozenVersion, a.Status = 4, 4, archive.StatusPendingApproval
	a.Consent = &archive.ConsentScope{AllowedUses: []string{"研究"}, NameDisclosure: archive.DisclosurePseudonym}
	a.Segments = []archive.TranscriptSegment{{SegmentID: "S1", Revision: 1, SpeakerCode: "P", SanitizedText: "公开文本", Locked: true}}
	a.Marks = []archive.SensitivityMark{{ID: "M", ReviewStatus: archive.ReviewApproved}}
	events := []archive.AuditEvent{{Sequence: 1, At: now, Hash: "abc"}}
	m, err := Generate("manifest-1", "负责人", now, a, events)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(m); err != nil {
		t.Fatal(err)
	}
	m.ContentSummary.Segments[0].SanitizedText = "被篡改"
	if err := Verify(m); err == nil {
		t.Fatal("篡改内容应导致验证失败")
	}
}
