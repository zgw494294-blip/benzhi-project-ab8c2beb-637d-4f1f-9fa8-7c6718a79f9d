package web

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/http"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/manifest"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/workflow"
)

//go:embed static/*
var assets embed.FS

type Server struct {
	workflow *workflow.Service
	mux      *http.ServeMux
}

type apiError struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
	Details any    `json:"details,omitempty"`
}

func New(service *workflow.Service) *Server {
	s := &Server{workflow: service, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'")
		s.mux.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	staticFS, _ := fs.Sub(assets, "static")
	s.mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(staticFS))))
	s.mux.HandleFunc("GET /", s.WorkbenchHandler)
	s.mux.HandleFunc("GET /healthz", s.HealthHandler)
	s.mux.HandleFunc("GET /api/archives", s.ListArchivesHandler)
	s.mux.HandleFunc("POST /api/archives", s.CreateArchiveHandler)
	s.mux.HandleFunc("GET /api/archives/{id}", s.GetArchiveHandler)
	s.mux.HandleFunc("PUT /api/archives/{id}/consent", s.SetConsentHandler)
	s.mux.HandleFunc("PUT /api/archives/{id}/segments/{segmentId}", s.UpsertSegmentHandler)
	s.mux.HandleFunc("PUT /api/archives/{id}/segments/batch", s.UpsertSegmentsHandler)
	s.mux.HandleFunc("POST /api/archives/{id}/marks", s.AddMarkHandler)
	s.mux.HandleFunc("POST /api/archives/{id}/marks/preflight", s.PreflightMarksHandler)
	s.mux.HandleFunc("POST /api/archives/{id}/marks/batch", s.CommitMarksHandler)
	s.mux.HandleFunc("DELETE /api/archives/{id}/marks/{markId}", s.RemoveMarkHandler)
	s.mux.HandleFunc("POST /api/archives/{id}/submit", s.SubmitHandler)
	s.mux.HandleFunc("POST /api/archives/{id}/redaction", s.GenerateRedactionHandler)
	s.mux.HandleFunc("POST /api/archives/{id}/reviews", s.ReviewHandler)
	s.mux.HandleFunc("POST /api/archives/{id}/reviews/batch", s.ReviewBatchHandler)
	s.mux.HandleFunc("GET /api/review-tasks", s.ReviewTasksHandler)
	s.mux.HandleFunc("GET /api/archives/{id}/audit", s.AuditHandler)
	s.mux.HandleFunc("GET /api/archives/{id}/redaction/report", s.RedactionReportHandler)
	s.mux.HandleFunc("POST /api/archives/{id}/freeze", s.FreezeHandler)
	s.mux.HandleFunc("POST /api/archives/{id}/approve", s.ApproveHandler)
	s.mux.HandleFunc("GET /api/archives/{id}/manifest", s.ManifestHandler)
	s.mux.HandleFunc("GET /api/archives/{id}/manifest/download", s.DownloadManifestHandler)
	s.mux.HandleFunc("POST /api/manifests/verify", s.VerifyManifestHandler)
}

func (s *Server) UpsertSegmentsHandler(w http.ResponseWriter, r *http.Request) {
	var input workflow.SegmentBatchInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.workflow.UpsertSegments(r.PathValue("id"), input, actor(r))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, value)
}

func (s *Server) PreflightMarksHandler(w http.ResponseWriter, r *http.Request) {
	var input workflow.MarkPreflightInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.workflow.PreflightMarks(r.PathValue("id"), input)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, value)
}

func (s *Server) CommitMarksHandler(w http.ResponseWriter, r *http.Request) {
	var input workflow.MarkBatchInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.workflow.CommitMarks(r.PathValue("id"), input, actor(r))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, value)
}

func (s *Server) ReviewBatchHandler(w http.ResponseWriter, r *http.Request) {
	var input workflow.ReviewBatchInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.workflow.ReviewBatch(r.PathValue("id"), input, actor(r))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, value)
}

func (s *Server) ReviewTasksHandler(w http.ResponseWriter, r *http.Request) {
	filter := workflow.ReviewTaskFilter{ArchiveStatus: archive.Status(r.URL.Query().Get("archiveStatus")), Category: archive.MarkCategory(r.URL.Query().Get("category")), Strategy: archive.RedactionStrategy(r.URL.Query().Get("strategy")), ReviewStatus: archive.ReviewStatus(r.URL.Query().Get("reviewStatus"))}
	values, err := s.workflow.ReviewTasks(filter)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"tasks": values, "count": len(values)})
}

func (s *Server) AuditHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	from, err := workflow.ParseAuditTime(query.Get("from"))
	if err != nil {
		s.writeError(w, &archive.FieldError{Field: "from", Message: err.Error()})
		return
	}
	to, err := workflow.ParseAuditTime(query.Get("to"))
	if err != nil {
		s.writeError(w, &archive.FieldError{Field: "to", Message: err.Error()})
		return
	}
	version, err := queryInt64(query.Get("archiveVersion"), "archiveVersion")
	if err != nil {
		s.writeError(w, err)
		return
	}
	page, err := queryInt(query.Get("page"), "page")
	if err != nil {
		s.writeError(w, err)
		return
	}
	pageSize, err := queryInt(query.Get("pageSize"), "pageSize")
	if err != nil {
		s.writeError(w, err)
		return
	}
	value, err := s.workflow.AuditTimeline(r.PathValue("id"), workflow.AuditQuery{Actor: query.Get("actor"), Action: query.Get("action"), ArchiveVersion: version, From: from, To: to, Order: query.Get("order"), Page: page, PageSize: pageSize})
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, value)
}

func (s *Server) RedactionReportHandler(w http.ResponseWriter, r *http.Request) {
	view, err := s.workflow.Get(r.PathValue("id"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, view.Preview.Report)
}

func (s *Server) WorkbenchHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := assets.ReadFile("static/index.html")
	if err != nil {
		s.writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) HealthHandler(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ListArchivesHandler(w http.ResponseWriter, r *http.Request) {
	values, err := s.workflow.List()
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"archives": values})
}

func (s *Server) CreateArchiveHandler(w http.ResponseWriter, r *http.Request) {
	var input workflow.CreateArchiveInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.workflow.Create(input, actor(r))
	if err != nil {
		s.writeError(w, err)
		return
	}
	w.Header().Set("Location", "/api/archives/"+value.ID)
	s.writeJSON(w, http.StatusCreated, value)
}

func (s *Server) GetArchiveHandler(w http.ResponseWriter, r *http.Request) {
	view, err := s.workflow.Get(r.PathValue("id"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, view)
}

func (s *Server) SetConsentHandler(w http.ResponseWriter, r *http.Request) {
	var input workflow.ConsentInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.workflow.SetConsent(r.PathValue("id"), input, actor(r))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, value)
}

func (s *Server) UpsertSegmentHandler(w http.ResponseWriter, r *http.Request) {
	var input workflow.SegmentInput
	if !s.decode(w, r, &input) {
		return
	}
	input.SegmentID = r.PathValue("segmentId")
	value, err := s.workflow.UpsertSegment(r.PathValue("id"), input, actor(r))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, value)
}

func (s *Server) AddMarkHandler(w http.ResponseWriter, r *http.Request) {
	var input workflow.MarkInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.workflow.AddMark(r.PathValue("id"), input, actor(r))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, value)
}

func (s *Server) RemoveMarkHandler(w http.ResponseWriter, r *http.Request) {
	var input workflow.VersionInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.workflow.RemoveMark(r.PathValue("id"), r.PathValue("markId"), input, actor(r))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, value)
}

func (s *Server) SubmitHandler(w http.ResponseWriter, r *http.Request) {
	var input workflow.VersionInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.workflow.SubmitForRedaction(r.PathValue("id"), input, actor(r))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, value)
}

func (s *Server) GenerateRedactionHandler(w http.ResponseWriter, r *http.Request) {
	var input workflow.VersionInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.workflow.GenerateRedaction(r.PathValue("id"), input, actor(r))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, value)
}

func (s *Server) ReviewHandler(w http.ResponseWriter, r *http.Request) {
	var input workflow.ReviewInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.workflow.Review(r.PathValue("id"), input, actor(r))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, value)
}

func (s *Server) FreezeHandler(w http.ResponseWriter, r *http.Request) {
	var input workflow.VersionInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.workflow.Freeze(r.PathValue("id"), input, actor(r))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, value)
}

func (s *Server) ApproveHandler(w http.ResponseWriter, r *http.Request) {
	var input workflow.ApprovalInput
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.workflow.Approve(r.PathValue("id"), input, actor(r))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, value)
}

func (s *Server) ManifestHandler(w http.ResponseWriter, r *http.Request) {
	value, err := s.workflow.Manifest(r.PathValue("id"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"valid": true, "manifest": value})
}

func (s *Server) DownloadManifestHandler(w http.ResponseWriter, r *http.Request) {
	value, err := s.workflow.Manifest(r.PathValue("id"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	data, err := manifest.Marshal(value)
	if err != nil {
		s.writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-release-manifest.json"`, value.ArchiveID))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) VerifyManifestHandler(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		s.writeError(w, &archive.FieldError{Field: "manifest", Message: "清单超过 2 MiB 限制"})
		return
	}
	value, err := s.workflow.VerifyManifest(data)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"valid": true, "manifest": value})
}
