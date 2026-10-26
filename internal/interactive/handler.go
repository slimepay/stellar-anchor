// Package interactive serves the SEP-24 withdrawal page that SAP sends wallets
// to. A customer whose identity is not verified yet sees the identity step
// first. A verified customer goes straight to the withdrawal form, chooses an
// amount and bank account, and the Business Server asks SAP for the on-chain
// payment.
package interactive

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"net/http"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
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

var pages = template.Must(template.New("").Funcs(template.FuncMap{"naira": naira}).ParseFS(templateFS, "templates/*.html"))

// receiveScript keeps "You Will Receive" in step with the amount. The CSP allows
// only this script, by hash.
const receiveScript = `(function(){var a=document.getElementById("amount"),o=document.getElementById("receive");if(!a||!o)return;var r=parseFloat(o.dataset.rate)||0;function u(){var v=parseFloat(a.value);o.value="₦"+(v>0?v*r:0).toLocaleString("en-NG",{minimumFractionDigits:2,maximumFractionDigits:2})}a.addEventListener("input",u);u()})();`

var csp = func() string {
	sum := sha256.Sum256([]byte(receiveScript))
	return "default-src 'none'; style-src 'unsafe-inline'; script-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'; form-action 'self'; base-uri 'none'"
}()

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

const stepIdentity = "identity"

type pageData struct {
	TransactionID string
	Token         string
	AssetCode     string
	Rate          string
	Amount        string
	Banks         []payout.Bank
	Values        map[string]string
	HasIDNumber   bool
	Name          string
	Error         string
	Details       bool
	AssetName     string
	Receive       string
	Script        template.JS
}

type resultData struct {
	Title   string
	Message string
	W       *withdrawal.Withdrawal
	Asset   string
}

var (
	identityForm = []string{"first_name", "last_name", "email_address", "birth_date", "id_type", "id_number", "mobile_number"}
	bankForm     = []string{"bank_number", "bank_account_number"}
)

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

type session struct {
	claims  *auth.InteractiveClaims
	tx      *sap.Transaction
	token   string
	account string
	memo    string
}

func (h *Handler) session(w http.ResponseWriter, r *http.Request, txID, token string) (*session, bool) {
	claims, err := auth.ParseInteractive(token, h.secret, txID)
	if err != nil {
		h.result(w, http.StatusUnauthorized, resultData{Title: "Session expired", Message: "Start the withdrawal again from your wallet."})
		return nil, false
	}
	tx, err := h.platform.GetTransaction(r.Context(), txID)
	if err != nil {
		log.Error().Err(err).Str("tx", txID).Msg("interactive: failed to load transaction")
		h.result(w, http.StatusBadGateway, resultData{Title: "Something went wrong", Message: "Please try again in a moment."})
		return nil, false
	}
	if tx.Kind != sap.KindWithdrawal {
		h.result(w, http.StatusBadRequest, resultData{Title: "Not supported", Message: "Only withdrawals are supported."})
		return nil, false
	}
	if tx.Status != sap.StatusIncomplete {
		h.result(w, http.StatusOK, resultData{Title: "Withdrawal started", Message: "This withdrawal is already in progress. Return to your wallet to follow it."})
		return nil, false
	}
	account, memo := claims.Account()
	return &session{claims: claims, tx: tx, token: token, account: account, memo: memo}, true
}

// amount is what the wallet asked for, used to pre-fill the withdrawal form.
func (s *session) amount() string {
	if s.tx.AmountExpected != nil && s.tx.AmountExpected.Amount != "" {
		return s.tx.AmountExpected.Amount
	}
	v, _ := s.claims.Data["amount"].(string)
	return v
}

func (h *Handler) lookup(r *http.Request, s *session) *customer.Customer {
	c, err := h.customers.Lookup(r.Context(), s.account, s.memo)
	if err != nil {
		if !errors.Is(err, customer.ErrNotFound) {
			log.Error().Err(err).Msg("interactive: failed to load customer")
		}
		return nil
	}
	return c
}

func (h *Handler) show(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s, ok := h.session(w, r, q.Get("transaction_id"), q.Get("token"))
	if !ok {
		return
	}
	c := h.lookup(r, s)
	if !c.IdentityVerified() {
		h.identity(w, s, c, saved(c, identityForm), "", q.Get("step") == "details")
		return
	}
	h.withdraw(w, r, s, c, s.amount(), saved(c, bankForm), "")
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s, ok := h.session(w, r, r.PostForm.Get("transaction_id"), r.PostForm.Get("token"))
	if !ok {
		return
	}
	if r.PostForm.Get("step") == stepIdentity {
		h.submitIdentity(w, r, s)
		return
	}
	h.submitWithdraw(w, r, s)
}

func (h *Handler) submitIdentity(w http.ResponseWriter, r *http.Request, s *session) {
	values := posted(r, identityForm)
	resp, err := h.customers.Put(r.Context(), customer.PutRequest{Account: s.account, Memo: s.memo, Fields: values})
	var verr *customer.ValidationError
	switch {
	case errors.As(err, &verr):
		h.identity(w, s, h.lookup(r, s), values, verr.Error(), true)
		return
	case err != nil:
		log.Error().Err(err).Msg("interactive: failed to save customer")
		h.identity(w, s, h.lookup(r, s), values, "We could not save your details. Please try again.", true)
		return
	}

	c := h.lookup(r, s)
	switch {
	case resp.Status == customer.StatusRejected:
		h.identity(w, s, c, values, "We could not verify your identity. Check that your name and date of birth match your BVN or NIN record.", false)
	case resp.Status == customer.StatusProcessing:
		h.result(w, http.StatusOK, resultData{Title: "Verification in progress", Message: "We are still verifying your identity. Start the withdrawal again from your wallet once it is complete."})
	case !c.IdentityVerified():
		h.identity(w, s, c, values, "Fill in all the required fields.", true)
	default:
		h.withdraw(w, r, s, c, s.amount(), saved(c, bankForm), "")
	}
}

func (h *Handler) submitWithdraw(w http.ResponseWriter, r *http.Request, s *session) {
	c := h.lookup(r, s)
	if !c.IdentityVerified() {
		h.identity(w, s, c, saved(c, identityForm), "Verify your identity before you withdraw.", true)
		return
	}
	values := posted(r, bankForm)
	amount := strings.TrimSpace(r.PostForm.Get("amount"))

	resp, err := h.customers.Put(r.Context(), customer.PutRequest{Account: s.account, Memo: s.memo, Fields: values})
	var verr *customer.ValidationError
	switch {
	case errors.As(err, &verr):
		h.withdraw(w, r, s, c, amount, values, verr.Error())
		return
	case err != nil:
		log.Error().Err(err).Msg("interactive: failed to save customer")
		h.withdraw(w, r, s, c, amount, values, "We could not save your details. Please try again.")
		return
	case resp.Status != customer.StatusAccepted:
		h.withdraw(w, r, s, c, amount, values, "Choose your bank and enter your account number.")
		return
	}

	wd, err := h.withdrawals.Start(r.Context(), s.tx, s.account, s.memo, amount)
	var uerr *withdrawal.UserError
	switch {
	case errors.As(err, &uerr):
		h.withdraw(w, r, s, c, amount, values, uerr.Msg)
		return
	case err != nil:
		log.Error().Err(err).Str("tx", s.tx.ID).Msg("interactive: failed to start withdrawal")
		h.withdraw(w, r, s, c, amount, values, "We could not start the withdrawal. Please try again.")
		return
	}

	h.result(w, http.StatusOK, resultData{
		Title:   "Almost done",
		Message: "Return to your wallet and confirm the payment. Your wallet already has the destination and memo.",
		W:       wd,
		Asset:   assetCode(wd.Asset),
	})
}

func (h *Handler) identity(w http.ResponseWriter, s *session, c *customer.Customer, values map[string]string, msg string, details bool) {
	h.render(w, "identity.html", msg, pageData{
		TransactionID: s.tx.ID,
		Token:         s.token,
		AssetCode:     assetCode(withdrawal.TransactionAsset(s.tx)),
		Values:        values,
		HasIDNumber:   c != nil && c.Fields["id_number"] != "",
		Error:         msg,
		Details:       details,
	})
}

func (h *Handler) withdraw(w http.ResponseWriter, r *http.Request, s *session, c *customer.Customer, amount string, values map[string]string, msg string) {
	banks, err := h.banks.Banks(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("interactive: failed to load banks")
	}
	asset := withdrawal.TransactionAsset(s.tx)
	h.render(w, "form.html", msg, pageData{
		TransactionID: s.tx.ID,
		Token:         s.token,
		AssetCode:     assetCode(asset),
		AssetName:     assetName(asset),
		Rate:          h.withdrawals.Rate(asset),
		Amount:        amount,
		Receive:       naira(receive(amount, h.withdrawals.Rate(asset))),
		Script:        template.JS(receiveScript),
		Banks:         banks,
		Values:        values,
		Name:          strings.TrimSpace(c.Fields["first_name"] + " " + c.Fields["last_name"]),
		Error:         msg,
	})
}

func (h *Handler) render(w http.ResponseWriter, page, msg string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if msg != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	if err := pages.ExecuteTemplate(w, page, data); err != nil {
		log.Error().Err(err).Str("page", page).Msg("interactive: render")
	}
}

func (h *Handler) result(w http.ResponseWriter, status int, data resultData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := pages.ExecuteTemplate(w, "result.html", data); err != nil {
		log.Error().Err(err).Msg("interactive: render result")
	}
}

// saved pre-fills a form from the stored customer. The ID number is never
// sent back to the browser.
func saved(c *customer.Customer, names []string) map[string]string {
	values := map[string]string{}
	if c == nil {
		return values
	}
	for _, f := range names {
		if f != "id_number" {
			values[f] = c.Fields[f]
		}
	}
	return values
}

func posted(r *http.Request, names []string) map[string]string {
	values := map[string]string{}
	for _, f := range names {
		values[f] = strings.TrimSpace(r.PostForm.Get(f))
	}
	return values
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
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
}

func assetName(asset string) string {
	switch assetCode(asset) {
	case "USDC":
		return "USD Coin"
	case "XLM":
		return "Stellar Lumens"
	}
	return assetCode(asset)
}

// receive is the naira the amount buys at rate, for the first render of the form.
func receive(amount, rate string) string {
	a, err1 := strconv.ParseFloat(amount, 64)
	r, err2 := strconv.ParseFloat(rate, 64)
	if err1 != nil || err2 != nil || a <= 0 {
		return "0"
	}
	return strconv.FormatFloat(a*r, 'f', 2, 64)
}

// naira formats a decimal string as ₦1,234.50.
func naira(v string) string {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return "₦" + v
	}
	s := strconv.FormatFloat(f, 'f', 2, 64)
	whole, frac := s[:len(s)-3], s[len(s)-3:]
	var b strings.Builder
	for i, ch := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	return "₦" + b.String() + frac
}
