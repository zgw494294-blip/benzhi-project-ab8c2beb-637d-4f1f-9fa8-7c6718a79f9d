package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/redaction"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/store"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/workflow"
)

func TestWorkbenchAndStructuredError(t *testing.T) {
	repository, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := New(workflow.New(repository, redaction.New(), nil)).Handler()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "<body>") || !strings.Contains(response.Body.String(), "口述史授权净化发布台") {
		t.Fatalf("工作台响应不完整: %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/archives", strings.NewReader(`{"subjectCode":"P"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Actor", "甲")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `"code":"validation_failed"`) {
		t.Fatalf("错误结构不一致: %d %s", response.Code, response.Body.String())
	}
}
