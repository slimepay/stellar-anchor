package interactive

import (
	"net/http"
	"time"

	"aidmedium/stellar-anchor/internal/auth"
	"aidmedium/stellar-anchor/internal/sap"

	"github.com/rs/zerolog/log"
)

// MoreInfo serves the read-only status page SAP links to as more_info_url on
// SEP-6 and SEP-24 transactions.
type MoreInfo struct {
	secrets  auth.MoreInfoSecrets
	platform Platform
	explorer string
}

// NewMoreInfo takes the stellar.expert base for the network, such as
// https://stellar.expert/explorer/testnet.
func NewMoreInfo(secrets auth.MoreInfoSecrets, platform Platform, explorer string) *MoreInfo {
	return &MoreInfo{secrets: secrets, platform: platform, explorer: explorer}
}

type statusData struct {
	Title     string
	Message   string
	Tx        *sap.Transaction
	Status    string
	Final     bool
	Failed    bool
	Asset     string
	StellarTx string
	Explorer  string
}

var statusText = map[string]string{
	sap.StatusIncomplete:               "Waiting for your details",
	sap.StatusPendingUserTransferStart: "Waiting for your payment",
	sap.StatusPendingAnchor:            "Payment received, sending naira",
	sap.StatusPendingExternal:          "Naira on its way to your bank",
	sap.StatusOnHold:                   "On hold for review",
	sap.StatusCompleted:                "Completed",
	sap.StatusError:                    "Failed",
	"pending_trust":                    "Waiting for a trustline",
	"pending_user":                     "Waiting for you",
	"pending_stellar":                  "Waiting for Stellar",
	"refunded":                         "Refunded",
	"expired":                          "Expired",
	"no_market":                        "Unavailable",
	"too_small":                        "Amount too small",
	"too_large":                        "Amount too large",
}

func (m *MoreInfo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	id := q.Get("transaction_id")
	sep, err := auth.ParseMoreInfo(q.Get("token"), id, m.secrets)
	if err != nil {
		m.render(w, http.StatusForbidden, statusData{Title: "Link expired", Message: "Open the transaction again from your wallet."})
		return
	}
	tx, err := m.platform.GetTransaction(r.Context(), id)
	if err != nil {
		if sap.IsRPCError(err) {
			m.render(w, http.StatusNotFound, statusData{Title: "Transaction not found", Message: "Open the transaction again from your wallet."})
			return
		}
		log.Error().Err(err).Str("tx", id).Msg("more info: failed to load transaction")
		m.render(w, http.StatusBadGateway, statusData{Title: "Something went wrong", Message: "Please try again in a moment."})
		return
	}
	if tx.Sep != sep {
		m.render(w, http.StatusForbidden, statusData{Title: "Link expired", Message: "Open the transaction again from your wallet."})
		return
	}

	d := statusData{Title: "Withdrawal status", Tx: tx, Status: statusText[tx.Status], Message: tx.Message, Explorer: m.explorer}
	if d.Status == "" {
		d.Status = tx.Status
	}
	switch tx.Status {
	case sap.StatusCompleted, "refunded":
		d.Final = true
	case sap.StatusError, "expired", "no_market", "too_small", "too_large":
		d.Final, d.Failed = true, true
	}
	if tx.AmountIn != nil {
		d.Asset = assetCode(tx.AmountIn.Asset)
	}
	if n := len(tx.StellarTransactions); n > 0 {
		d.StellarTx = tx.StellarTransactions[n-1].ID
	}
	m.render(w, http.StatusOK, d)
}

func (m *MoreInfo) render(w http.ResponseWriter, status int, d statusData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := pages.ExecuteTemplate(w, "status.html", d); err != nil {
		log.Error().Err(err).Msg("more info: render")
	}
}

func short(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:6] + "…" + hash[len(hash)-6:]
}

func stamp(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2 Jan 2006, 15:04 UTC")
}
