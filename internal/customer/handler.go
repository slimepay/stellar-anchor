package customer

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi"
	"github.com/rs/zerolog/log"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Routes registers GET, PUT and DELETE /customer as SAP's callback API expects.
func (h *Handler) Routes(r chi.Router) {
	r.Get("/customer", h.get)
	r.Put("/customer", h.put)
	r.Delete("/customer/{id}", h.delete)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	resp, err := h.svc.Get(r.Context(), Query{ID: q.Get("id"), Account: q.Get("account"), Memo: q.Get("memo")})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	var raw map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must be JSON"})
		return
	}

	fields := map[string]string{}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			fields[k] = s
		}
	}

	resp, err := h.svc.Put(r.Context(), PutRequest{
		ID:      fields["id"],
		Account: fields["account"],
		Memo:    fields["memo"],
		Fields:  fields,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeErr(w http.ResponseWriter, err error) {
	var verr *ValidationError
	switch {
	case errors.As(err, &verr):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": verr.Error()})
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "customer not found"})
	default:
		log.Error().Err(err).Msg("customer callback failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
