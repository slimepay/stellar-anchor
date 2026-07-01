package sep12

import (
	"encoding/json"
	"net/http"
	"time"

	"aidmedium/stellar-anchor/internal/middleware"
)

type CustomerStatus string

const (
	StatusNeedsInfo  CustomerStatus = "NEEDS_INFO"
	StatusProcessing CustomerStatus = "PROCESSING"
	StatusAccepted   CustomerStatus = "ACCEPTED"
	StatusRejected   CustomerStatus = "REJECTED"
)

type Customer struct {
	ID             string         `bson:"_id"         json:"id"`
	StellarAccount string         `bson:"stellar_account" json:"-"`
	Status         CustomerStatus `bson:"status"      json:"status"`
	FirstName      string         `bson:"first_name"  json:"first_name,omitempty"`
	LastName       string         `bson:"last_name"   json:"last_name,omitempty"`
	Email          string         `bson:"email"       json:"email,omitempty"`
	IDType         string         `bson:"id_type"     json:"id_type,omitempty"`
	IDNumber       string         `bson:"id_number"   json:"id_number,omitempty"`
	CreatedAt      time.Time      `bson:"created_at"  json:"created_at"`
	UpdatedAt      time.Time      `bson:"updated_at"  json:"updated_at"`
}

type Handler struct {
	store Store
}

func NewHandler(store Store) *Handler {
	return &Handler{store: store}
}

// GetCustomer handles GET /sep12/customer
func (h *Handler) GetCustomer(w http.ResponseWriter, r *http.Request) {
	account := middleware.StellarAccountFromContext(r.Context())
	if account == "" {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}

	customer, err := h.store.FindByAccount(r.Context(), account)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	if customer == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":     "",
			"status": StatusNeedsInfo,
			"fields": map[string]any{
				"first_name": map[string]string{"type": "string", "description": "First name"},
				"last_name":  map[string]string{"type": "string", "description": "Last name"},
				"email":      map[string]string{"type": "string", "description": "Email address"},
				"id_type":    map[string]string{"type": "string", "description": "Government ID type (passport, nin, drivers_license)"},
				"id_number":  map[string]string{"type": "string", "description": "Government ID number"},
			},
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(customer)
}

// PutCustomer handles PUT /sep12/customer — upsert KYC fields
func (h *Handler) PutCustomer(w http.ResponseWriter, r *http.Request) {
	account := middleware.StellarAccountFromContext(r.Context())
	if account == "" {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var body UpsertFields
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.FirstName == "" || body.LastName == "" || body.Email == "" {
		writeErr(w, http.StatusBadRequest, "first_name, last_name, and email are required")
		return
	}

	id, err := h.store.Upsert(r.Context(), account, body)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to save customer record")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"id":     id,
		"status": string(StatusProcessing),
	})
}

// DeleteCustomer handles DELETE /sep12/customer — right to erasure (GDPR)
func (h *Handler) DeleteCustomer(w http.ResponseWriter, r *http.Request) {
	account := middleware.StellarAccountFromContext(r.Context())
	if account == "" {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}

	if err := h.store.Delete(r.Context(), account); err != nil {
		writeErr(w, http.StatusInternalServerError, "delete failed")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
