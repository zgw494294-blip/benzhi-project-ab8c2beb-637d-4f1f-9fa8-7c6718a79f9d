package commitsplitbrain_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/store"
)

func TestFailedCommitDoesNotAdvanceSnapshot(t *testing.T) {
	root := t.TempDir()
	repository, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	value, err := archive.NewInterview("split-brain", "P-1", "2026-01-01", "研究", "甲", now)
	if err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(root, "audit", "split-brain.jsonl")
	if err := os.WriteFile(auditPath, nil, 0o440); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Commit(store.CommitRequest{Archive: value, ExpectedVersion: 0, Actor: "甲", Action: "archive.created", At: now}); err == nil {
		t.Fatal("审计目标为目录时提交本应失败")
	}
	loaded, loadErr := repository.Load(value.ID)
	if loadErr == nil {
		t.Fatalf("提交已报错却留下可见快照: version=%d", loaded.Version)
	}
	if loadErr != archive.ErrNotFound {
		t.Fatalf("失败提交后应保持未创建状态，实际错误: %v", loadErr)
	}
}
