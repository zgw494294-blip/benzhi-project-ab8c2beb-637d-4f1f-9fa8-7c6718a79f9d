package concurrentidempotency_test

import (
	"sync"
	"testing"
	"time"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/store"
)

func TestConcurrentSameActionKeyReusesCommittedResult(t *testing.T) {
	repository, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	first, err := archive.NewInterview("same-action", "P-1", "2026-01-01", "研究", "甲", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := archive.NewInterview("same-action", "P-1", "2026-01-01", "研究", "甲", now)
	if err != nil {
		t.Fatal(err)
	}

	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	for _, value := range []*archive.InterviewArchive{first, second} {
		workers.Add(1)
		go func(value *archive.InterviewArchive) {
			defer workers.Done()
			ready <- struct{}{}
			<-start
			_, commitErr := repository.Commit(store.CommitRequest{Archive: value, ExpectedVersion: 0, Actor: "甲", Action: "archive.created", ActionKey: "request-1", At: now})
			errs <- commitErr
		}(value)
	}
	<-ready
	<-ready
	close(start)
	workers.Wait()
	close(errs)
	for commitErr := range errs {
		if commitErr != nil {
			t.Fatalf("同一 actionKey 的并发重放应复用已提交结果，却返回错误: %v", commitErr)
		}
	}
}
