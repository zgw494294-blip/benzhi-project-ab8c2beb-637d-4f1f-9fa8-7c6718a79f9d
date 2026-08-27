package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
)

func (s *Server) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	if media := r.Header.Get("Content-Type"); media != "" && !strings.HasPrefix(media, "application/json") {
		s.writeStatusError(w, http.StatusUnsupportedMediaType, "content_type", "请求必须使用 application/json", "")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		s.writeStatusError(w, http.StatusBadRequest, "invalid_json", "JSON 请求无效："+err.Error(), "")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		s.writeStatusError(w, http.StatusBadRequest, "invalid_json", "请求只能包含一个 JSON 对象", "")
		return false
	}
	return true
}

func (s *Server) writeError(w http.ResponseWriter, err error) {
	var fieldErr *archive.FieldError
	var stateErr *archive.StateError
	var consentErr *archive.ConsentConflictError
	var batchErr *archive.BatchValidationError
	switch {
	case errors.As(err, &consentErr):
		s.writeDetailedError(w, http.StatusConflict, "consent_conflict", consentErr.Error(), "expectedConsentRevision", consentErr)
	case errors.As(err, &batchErr):
		s.writeDetailedError(w, http.StatusUnprocessableEntity, "batch_validation_failed", batchErr.Error(), batchErr.Kind, batchErr.Items)
	case errors.As(err, &fieldErr):
		s.writeStatusError(w, http.StatusUnprocessableEntity, "validation_failed", fieldErr.Message, fieldErr.Field)
	case errors.As(err, &stateErr):
		s.writeStatusError(w, http.StatusConflict, "invalid_state", stateErr.Error(), "status")
	case errors.Is(err, archive.ErrVersionConflict):
		s.writeStatusError(w, http.StatusConflict, "version_conflict", err.Error(), "expectedVersion")
	case errors.Is(err, archive.ErrNotFound):
		s.writeStatusError(w, http.StatusNotFound, "not_found", "请求的档案或制品不存在", "")
	default:
		s.writeStatusError(w, http.StatusInternalServerError, "internal_error", err.Error(), "")
	}
}

func (s *Server) writeDetailedError(w http.ResponseWriter, status int, code, message, field string, details any) {
	s.writeJSON(w, status, apiError{Error: errorBody{Code: code, Message: message, Field: field, Details: details}})
}

func (s *Server) writeStatusError(w http.ResponseWriter, status int, code, message, field string) {
	s.writeJSON(w, status, apiError{Error: errorBody{Code: code, Message: message, Field: field}})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func actor(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("X-Actor"))
}
