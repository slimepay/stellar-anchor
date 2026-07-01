package sep31

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"aidmedium/stellar-anchor/internal/config"
	"aidmedium/stellar-anchor/internal/middleware"

	"github.com/go-chi/chi"
	"github.com/google/uuid"
)

type TxStatus string

const (
	StatusPendingReceiver     TxStatus = "pending_receiver"
	StatusPendingCustomerInfo TxStatus = "pending_customer_info_update"
	StatusPendingExternal     TxStatus = "pending_external"
	StatusCompleted           TxStatus = "completed"
	StatusError               TxStatus = "error"
)

type Transaction struct {
	ID                    string     `bson:"_id"            json:"id"`
	Status                TxStatus   `bson:"status"         json:"status"`
	StellarAccountID      string     `bson:"stellar_account" json:"-"`
	AssetCode             string     `bson:"asset_code"     json:"asset_code"`
	AssetIssuer           string     `bson:"asset_issuer,omitempty" json:"asset_issuer,omitempty"`
	AmountIn              string     `bson:"amount_in"      json:"amount_in"`
	AmountInAsset         string     `bson:"amount_in_asset" json:"amount_in_asset"`
	AmountOut             string     `bson:"amount_out,omitempty" json:"amount_out,omitempty"`
	AmountFee             string     `bson:"amount_fee,omitempty" json:"amount_fee,omitempty"`
	StellarTxHash         string     `bson:"stellar_tx_hash,omitempty" json:"stellar_transaction_id,omitempty"`
	StartedAt             time.Time  `bson:"started_at"     json:"started_at"`
	CompletedAt           *time.Time `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
	ReceiverAccountNumber string     `bson:"receiver_account_number,omitempty" json:"-"`
	ReceiverBankCode      string     `bson:"receiver_bank_code,omitempty"      json:"-"`
	ReceiverName          string     `bson:"receiver_name,omitempty"           json:"-"`
}

type Handler struct {
	store Store
	cfg   *config.Config
}

func NewHandler(store Store, cfg *config.Config) *Handler {
	return &Handler{store: store, cfg: cfg}
}

func (h *Handler) Info(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"receive": map[string]any{
			"USDC": map[string]any{
				"enabled": true, "min_amount": 1, "max_amount": 50000,
				"fee_fixed": 1.0, "fee_percent": 0.5,
				"sep12": map[string]any{
					"sender":   map[string]any{"types": map[string]any{"sep12-sender": map[string]string{"description": "Sending institution KYC"}}},
					"receiver": map[string]any{"types": map[string]any{"sep12-receiver": map[string]string{"description": "Receiver KYC (NGN payout)"}}},
				},
				"fields": map[string]any{
					"transaction": map[string]any{
						"receiver_account_number": map[string]string{"description": "Receiver Nigerian bank account number"},
						"receiver_bank_code":      map[string]string{"description": "Receiver bank CBN code (e.g. 058 for GTBank)"},
						"receiver_name":           map[string]string{"description": "Full name of account holder"},
					},
				},
			},
		},
	})
}

func (h *Handler) CreateTransaction(w http.ResponseWriter, r *http.Request) {
	account := middleware.StellarAccountFromContext(r.Context())
	if account == "" {
		writeErr(w, http.StatusUnauthorized, "SEP-10 authentication required")
		return
	}

	var body struct {
		Amount                string `json:"amount"`
		AssetCode             string `json:"asset_code"`
		AssetIssuer           string `json:"asset_issuer"`
		ReceiverAccountNumber string `json:"receiver_account_number"`
		ReceiverBankCode      string `json:"receiver_bank_code"`
		ReceiverName          string `json:"receiver_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.AssetCode != "USDC" {
		writeErr(w, http.StatusBadRequest, "only USDC is supported for SEP-31 cross-border payments")
		return
	}
	if body.ReceiverAccountNumber == "" || body.ReceiverBankCode == "" || body.ReceiverName == "" {
		writeErr(w, http.StatusBadRequest, "receiver_account_number, receiver_bank_code, and receiver_name are required")
		return
	}

	tx := &Transaction{
		ID:                    uuid.New().String(),
		Status:                StatusPendingReceiver,
		StellarAccountID:      account,
		AssetCode:             body.AssetCode,
		AssetIssuer:           body.AssetIssuer,
		AmountIn:              body.Amount,
		AmountInAsset:         fmt.Sprintf("stellar:USDC:%s", h.cfg.USDCIssuer),
		ReceiverAccountNumber: body.ReceiverAccountNumber,
		ReceiverBankCode:      body.ReceiverBankCode,
		ReceiverName:          body.ReceiverName,
		StartedAt:             time.Now().UTC(),
	}
	if err := h.store.Insert(r.Context(), tx); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create transaction")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"id":                 tx.ID,
		"stellar_account_id": h.cfg.DistributionKey,
		"stellar_memo_type":  "hash",
		"stellar_memo":       tx.ID[:32],
	})
}

func (h *Handler) GetTransaction(w http.ResponseWriter, r *http.Request) {
	txID := chi.URLParam(r, "id")
	if txID == "" {
		writeErr(w, http.StatusBadRequest, "transaction id required")
		return
	}

	tx, err := h.store.FindByID(r.Context(), txID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	if tx == nil {
		writeErr(w, http.StatusNotFound, "transaction not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"transaction": tx})
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
