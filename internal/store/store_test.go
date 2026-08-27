package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
)

func TestAtomicCommitConflictAndRecovery(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := archive.NewInterview("arc-1", "P", "2025-01-01", "研究", "甲", time.Now())
	event, err := s.Commit(CommitRequest{Archive: value, ExpectedVersion: 0, Actor: "甲", Action: "created", Detail: "建档", At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if event.Sequence != 1 || value.Version != 1 {
		t.Fatalf("提交版本错误: %#v", event)
	}
	stale := cloneArchive(value)
	stale.Version = 0
	if _, err := s.Commit(CommitRequest{Archive: stale, ExpectedVersion: 0, Actor: "乙", Action: "stale", At: time.Now()}); !errors.Is(err, archive.ErrVersionConflict) {
		t.Fatalf("应返回版本冲突: %v", err)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Load("arc-1")
	if err != nil || loaded.Version != 1 {
		t.Fatalf("恢复失败: %#v %v", loaded, err)
	}
}

func TestAuditCorruptionReportsLine(t *testing.T) {
	root := t.TempDir()
	s, _ := Open(root)
	value, _ := archive.NewInterview("arc-bad", "P", "2025-01-01", "研究", "甲", time.Now())
	_, _ = s.Commit(CommitRequest{Archive: value, ExpectedVersion: 0, Actor: "甲", Action: "created", At: time.Now()})
	path := filepath.Join(root, "audit", "arc-bad.jsonl")
	data, _ := os.ReadFile(path)
	data = []byte(strings.Replace(string(data), `"actor":"甲"`, `"actor":"篡改"`, 1))
	if err := os.WriteFile(path, data, 0o640); err != nil {
		t.Fatal(err)
	}
	_, err := Open(root)
	if err == nil || !strings.Contains(err.Error(), "第 1 行") {
		t.Fatalf("应定位损坏行: %v", err)
	}
}
