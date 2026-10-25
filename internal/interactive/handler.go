// Package interactive serves the SEP-24 withdrawal page that SAP sends wallets
// to. The user confirms their identity and bank account, chooses an amount,
// and the Business Server asks SAP for the on-chain payment.
package interactive

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"net/http"
	"strings"

	"aidmedium/stellar-anchor/internal/auth"
	"aidmedium/stellar-anchor/internal/customer"
	"aidmedium/stellar-anchor/internal/payout"
	"aidmedium/stellar-anchor/internal/sap"
	"aidmedium/stellar-anchor/internal/withdrawal"

	"github.com/rs/zerolog/log"
)

//go:embed templates/*.html
var templateFS embed.FS

var pages = template.Must(template.ParseFS(templateFS, "templates/*.html"))

type Platform interface {
	GetTransaction(ctx context.Context, id string) (*sap.Transaction, error)
}

type Customers interface {
	Lookup(ctx context.Context, account, memo string) (*customer.Customer, error)
	Put(ctx context.Context, req customer.PutRequest) (*customer.Response, error)
}

type Withdrawals interface {
	Start(ctx context.Context, tx *sap.Transaction, account, memo, amount string) (*withdrawal.Withdrawal, error)
	Rate(asset string) string
}

type Banks interface {
	Banks(ctx context.Context) ([]payout.Bank, error)
}

type Handler struct {
	secret      string
	platform    Platform
	customers   Customers
	withdrawals Withdrawals
	banks       Banks
}

func NewHandler(secret string, platform Platform, customers Customers, withdrawals Withdrawals, banks Banks) *Handler {
	return &Handler{secret: secret, platform: platform, customers: customers, withdrawals: withdrawals, banks: banks}
}

type formData struct {
	TransactionID string
	Token         string
	AssetCode     string
	Rate          string
	Amount        string
	Banks         []payout.Bank
	Values        map[string]string
	HasIDNumber   bool
	Error         string
}

type resultData struct {
	Title   string
	Message string
	W       *withdrawal.Withdrawal
	Asset   string
}

var formFields = []string{"first_name", "last_name", "email_address", "birth_date", "id_type", "id_number", "bank_number", "bank_account_number", "mobile_number"}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	switch r.Method {
	case http.MethodGet:
		h.show(w, r)
	case http.MethodPost:
		h.submit(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) session(w http.ResponseWriter, r *http.Request, txID, token string) (*auth.InteractiveClaims, *sap.Transaction, bool) {
	claims, err := auth.ParseInteractive(token, h.secret, txID)
	if err != nil {
		h.result(w, http.StatusUnauthorized, resultData{Title: "Session expired", Message: "Start the withdrawal again from your wallet."})
		return nil, nil, false
	}
	tx, err := h.platform.GetTransaction(r.Context(), txID)
	if err != nil {
		log.Error().Err(err).Str("tx", txID).Msg("interactive: failed to load transaction")
		h.result(w, http.StatusBadGateway, resultData{Title: "Something went wrong", Message: "Please try again in a moment."})
		return nil, nil, false
	}
	if tx.Kind != sap.KindWithdrawal {
		h.result(w, http.StatusBadRequest, resultData{Title: "Not supported", Message: "Only withdrawals are supported."})
		return nil, nil, false
	}
	if tx.Status != sap.StatusIncomplete {
		h.result(w, http.StatusOK, resultData{Title: "Withdrawal started", Message: "This withdrawal is already in progress. Return to your wallet to follow it."})
		return nil, nil, false
	}
	return claims, tx, true
}

func (h *Handler) show(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	claims, tx, ok := h.session(w, r, q.Get("transaction_id"), q.Get("token"))
	if !ok {
		return
	}

	values := map[string]string{}
	account, memo := claims.Account()
	if c, err := h.customers.Lookup(r.Context(), account, memo); err == nil {
		for _, f := range formFields {
			if f != "id_number" {
				values[f] = c.Fields[f]
			}
		}
	} else if !errors.Is(err, customer.ErrNotFound) {
		log.Error().Err(err).Msg("interactive: failed to load customer")
	}

	amount := ""
	if tx.AmountExpected != nil {
		amount = tx.AmountExpected.Amount
	}
	if v, ok := claims.Data["amount"].(string); ok && amount == "" {
		amount = v
	}
	h.form(w, r, tx, claims, q.Get("token"), amount, values, "")
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	token := r.PostForm.Get("token")
	claims, tx, ok := h.session(w, r, r.PostForm.Get("transaction_id"), token)
	if !ok {
		return
	}
	account, memo := claims.Account()

	values := map[string]string{}
	for _, f := range formFields {
		values[f] = strings.TrimSpace(r.PostForm.Get(f))
	}
	amount := strings.TrimSpace(r.PostForm.Get("amount"))

	resp, err := h.customers.Put(r.Context(), customer.PutRequest{Account: account, Memo: memo, Fields: values})
	var verr *customer.ValidationError
	switch {
	case errors.As(err, &verr):
		h.form(w, r, tx, claims, token, amount, values, verr.Error())
		return
	case err != nil:
		log.Error().Err(err).Msg("interactive: failed to save customer")
		h.form(w, r, tx, claims, token, amount, values, "We could not save your details. Please try again.")
		return
	}

	switch resp.Status {
	case customer.StatusAccepted:
	case customer.StatusRejected:
		h.form(w, r, tx, claims, token, amount, values, "We could not verify your identity. Check that your name and date of birth match your BVN or NIN record.")
		return
	case customer.StatusNeedsInfo:
		h.form(w, r, tx, claims, token, amount, values, "Fill in all the required fields.")
		return
	default:
		h.result(w, http.StatusOK, resultData{Title: "Verification in progress", Message: "We are still verifying your identity. Start the withdrawal again from your wallet once it is complete."})
		return
	}

	wd, err := h.withdrawals.Start(r.Context(), tx, account, memo, amount)
	var uerr *withdrawal.UserError
	switch {
	case errors.As(err, &uerr):
		h.form(w, r, tx, claims, token, amount, values, uerr.Msg)
		return
	case err != nil:
		log.Error().Err(err).Str("tx", tx.ID).Msg("interactive: failed to start withdrawal")
		h.form(w, r, tx, claims, token, amount, values, "We could not start the withdrawal. Please try again.")
		return
	}

	h.result(w, http.StatusOK, resultData{
		Title:   "Almost done",
		Message: "Return to your wallet and confirm the payment. Your wallet already has the destination and memo.",
		W:       wd,
		Asset:   assetCode(wd.Asset),
	})
}

func (h *Handler) form(w http.ResponseWriter, r *http.Request, tx *sap.Transaction, claims *auth.InteractiveClaims, token, amount string, values map[string]string, msg string) {
	banks, err := h.banks.Banks(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("interactive: failed to load banks")
	}
	hasID := false
	account, memo := claims.Account()
	if c, err := h.customers.Lookup(r.Context(), account, memo); err == nil {
		hasID = c.Fields["id_number"] != ""
	}
	asset := withdrawal.TransactionAsset(tx)
	data := formData{
		TransactionID: tx.ID,
		Token:         token,
		AssetCode:     assetCode(asset),
		Rate:          h.withdrawals.Rate(asset),
		Amount:        amount,
		Banks:         banks,
		Values:        values,
		HasIDNumber:   hasID,
		Error:         msg,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if msg != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	if err := pages.ExecuteTemplate(w, "form.html", data); err != nil {
		log.Error().Err(err).Msg("interactive: render form")
	}
}

func (h *Handler) result(w http.ResponseWriter, status int, data resultData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := pages.ExecuteTemplate(w, "result.html", data); err != nil {
		log.Error().Err(err).Msg("interactive: render result")
	}
}

func assetCode(asset string) string {
	if asset == withdrawal.AssetXLM {
		return "XLM"
	}
	parts := strings.Split(asset, ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return asset
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
}
