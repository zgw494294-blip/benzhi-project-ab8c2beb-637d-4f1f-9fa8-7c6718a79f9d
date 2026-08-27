package workflow

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/manifest"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/store"
)

type AuditOverview struct {
	IntegrityStatus string    `json:"integrityStatus"`
	EventCount      int       `json:"eventCount"`
	MatchedCount    int       `json:"matchedCount"`
	FirstEventAt    time.Time `json:"firstEventAt,omitempty"`
	LastEventAt     time.Time `json:"lastEventAt,omitempty"`
	LastEventHash   string    `json:"lastEventHash,omitempty"`
	BrokenSequence  int64     `json:"brokenSequence,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	ManifestStatus  string    `json:"manifestStatus"`
}

type AuditQuery struct {
	Actor          string
	Action         string
	ArchiveVersion int64
	From           time.Time
	To             time.Time
	Order          string
	Page           int
	PageSize       int
}

type AuditPage struct {
	Events   []archive.AuditEvent `json:"events"`
	Overview AuditOverview        `json:"overview"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"pageSize"`
	HasMore  bool                 `json:"hasMore"`
}

func (s *Service) AuditTimeline(id string, query AuditQuery) (AuditPage, error) {
	events, integrity, err := s.repository.AuditDiagnostics(id)
	if err != nil {
		return AuditPage{}, err
	}
	filtered := make([]archive.AuditEvent, 0, len(events))
	for _, event := range events {
		if query.Actor != "" && event.Actor != query.Actor {
			continue
		}
		if query.Action != "" && event.Action != query.Action {
			continue
		}
		if query.ArchiveVersion > 0 && event.ArchiveVersion != query.ArchiveVersion {
			continue
		}
		if !query.From.IsZero() && event.At.Before(query.From) {
			continue
		}
		if !query.To.IsZero() && event.At.After(query.To) {
			continue
		}
		filtered = append(filtered, event)
	}
	if strings.EqualFold(query.Order, "desc") {
		sort.Slice(filtered, func(i, j int) bool { return filtered[i].Sequence > filtered[j].Sequence })
	} else {
		sort.Slice(filtered, func(i, j int) bool { return filtered[i].Sequence < filtered[j].Sequence })
	}
	page, pageSize := query.Page, query.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	start := (page - 1) * pageSize
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + pageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	overview := auditOverview(integrity, len(filtered))
	value, loadErr := s.repository.Load(id)
	if loadErr != nil {
		return AuditPage{}, loadErr
	}
	if value.Status == archive.StatusPublished && value.ManifestID != "" {
		data, artifactErr := s.repository.LoadArtifact(id, value.ManifestID)
		if artifactErr != nil {
			overview.ManifestStatus = "manifest_unavailable"
		} else if release, parseErr := manifest.Parse(data); parseErr != nil {
			overview.ManifestStatus = "manifest_invalid"
		} else {
			overview.ManifestStatus = compareManifestAudit(release, integrity)
		}
	}
	if len(filtered) == 0 && integrity.Intact {
		overview.Reason = "筛选条件没有匹配的审计事件"
	}
	return AuditPage{Events: filtered[start:end], Overview: overview, Page: page, PageSize: pageSize, HasMore: end < len(filtered)}, nil
}

func auditOverview(integrity store.AuditIntegrity, matched int) AuditOverview {
	status := "intact"
	if !integrity.Intact {
		status = "broken"
	}
	return AuditOverview{IntegrityStatus: status, EventCount: integrity.EventCount, MatchedCount: matched, FirstEventAt: integrity.FirstEventAt, LastEventAt: integrity.LastEventAt, LastEventHash: integrity.LastEventHash, BrokenSequence: integrity.BrokenSequence, Reason: integrity.Reason, ManifestStatus: "not_applicable"}
}

func compareManifestAudit(release manifest.ReleaseManifest, integrity store.AuditIntegrity) string {
	if !integrity.Intact {
		return "log_broken"
	}
	if release.AuditSummary.EventCount != integrity.EventCount || release.AuditSummary.LastEventHash != integrity.LastEventHash || !release.AuditSummary.FirstEventAt.Equal(integrity.FirstEventAt) || !release.AuditSummary.LastEventAt.Equal(integrity.LastEventAt) {
		return "summary_mismatch"
	}
	return "match"
}

type ReviewTaskFilter struct {
	ArchiveStatus archive.Status
	Category      archive.MarkCategory
	Strategy      archive.RedactionStrategy
	ReviewStatus  archive.ReviewStatus
}

type ReviewTask struct {
	ArchiveID       string                    `json:"archiveId"`
	ArchiveVersion  int64                     `json:"archiveVersion"`
	ArchiveStatus   archive.Status            `json:"archiveStatus"`
	SegmentID       string                    `json:"segmentId"`
	SegmentRevision int                       `json:"segmentRevision"`
	MarkID          string                    `json:"markId"`
	Category        archive.MarkCategory      `json:"category"`
	Strategy        archive.RedactionStrategy `json:"strategy"`
	ReviewStatus    archive.ReviewStatus      `json:"reviewStatus"`
	StartOffset     int                       `json:"startOffset"`
	EndOffset       int                       `json:"endOffset"`
	Original        string                    `json:"original"`
	Redacted        string                    `json:"redacted"`
}

func (s *Service) ReviewTasks(filter ReviewTaskFilter) ([]ReviewTask, error) {
	archives, err := s.repository.List()
	if err != nil {
		return nil, err
	}
	tasks := []ReviewTask{}
	for _, value := range archives {
		if filter.ArchiveStatus != "" && value.Status != filter.ArchiveStatus {
			continue
		}
		preview, err := s.redactor.Generate(value)
		if err != nil {
			continue
		}
		spans := map[string]struct{ original, redacted string }{}
		for _, segment := range preview.Segments {
			for _, span := range segment.Spans {
				spans[span.MarkID] = struct{ original, redacted string }{span.Before, span.After}
			}
		}
		for _, mark := range value.Marks {
			if filter.Category != "" && mark.Category != filter.Category {
				continue
			}
			if filter.Strategy != "" && mark.Strategy != filter.Strategy {
				continue
			}
			status := filter.ReviewStatus
			if status == "" {
				status = archive.ReviewPending
			}
			if mark.ReviewStatus != status {
				continue
			}
			segment, ok := value.Segment(mark.SegmentID)
			if !ok {
				continue
			}
			diff := spans[mark.ID]
			tasks = append(tasks, ReviewTask{ArchiveID: value.ID, ArchiveVersion: value.Version, ArchiveStatus: value.Status, SegmentID: mark.SegmentID, SegmentRevision: segment.Revision, MarkID: mark.ID, Category: mark.Category, Strategy: mark.Strategy, ReviewStatus: mark.ReviewStatus, StartOffset: mark.StartOffset, EndOffset: mark.EndOffset, Original: diff.original, Redacted: diff.redacted})
		}
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].ArchiveID != tasks[j].ArchiveID {
			return tasks[i].ArchiveID < tasks[j].ArchiveID
		}
		if tasks[i].SegmentID != tasks[j].SegmentID {
			return tasks[i].SegmentID < tasks[j].SegmentID
		}
		if tasks[i].StartOffset != tasks[j].StartOffset {
			return tasks[i].StartOffset < tasks[j].StartOffset
		}
		return tasks[i].MarkID < tasks[j].MarkID
	})
	return tasks, nil
}

func ParseAuditTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("时间必须为 RFC3339 格式")
	}
	return parsed.UTC(), nil
}
