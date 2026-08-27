package store

import (
	"path/filepath"
	"regexp"
	"sync"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func validateID(id string) error {
	if !safeID.MatchString(id) {
		return &archive.FieldError{Field: "id", Message: "编号只能包含字母、数字、点、下划线和连字符"}
	}
	return nil
}

func (s *JSONStore) snapshotPath(id string) string {
	return filepath.Join(s.archives, id+".json")
}

func (s *JSONStore) auditPath(id string) string {
	return filepath.Join(s.audits, id+".jsonl")
}

func (s *JSONStore) archiveLock(id string) *sync.Mutex {
	s.lockGuard.Lock()
	defer s.lockGuard.Unlock()
	if s.locks[id] == nil {
		s.locks[id] = &sync.Mutex{}
	}
	return s.locks[id]
}
