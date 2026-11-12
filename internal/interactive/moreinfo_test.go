package interactive

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"aidmedium/stellar-anchor/internal/auth"
	"aidmedium/stellar-anchor/internal/sap"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	sep6Secret  = "sep6-more-info-secret-0123456789abcdef"
	sep24Secret = "sep24-more-info-secret-0123456789abcdef"
)

type txPlatform struct {
	tx  *sap.Transaction
	err error
}

func (p *txPlatform) GetTransaction(context.Context, string) (*sap.Transaction, error) {
	if p.err != nil {
		return nil, p.err
	}
	cp := *p.tx
	return &cp, nil
}

func moreInfoToken(t *testing.T, secret, aud, jti string, exp time.Time) string {
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		ID: jti, Subject: "GUSER", ExpiresAt: jwt.NewNumericDate(exp), Audience: jwt.ClaimStrings{aud},
	}).SignedString([]byte(secret))
	require.NoError(t, err)
	return s
}

func completedTx(sep string) *sap.Transaction {
	started := time.Date(2026, 10, 25, 21, 38, 57, 0, time.UTC)
	done := started.Add(16 * time.Minute)
	return &sap.Transaction{
		ID: "tx-1", Sep: sep, Kind: sap.KindWithdrawal, Status: sap.StatusCompleted,
		AmountIn:              &sap.Amount{Amount: "5", Asset: asset},
		AmountOut:             &sap.Amount{Amount: "7500", Asset: "iso4217:NGN"},
		FeeDetails:            &sap.FeeDetails{Total: "0", Asset: asset},
		StartedAt:             &started,
		CompletedAt:           &done,
		ExternalTransactionID: "anchor-tx-1",
		Message:               "naira sent to your bank account",
		StellarTransactions:   []sap.StellarTransaction{{ID: "d81c2536035a41115964ef5e5dc6d5320cc9306fb9b9850d27854a535007cb3d"}},
	}
}

func getMoreInfo(m *MoreInfo, id, tok string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/more-info?transaction_id="+id+"&token="+url.QueryEscape(tok), nil))
	return w
}

func newMoreInfo(tx *sap.Transaction) *MoreInfo {
	return NewMoreInfo(auth.MoreInfoSecrets{Sep6: sep6Secret, Sep24: sep24Secret}, &txPlatform{tx: tx}, "https://stellar.expert/explorer/testnet")
}

func TestMoreInfoShowsCompletedSep24Withdrawal(t *testing.T) {
	m := newMoreInfo(completedTx("24"))
	w := getMoreInfo(m, "tx-1", moreInfoToken(t, sep24Secret, auth.AudSep24MoreInfo, "tx-1", time.Now().Add(time.Hour)))

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Completed")
	assert.Contains(t, body, "5 USDC")
	assert.Contains(t, body, "₦7,500.00")
	assert.Contains(t, body, "anchor-tx-1")
	assert.Contains(t, body, "naira sent to your bank account")
	assert.Contains(t, body, "25 Oct 2026, 21:54 UTC")
	assert.Contains(t, body, `href="https://stellar.expert/explorer/testnet/tx/d81c2536035a41115964ef5e5dc6d5320cc9306fb9b9850d27854a535007cb3d"`)
	assert.NotContains(t, body, `http-equiv="refresh"`, "a finished transaction does not reload")
	assert.Equal(t, csp, w.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

func TestMoreInfoAcceptsSep6TokenForSep6Transaction(t *testing.T) {
	m := newMoreInfo(completedTx("6"))
	w := getMoreInfo(m, "tx-1", moreInfoToken(t, sep6Secret, auth.AudSep6MoreInfo, "tx-1", time.Now().Add(time.Hour)))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Completed")
}

func TestMoreInfoPendingReloads(t *testing.T) {
	tx := completedTx("24")
	tx.Status, tx.CompletedAt, tx.Message = sap.StatusPendingUserTransferStart, nil, "waiting for your payment"
	w := getMoreInfo(newMoreInfo(tx), "tx-1", moreInfoToken(t, sep24Secret, auth.AudSep24MoreInfo, "tx-1", time.Now().Add(time.Hour)))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Waiting for your payment")
	assert.Contains(t, w.Body.String(), `http-equiv="refresh"`)
}

func TestMoreInfoRefusesBadTokens(t *testing.T) {
	future := time.Now().Add(time.Hour)
	cases := map[string]string{
		"missing":             "",
		"garbage":             "not-a-jwt",
		"expired":             moreInfoToken(t, sep24Secret, auth.AudSep24MoreInfo, "tx-1", time.Now().Add(-time.Hour)),
		"other transaction":   moreInfoToken(t, sep24Secret, auth.AudSep24MoreInfo, "tx-2", future),
		"wrong secret":        moreInfoToken(t, "some-other-secret-0123456789abcdef", auth.AudSep24MoreInfo, "tx-1", future),
		"sep6 secret, sep24":  moreInfoToken(t, sep6Secret, auth.AudSep24MoreInfo, "tx-1", future),
		"sep24 secret, sep6":  moreInfoToken(t, sep24Secret, auth.AudSep6MoreInfo, "tx-1", future),
		"interactive token":   moreInfoToken(t, sep24Secret, auth.AudSep24Interactive, "tx-1", future),
		"sep6 token on sep24": moreInfoToken(t, sep6Secret, auth.AudSep6MoreInfo, "tx-1", future),
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			w := getMoreInfo(newMoreInfo(completedTx("24")), "tx-1", tok)
			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.NotContains(t, w.Body.String(), "anchor-tx-1")
		})
	}
}

func TestMoreInfoRefusesEverythingWithoutSecrets(t *testing.T) {
	m := NewMoreInfo(auth.MoreInfoSecrets{}, &txPlatform{tx: completedTx("24")}, "")
	w := getMoreInfo(m, "tx-1", moreInfoToken(t, sep24Secret, auth.AudSep24MoreInfo, "tx-1", time.Now().Add(time.Hour)))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestMoreInfoUnknownTransaction(t *testing.T) {
	m := NewMoreInfo(auth.MoreInfoSecrets{Sep24: sep24Secret}, &txPlatform{err: &sap.RPCError{Code: -32600, Message: "not found"}}, "")
	w := getMoreInfo(m, "tx-1", moreInfoToken(t, sep24Secret, auth.AudSep24MoreInfo, "tx-1", time.Now().Add(time.Hour)))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMoreInfoPlatformOutage(t *testing.T) {
	m := NewMoreInfo(auth.MoreInfoSecrets{Sep24: sep24Secret}, &txPlatform{err: errors.New("connection refused")}, "")
	w := getMoreInfo(m, "tx-1", moreInfoToken(t, sep24Secret, auth.AudSep24MoreInfo, "tx-1", time.Now().Add(time.Hour)))
	assert.Equal(t, http.StatusBadGateway, w.Code)
}

func TestMoreInfoEscapesMessage(t *testing.T) {
	tx := completedTx("24")
	tx.Message = `<script>alert(1)</script>`
	w := getMoreInfo(newMoreInfo(tx), "tx-1", moreInfoToken(t, sep24Secret, auth.AudSep24MoreInfo, "tx-1", time.Now().Add(time.Hour)))
	assert.NotContains(t, w.Body.String(), "<script>alert(1)</script>")
	assert.Contains(t, w.Body.String(), "&lt;script&gt;")
}

func TestMoreInfoOnlyAllowsGet(t *testing.T) {
	w := httptest.NewRecorder()
	newMoreInfo(completedTx("24")).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/more-info", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
