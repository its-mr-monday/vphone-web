package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cyberm-tech/vphone-web/internal/ipsw"
	"github.com/go-chi/chi/v5"
)

// listIPSWs handles GET /api/v1/ipsws.
func (s *Server) listIPSWs(w http.ResponseWriter, r *http.Request) {
	items, err := s.ipsw.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list IPSWs")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// registerIPSWRequest registers an existing file on disk.
type registerIPSWRequest struct {
	FilePath string `json:"file_path"`
	Version  string `json:"version"`
	Build    string `json:"build"`
	Device   string `json:"device"`
	Kind     string `json:"kind"` // iphone (default) | cloudos
}

// registerIPSW handles POST /api/v1/ipsws (register a file already on disk).
func (s *Server) registerIPSW(w http.ResponseWriter, r *http.Request) {
	var req registerIPSWRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	it, err := s.ipsw.Register(ipsw.RegisterParams{
		FilePath: req.FilePath, Version: req.Version, Build: req.Build,
		Device: req.Device, Kind: req.Kind,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, it)
}

// downloadIPSWRequest triggers a URL download.
type downloadIPSWRequest struct {
	URL  string `json:"url"`
	Kind string `json:"kind"` // iphone (default) | cloudos
}

// downloadIPSW handles POST /api/v1/ipsws/download.
func (s *Server) downloadIPSW(w http.ResponseWriter, r *http.Request) {
	var req downloadIPSWRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	it, handle, err := s.ipsw.Download(req.URL, req.Kind)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ipsw": it, "job_id": handle.ID})
}

// uploadIPSW handles POST /api/v1/ipsws/upload (multipart file upload).
func (s *Server) uploadIPSW(w http.ResponseWriter, r *http.Request) {
	// Cap in-memory portion; large files stream to disk.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing 'file' field")
		return
	}
	defer file.Close()

	it, err := s.ipsw.SaveUpload(header.Filename, r.FormValue("kind"), file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, it)
}

// deleteIPSW handles DELETE /api/v1/ipsws/:id.
func (s *Server) deleteIPSW(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	err := s.ipsw.Delete(id)
	if errors.Is(err, ipsw.ErrNotFound) {
		writeError(w, http.StatusNotFound, "IPSW not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
