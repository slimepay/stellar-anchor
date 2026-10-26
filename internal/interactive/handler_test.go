package interactive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"aidmedium/stellar-anchor/internal/auth"
	"aidmedium/stellar-anchor/internal/customer"
	"aidmedium/stellar-anchor/internal/payout"
	"aidmedium/stellar-anchor/internal/sap"
	"aidmedium/stellar-anchor/internal/withdrawal"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	secret = "0123456789abcdef0123456789abcdef"
	asset  = "stellar:USDC:GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5"
)

var identity = []string{"first_name", "last_name", "birth_date", "id_type", "id_number"}

type platform struct{ tx *sap.Transaction }

func (p *platform) GetTransaction(_ context.Context, id string) (*sap.Transaction, error) {
	cp := *p.tx
	return &cp, nil
}

// customers stores what is put and gives it the configured KYC outcome.
type customers struct {
	saved  map[string]*customer.Customer
	status string
	puts   []customer.PutRequest
}

func (c *customers) Lookup(_ context.Context, account, memo string) (*customer.Customer, error) {
	if cu, ok := c.saved[account+":"+memo]; ok {
		cp := *cu
		return &cp, nil
	}
	return nil, customer.ErrNotFound
}

func (c *customers) Put(_ context.Context, req customer.PutRequest) (*customer.Response, error) {
	c.puts = append(c.puts, req)
	if req.Fields["id_number"] == "1" {
		return nil, &customer.ValidationError{}
	}
	key := req.Account + ":" + req.Memo
	cu, ok := c.saved[key]
	if !ok {
		cu = &customer.Customer{ID: "cust-1", Fields: map[string]string{}}
		c.saved[key] = cu
	}
	for k, v := range req.Fields {
		if v != "" {
			cu.Fields[k] = v
		}
	}
	cu.Status = c.status
	if c.status == customer.StatusAccepted || c.status == customer.StatusNeedsInfo {
		cu.Verified = map[string]string{}
		for _, k := range identity {
			cu.Verified[k] = cu.Fields[k]
		}
	}
	return &customer.Response{ID: cu.ID, Status: c.status}, nil
}

type withdrawals struct {
	started int
	account string
	amount  string
	err     error
}

func (w *withdrawals) Start(_ context.Context, tx *sap.Transaction, account, _ string, amount string) (*withdrawal.Withdrawal, error) {
	if w.err != nil {
		return nil, w.err
	}
	w.started++
	w.account, w.amount = account, amount
	return &withdrawal.Withdrawal{TransactionID: tx.ID, Asset: asset, AmountIn: "10.0000000", AmountOut: "15000.00", AccountName: "ADA OBI"}, nil
}

func (w *withdrawals) Rate(string) string { return "1500.00" }

type banks struct{}

func (banks) Banks(context.Context) ([]payout.Bank, error) {
	return []payout.Bank{{Code: "000013", Name: "GTBank"}}, nil
}

type fixture struct {
	h   *Handler
	p   *platform
	c   *customers
	w   *withdrawals
	tok string
}

func setup(t *testing.T) *fixture {
	f := &fixture{
		p: &platform{tx: &sap.Transaction{ID: "tx-1", Kind: sap.KindWithdrawal, Status: sap.StatusIncomplete,
			AmountExpected: &sap.Amount{Asset: asset}}},
		c: &customers{saved: map[string]*customer.Customer{}, status: customer.StatusAccepted},
		w: &withdrawals{},
	}
	f.h = NewHandler(secret, f.p, f.c, f.w, banks{})
	f.tok = token(t, "GUSER", "tx-1", time.Now().Add(10*time.Minute))
	return f
}

// verified stores a customer whose identity KYC already accepted.
func (f *fixture) verified() {
	fields := map[string]string{"first_name": "Ada", "last_name": "Obi", "birth_date": "1990-05-17", "id_type": "bvn", "id_number": "22222222222", "email_address": "ada@example.com"}
	ver := map[string]string{}
	for _, k := range identity {
		ver[k] = fields[k]
	}
	f.c.saved["GUSER:"] = &customer.Customer{ID: "cust-1", Status: customer.StatusNeedsInfo, Fields: fields, Verified: ver}
}

func token(t *testing.T, sub, jti string, exp time.Time) string {
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.InteractiveClaims{
		Data: map[string]any{"asset": asset, "amount": "25"},
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: sub, ID: jti, ExpiresAt: jwt.NewNumericDate(exp),
			Audience: jwt.ClaimStrings{auth.AudSep24Interactive},
		},
	}).SignedString([]byte(secret))
	require.NoError(t, err)
	return s
}

func (f *fixture) get(tok string, extra ...string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sep24/interactive?transaction_id=tx-1&token="+url.QueryEscape(tok)+strings.Join(extra, ""), nil))
	return w
}

func (f *fixture) post(form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/sep24/interactive", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, req)
	return w
}

func (f *fixture) identityForm() url.Values {
	return url.Values{
		"transaction_id": {"tx-1"}, "token": {f.tok}, "step": {"identity"},
		"first_name": {"Ada"}, "last_name": {"Obi"}, "email_address": {"ada@example.com"}, "birth_date": {"1990-05-17"},
		"id_type": {"bvn"}, "id_number": {"22222222222"},
	}
}

func (f *fixture) withdrawForm() url.Values {
	return url.Values{
		"transaction_id": {"tx-1"}, "token": {f.tok}, "amount": {"10"},
		"bank_number": {"000013"}, "bank_account_number": {"0123456789"},
	}
}

func TestNewCustomerSeesIdentityFirst(t *testing.T) {
	f := setup(t)
	w := f.get(f.tok)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Verify Your Identity")
	assert.Contains(t, body, "What you'll need")
	assert.Contains(t, body, `name="step" value="details"`)
	assert.NotContains(t, body, "bank_account_number", "bank details are not part of the identity step")
	assert.NotContains(t, body, `name="amount"`)
	assert.Contains(t, w.Header().Get("Content-Security-Policy"), "default-src 'none'")
	assert.Contains(t, w.Header().Get("Content-Security-Policy"), "script-src 'sha256-")

	details := f.get(f.tok, "&step=details").Body.String()
	assert.Contains(t, details, `name="step" value="identity"`)
	assert.Contains(t, details, `name="id_number"`)
	assert.NotContains(t, details, "bank_account_number")
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

func TestVerifiedCustomerSeesWithdrawForm(t *testing.T) {
	f := setup(t)
	f.verified()
	body := f.get(f.tok).Body.String()
	assert.Contains(t, body, "Withdraw USDC to naira")
	assert.Contains(t, body, "₦1,500.00", "current rate")
	assert.Contains(t, body, "Withdraw USD Coin")
	assert.Contains(t, body, "₦37,500.00", "you will receive")
	assert.Contains(t, body, `value="25"`, "amount from the token")
	assert.Contains(t, body, "GTBank")
	assert.Contains(t, body, "Verified as Ada Obi")
	assert.NotContains(t, body, "first_name", "identity is not asked again")
	assert.NotContains(t, body, "22222222222")
}

func TestIdentityPrefillsWithoutIDNumber(t *testing.T) {
	f := setup(t)
	f.c.saved["GUSER:"] = &customer.Customer{Status: customer.StatusRejected, Fields: map[string]string{"first_name": "Ada", "id_number": "22222222222"}}
	body := f.get(f.tok, "&step=details").Body.String()
	assert.Contains(t, body, `value="Ada"`)
	assert.NotContains(t, body, "22222222222")
	assert.Contains(t, body, "Leave the ID number blank")
}

func TestRejectsBadTokens(t *testing.T) {
	f := setup(t)
	for name, tok := range map[string]string{
		"expired":  token(t, "GUSER", "tx-1", time.Now().Add(-time.Minute)),
		"other tx": token(t, "GUSER", "tx-2", time.Now().Add(time.Minute)),
		"garbage":  "abc",
		"missing":  "",
	} {
		w := f.get(tok)
		assert.Equal(t, http.StatusUnauthorized, w.Code, name)
	}

	f.verified()
	form := f.withdrawForm()
	form.Set("token", token(t, "GUSER", "tx-1", time.Now().Add(-time.Minute)))
	assert.Equal(t, http.StatusUnauthorized, f.post(form).Code)
	assert.Zero(t, f.w.started)
}

func TestAlreadyStarted(t *testing.T) {
	f := setup(t)
	f.verified()
	f.p.tx.Status = sap.StatusPendingUserTransferStart
	w := f.get(f.tok)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "already in progress")

	f.post(f.withdrawForm())
	assert.Zero(t, f.w.started)
}

func TestIdentityThenWithdraw(t *testing.T) {
	f := setup(t)
	f.c.status = customer.StatusNeedsInfo
	w := f.post(f.identityForm())
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Withdraw USDC to naira", "verified identity moves on to the withdrawal")
	assert.Zero(t, f.w.started, "verifying identity does not start a withdrawal")
	require.Len(t, f.c.puts, 1)
	assert.Equal(t, "GUSER", f.c.puts[0].Account)
	assert.Equal(t, "22222222222", f.c.puts[0].Fields["id_number"])
	assert.NotContains(t, f.c.puts[0].Fields, "bank_number")

	f.c.status = customer.StatusAccepted
	w = f.post(f.withdrawForm())
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "NGN 15000.00")
	assert.Contains(t, w.Body.String(), "ADA OBI")
	assert.Equal(t, 1, f.w.started)
	assert.Equal(t, "GUSER", f.w.account, "account comes from the token, not the form")
	assert.Equal(t, "10", f.w.amount)
	require.Len(t, f.c.puts, 2)
	assert.Equal(t, map[string]string{"bank_number": "000013", "bank_account_number": "0123456789"}, f.c.puts[1].Fields)
}

func TestIdentityOutcomes(t *testing.T) {
	cases := map[string]struct {
		status string
		code   int
		text   string
	}{
		"rejected":   {customer.StatusRejected, http.StatusUnprocessableEntity, "could not verify your identity"},
		"processing": {customer.StatusProcessing, http.StatusOK, "still verifying"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			f.c.status = tc.status
			w := f.post(f.identityForm())
			assert.Equal(t, tc.code, w.Code)
			assert.Contains(t, w.Body.String(), tc.text)
			assert.Zero(t, f.w.started)
		})
	}
}

func TestWithdrawNeedsVerifiedIdentity(t *testing.T) {
	f := setup(t)
	w := f.post(f.withdrawForm())
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "Verify your identity before you withdraw")
	assert.Zero(t, f.w.started)
	assert.Empty(t, f.c.puts)
}

func TestWithdrawShowsErrors(t *testing.T) {
	f := setup(t)
	f.verified()
	f.w.err = &withdrawal.UserError{Msg: "the naira amount must be at least NGN 100"}
	w := f.post(f.withdrawForm())
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "at least NGN 100")
	assert.Contains(t, w.Body.String(), `value="0123456789"`, "the form keeps what was typed")
}

func TestOutputIsEscaped(t *testing.T) {
	f := setup(t)
	form := f.identityForm()
	form.Set("first_name", `<script>alert(1)</script>`)
	f.c.status = customer.StatusRejected
	body := f.post(form).Body.String()
	assert.NotContains(t, body, "<script>alert(1)</script>")
}

func TestNaira(t *testing.T) {
	for in, want := range map[string]string{"1500": "₦1,500.00", "1500.5": "₦1,500.50", "37500": "₦37,500.00", "1234567.891": "₦1,234,567.89", "12": "₦12.00", "0": "₦0.00"} {
		assert.Equal(t, want, naira(in), in)
	}
}
