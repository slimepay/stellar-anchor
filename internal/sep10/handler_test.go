package sep10_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aidmedium/stellar-anchor/internal/config"
	"aidmedium/stellar-anchor/internal/sep10"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stellar/go-stellar-sdk/clients/horizonclient"
	stellarkeypair "github.com/stellar/go-stellar-sdk/keypair"
	hProtocol "github.com/stellar/go-stellar-sdk/protocols/horizon"
	"github.com/stellar/go-stellar-sdk/support/render/problem"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testConfig returns a minimal config with freshly generated anchor keypair.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	anchorKP := stellarkeypair.MustRandom()
	return &config.Config{
		Env:               "testnet",
		NetworkPassphrase: "Test SDF Network ; September 2015",
		AnchorSigningSeed: anchorKP.Seed(),
		AnchorSigningKey:  anchorKP.Address(),
		WebAuthDomain:     "anchor.example.com",
		HomeDomain:        "example.com",
		JWTSecret:         "test-secret-32-bytes-long-enough!",
		JWTExpiryHours:    24,
	}
}

// fakeHorizon returns a fixed account, or a not found error when account is nil.
type fakeHorizon struct {
	account *hProtocol.Account
	err     error
}

func (f fakeHorizon) AccountDetail(horizonclient.AccountRequest) (hProtocol.Account, error) {
	if f.err != nil {
		return hProtocol.Account{}, f.err
	}
	if f.account == nil {
		return hProtocol.Account{}, &horizonclient.Error{Problem: problem.P{
			Type:   "https://stellar.org/horizon-errors/not_found",
			Status: http.StatusNotFound,
		}}
	}
	return *f.account, nil
}

var accountNotFound = fakeHorizon{}

func existingAccount(id string, medThreshold byte, signers map[string]int32) fakeHorizon {
	acc := &hProtocol.Account{AccountID: id}
	acc.Thresholds.MedThreshold = medThreshold
	for key, weight := range signers {
		acc.Signers = append(acc.Signers, hProtocol.Signer{Key: key, Weight: weight, Type: "ed25519_public_key"})
	}
	return fakeHorizon{account: acc}
}

func fetchChallenge(t *testing.T, cfg *config.Config, account string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/sep10/auth?account="+account, nil)
	w := httptest.NewRecorder()
	sep10.GetChallenge(cfg)(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Transaction string `json:"transaction"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Transaction
}

func signChallenge(t *testing.T, cfg *config.Config, challenge string, signers ...*stellarkeypair.Full) string {
	t.Helper()
	parsed, err := txnbuild.TransactionFromXDR(challenge)
	require.NoError(t, err)
	tx, ok := parsed.Transaction()
	require.True(t, ok)
	if len(signers) > 0 {
		tx, err = tx.Sign(cfg.NetworkPassphrase, signers...)
		require.NoError(t, err)
	}
	signed, err := tx.Base64()
	require.NoError(t, err)
	return signed
}

func postChallenge(t *testing.T, handler http.HandlerFunc, signedXDR string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"transaction": signedXDR})
	req := httptest.NewRequest(http.MethodPost, "/sep10/auth", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

// buildChallenge builds a challenge by hand so tests can control each field.
func buildChallenge(t *testing.T, cfg *config.Config, client, homeDomain, webAuthDomain string, minTime, maxTime int64) string {
	t.Helper()
	anchorKP, err := stellarkeypair.ParseFull(cfg.AnchorSigningSeed)
	require.NoError(t, err)

	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount: &txnbuild.SimpleAccount{AccountID: anchorKP.Address(), Sequence: 0},
		Operations: []txnbuild.Operation{
			&txnbuild.ManageData{
				Name:          homeDomain + " auth",
				Value:         []byte("YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXphYmNkZWZnaGlqa2xtbm9wcXJzdHV2"),
				SourceAccount: client,
			},
			&txnbuild.ManageData{
				Name:          "web_auth_domain",
				Value:         []byte(webAuthDomain),
				SourceAccount: anchorKP.Address(),
			},
		},
		BaseFee:       txnbuild.MinBaseFee,
		Preconditions: txnbuild.Preconditions{TimeBounds: txnbuild.NewTimebounds(minTime, maxTime)},
	})
	require.NoError(t, err)
	tx, err = tx.Sign(cfg.NetworkPassphrase, anchorKP)
	require.NoError(t, err)
	out, err := tx.Base64()
	require.NoError(t, err)
	return out
}

func TestGetChallenge_MissingAccount(t *testing.T) {
	cfg := testConfig(t)
	handler := sep10.GetChallenge(cfg)

	req := httptest.NewRequest(http.MethodGet, "/sep10/auth", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "account")
}

func TestGetChallenge_InvalidAddress(t *testing.T) {
	cfg := testConfig(t)
	handler := sep10.GetChallenge(cfg)

	req := httptest.NewRequest(http.MethodGet, "/sep10/auth?account=notastellaraddress", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "invalid account")
}

func TestGetChallenge_ValidAddress(t *testing.T) {
	cfg := testConfig(t)
	handler := sep10.GetChallenge(cfg)

	clientKP := stellarkeypair.MustRandom()
	req := httptest.NewRequest(http.MethodGet, "/sep10/auth?account="+clientKP.Address(), nil)
	w := httptest.NewRecorder()
	handler(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp struct {
		Transaction       string `json:"transaction"`
		NetworkPassphrase string `json:"network_passphrase"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.Transaction, "transaction XDR must be present")
	assert.Equal(t, cfg.NetworkPassphrase, resp.NetworkPassphrase)
}

func TestGetChallenge_TransactionStructure(t *testing.T) {
	cfg := testConfig(t)
	clientKP := stellarkeypair.MustRandom()

	parsed, err := txnbuild.TransactionFromXDR(fetchChallenge(t, cfg, clientKP.Address()))
	require.NoError(t, err)
	tx, ok := parsed.Transaction()
	require.True(t, ok, "expected a regular transaction")

	assert.Equal(t, cfg.AnchorSigningKey, tx.SourceAccount().AccountID, "tx source must be anchor")
	assert.Equal(t, int64(0), tx.SourceAccount().Sequence, "tx sequence must be 0")

	tb := tx.Timebounds()
	assert.NotEqual(t, int64(txnbuild.TimeoutInfinite), tb.MaxTime)
	assert.LessOrEqual(t, tb.MaxTime-tb.MinTime, int64(15*60))

	ops := tx.Operations()
	require.Len(t, ops, 2, "challenge must have exactly 2 manage_data operations")

	op0, ok := ops[0].(*txnbuild.ManageData)
	require.True(t, ok, "first op must be manage_data")
	assert.Equal(t, clientKP.Address(), op0.SourceAccount, "first op source must be client account")
	assert.Equal(t, cfg.HomeDomain+" auth", op0.Name, "first op key must use the home domain")

	op1, ok := ops[1].(*txnbuild.ManageData)
	require.True(t, ok, "second op must be manage_data")
	assert.Equal(t, cfg.AnchorSigningKey, op1.SourceAccount, "second op source must be anchor")
	assert.Equal(t, "web_auth_domain", op1.Name)
	assert.Equal(t, []byte(cfg.WebAuthDomain), op1.Value)
}

func TestGetChallenge_PassesSDKValidation(t *testing.T) {
	cfg := testConfig(t)
	clientKP := stellarkeypair.MustRandom()

	_, client, domain, _, err := txnbuild.ReadChallengeTx(
		fetchChallenge(t, cfg, clientKP.Address()),
		cfg.AnchorSigningKey, cfg.NetworkPassphrase, cfg.WebAuthDomain, []string{cfg.HomeDomain},
	)
	require.NoError(t, err)
	assert.Equal(t, clientKP.Address(), client)
	assert.Equal(t, cfg.HomeDomain, domain)
}

func TestPostChallenge_MissingBody(t *testing.T) {
	cfg := testConfig(t)
	handler := sep10.PostChallenge(cfg, accountNotFound)

	req := httptest.NewRequest(http.MethodPost, "/sep10/auth", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostChallenge_InvalidXDR(t *testing.T) {
	cfg := testConfig(t)
	w := postChallenge(t, sep10.PostChallenge(cfg, accountNotFound), "not-valid-xdr")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostChallenge_ClientSignatureVerification(t *testing.T) {
	cfg := testConfig(t)
	clientKP := stellarkeypair.MustRandom()

	signed := signChallenge(t, cfg, fetchChallenge(t, cfg, clientKP.Address()), clientKP)
	w := postChallenge(t, sep10.PostChallenge(cfg, accountNotFound), signed)

	require.Equal(t, http.StatusOK, w.Code)
	var tokenResp struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tokenResp))
	require.NotEmpty(t, tokenResp.Token, "JWT must be returned on successful verification")

	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(tokenResp.Token, claims, func(*jwt.Token) (any, error) {
		return []byte(cfg.JWTSecret), nil
	})
	require.NoError(t, err)
	assert.Equal(t, clientKP.Address(), claims["sub"])
	assert.NotEmpty(t, claims["jti"])
}

func TestPostChallenge_UnsignedChallengeFails(t *testing.T) {
	cfg := testConfig(t)
	clientKP := stellarkeypair.MustRandom()

	// Only the anchor signature is present
	w := postChallenge(t, sep10.PostChallenge(cfg, accountNotFound), fetchChallenge(t, cfg, clientKP.Address()))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPostChallenge_WrongKeyFails(t *testing.T) {
	cfg := testConfig(t)
	clientKP := stellarkeypair.MustRandom()
	wrongKP := stellarkeypair.MustRandom()

	signed := signChallenge(t, cfg, fetchChallenge(t, cfg, clientKP.Address()), wrongKP)
	w := postChallenge(t, sep10.PostChallenge(cfg, accountNotFound), signed)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPostChallenge_ExpiredChallengeFails(t *testing.T) {
	cfg := testConfig(t)
	clientKP := stellarkeypair.MustRandom()

	past := time.Now().Add(-time.Hour)
	challenge := buildChallenge(t, cfg, clientKP.Address(), cfg.HomeDomain, cfg.WebAuthDomain,
		past.Unix(), past.Add(15*time.Minute).Unix())
	signed := signChallenge(t, cfg, challenge, clientKP)

	w := postChallenge(t, sep10.PostChallenge(cfg, accountNotFound), signed)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "timebounds")
}

func TestPostChallenge_InfiniteTimeboundsFails(t *testing.T) {
	cfg := testConfig(t)
	clientKP := stellarkeypair.MustRandom()

	challenge := buildChallenge(t, cfg, clientKP.Address(), cfg.HomeDomain, cfg.WebAuthDomain,
		time.Now().Unix(), txnbuild.TimeoutInfinite)
	signed := signChallenge(t, cfg, challenge, clientKP)

	w := postChallenge(t, sep10.PostChallenge(cfg, accountNotFound), signed)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostChallenge_ReplayFails(t *testing.T) {
	cfg := testConfig(t)
	clientKP := stellarkeypair.MustRandom()
	handler := sep10.PostChallenge(cfg, accountNotFound)

	signed := signChallenge(t, cfg, fetchChallenge(t, cfg, clientKP.Address()), clientKP)

	require.Equal(t, http.StatusOK, postChallenge(t, handler, signed).Code)
	w := postChallenge(t, handler, signed)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "already been used")
}

func TestPostChallenge_WrongHomeDomainFails(t *testing.T) {
	cfg := testConfig(t)
	clientKP := stellarkeypair.MustRandom()

	now := time.Now()
	challenge := buildChallenge(t, cfg, clientKP.Address(), "evil.com", cfg.WebAuthDomain,
		now.Unix(), now.Add(15*time.Minute).Unix())
	signed := signChallenge(t, cfg, challenge, clientKP)

	w := postChallenge(t, sep10.PostChallenge(cfg, accountNotFound), signed)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostChallenge_WebAuthDomainAsHomeDomainFails(t *testing.T) {
	cfg := testConfig(t)
	clientKP := stellarkeypair.MustRandom()

	// The old challenge format keyed the first op on WEB_AUTH_DOMAIN
	now := time.Now()
	challenge := buildChallenge(t, cfg, clientKP.Address(), cfg.WebAuthDomain, cfg.WebAuthDomain,
		now.Unix(), now.Add(15*time.Minute).Unix())
	signed := signChallenge(t, cfg, challenge, clientKP)

	w := postChallenge(t, sep10.PostChallenge(cfg, accountNotFound), signed)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostChallenge_WrongWebAuthDomainFails(t *testing.T) {
	cfg := testConfig(t)
	clientKP := stellarkeypair.MustRandom()

	now := time.Now()
	challenge := buildChallenge(t, cfg, clientKP.Address(), cfg.HomeDomain, "auth.evil.com",
		now.Unix(), now.Add(15*time.Minute).Unix())
	signed := signChallenge(t, cfg, challenge, clientKP)

	w := postChallenge(t, sep10.PostChallenge(cfg, accountNotFound), signed)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostChallenge_NotIssuedByAnchorFails(t *testing.T) {
	cfg := testConfig(t)
	clientKP := stellarkeypair.MustRandom()

	// Same structure, but signed by a key that is not the anchor
	other := testConfig(t)
	other.AnchorSigningKey = cfg.AnchorSigningKey
	challenge := buildChallenge(t, other, clientKP.Address(), cfg.HomeDomain, cfg.WebAuthDomain,
		time.Now().Unix(), time.Now().Add(15*time.Minute).Unix())
	signed := signChallenge(t, cfg, challenge, clientKP)

	w := postChallenge(t, sep10.PostChallenge(cfg, accountNotFound), signed)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostChallenge_ExistingAccountMasterKey(t *testing.T) {
	cfg := testConfig(t)
	clientKP := stellarkeypair.MustRandom()
	horizon := existingAccount(clientKP.Address(), 1, map[string]int32{clientKP.Address(): 1})

	signed := signChallenge(t, cfg, fetchChallenge(t, cfg, clientKP.Address()), clientKP)
	w := postChallenge(t, sep10.PostChallenge(cfg, horizon), signed)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPostChallenge_MasterKeyRemovedFails(t *testing.T) {
	cfg := testConfig(t)
	clientKP := stellarkeypair.MustRandom()
	newKP := stellarkeypair.MustRandom()
	horizon := existingAccount(clientKP.Address(), 1, map[string]int32{
		clientKP.Address(): 0,
		newKP.Address():    1,
	})
	handler := sep10.PostChallenge(cfg, horizon)

	oldKey := signChallenge(t, cfg, fetchChallenge(t, cfg, clientKP.Address()), clientKP)
	assert.Equal(t, http.StatusUnauthorized, postChallenge(t, handler, oldKey).Code)

	newKey := signChallenge(t, cfg, fetchChallenge(t, cfg, clientKP.Address()), newKP)
	assert.Equal(t, http.StatusOK, postChallenge(t, handler, newKey).Code)
}

func TestPostChallenge_ZeroThresholdStillNeedsWeight(t *testing.T) {
	cfg := testConfig(t)
	clientKP := stellarkeypair.MustRandom()
	newKP := stellarkeypair.MustRandom()
	horizon := existingAccount(clientKP.Address(), 0, map[string]int32{
		clientKP.Address(): 0,
		newKP.Address():    1,
	})

	signed := signChallenge(t, cfg, fetchChallenge(t, cfg, clientKP.Address()), clientKP)
	w := postChallenge(t, sep10.PostChallenge(cfg, horizon), signed)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPostChallenge_MultisigThreshold(t *testing.T) {
	cfg := testConfig(t)
	clientKP := stellarkeypair.MustRandom()
	cosignerKP := stellarkeypair.MustRandom()
	horizon := existingAccount(clientKP.Address(), 2, map[string]int32{
		clientKP.Address():   1,
		cosignerKP.Address(): 1,
	})
	handler := sep10.PostChallenge(cfg, horizon)

	oneKey := signChallenge(t, cfg, fetchChallenge(t, cfg, clientKP.Address()), clientKP)
	assert.Equal(t, http.StatusUnauthorized, postChallenge(t, handler, oneKey).Code)

	bothKeys := signChallenge(t, cfg, fetchChallenge(t, cfg, clientKP.Address()), clientKP, cosignerKP)
	assert.Equal(t, http.StatusOK, postChallenge(t, handler, bothKeys).Code)
}

func TestPostChallenge_HorizonErrorFails(t *testing.T) {
	cfg := testConfig(t)
	clientKP := stellarkeypair.MustRandom()
	horizon := fakeHorizon{err: errors.New("connection refused")}

	signed := signChallenge(t, cfg, fetchChallenge(t, cfg, clientKP.Address()), clientKP)
	w := postChallenge(t, sep10.PostChallenge(cfg, horizon), signed)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}
