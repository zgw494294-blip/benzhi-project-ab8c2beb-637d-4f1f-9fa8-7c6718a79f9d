package manifestcontentdrift_test

import (
	"testing"
	"time"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/manifest"
)

func TestVerifyAgainstRejectsChangedPublishedContent(t *testing.T) {
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	approvedAt := now
	value := &archive.InterviewArchive{
		ID: "archive-1", SubjectCode: "P-1", InterviewDate: "2026-01-01", Purpose: "研究", Curator: "甲",
		Status: archive.StatusPublished, Version: 2, FrozenVersion: 1, ManifestID: "manifest-1", ApprovedBy: "发布人", ApprovedAt: &approvedAt,
		Consent:  &archive.ConsentScope{AllowedUses: []string{"研究"}, NameDisclosure: archive.DisclosureAllowed, RecordedBy: "甲", RecordedAt: now},
		Segments: []archive.TranscriptSegment{{ArchiveID: "archive-1", SegmentID: "S1", Revision: 1, SpeakerCode: "P-1", SourceText: "原文", SanitizedText: "已脱敏文本", Locked: true}},
	}
	events := []archive.AuditEvent{{Sequence: 1, ArchiveID: value.ID, ArchiveVersion: 1, At: now, Hash: "hash-1"}}
	release, err := manifest.Generate(value.ManifestID, value.ApprovedBy, now, value, events)
	if err != nil {
		t.Fatal(err)
	}
	value.Segments[0].SanitizedText = "被替换的发布内容"
	if err := manifest.VerifyAgainst(release, value); err == nil {
		t.Fatal("清单与当前发布内容摘要不一致时不应验证通过")
	}
}
