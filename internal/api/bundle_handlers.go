package api

import (
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/cyberm-tech/vphone-web/internal/vm"
	"github.com/go-chi/chi/v5"
)

// exportVM handles GET /api/v1/vms/:id/export — streams a .zip bundle of a
// stopped VM (metadata + bootable image + firmware) for download / transfer.
func (s *Server) exportVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	v, err := s.vms.Get(id)
	if errors.Is(err, vm.ErrNotFound) {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load VM")
		return
	}
	if v.Status != vm.StatusStopped && v.Status != vm.StatusError {
		writeError(w, http.StatusConflict, "VM must be stopped to export (status "+string(v.Status)+")")
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+v.Name+`.vphonevm.zip"`)
	if err := s.vms.ExportTo(id, w); err != nil {
		// Headers/body may already be partially written; just log.
		s.log.Error("export vm", "id", id, "err", err)
	}
}

// importBundle handles POST /api/v1/vms/import-bundle — multipart upload of a
// .zip bundle. The upload is streamed to a temp file (zip needs random access),
// extracted into a new managed VM, and registered.
func (s *Server) importBundle(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing 'file' field")
		return
	}
	defer file.Close()

	tmp, err := os.CreateTemp("", "vphonevm-*.zip")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to buffer upload")
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		writeError(w, http.StatusInternalServerError, "failed to read upload")
		return
	}
	tmp.Close()

	v, err := s.vms.ImportBundle(r.FormValue("name"), tmp.Name())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, v)
}
