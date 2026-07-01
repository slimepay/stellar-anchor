package sep10_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"aidmedium/stellar-anchor/internal/config"
	"aidmedium/stellar-anchor/internal/sep10"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stellarkeypair "github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/txnbuild"
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
	handler := sep10.GetChallenge(cfg)

	clientKP := stellarkeypair.MustRandom()
	req := httptest.NewRequest(http.MethodGet, "/sep10/auth?account="+clientKP.Address(), nil)
	w := httptest.NewRecorder()
	handler(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Transaction string `json:"transaction"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	parsed, err := txnbuild.TransactionFromXDR(resp.Transaction)
	require.NoError(t, err)
	tx, ok := parsed.Transaction()
	require.True(t, ok, "expected a regular transaction")

	ops := tx.Operations()
	require.Len(t, ops, 2, "challenge must have exactly 2 manage_data operations")

	op0, ok := ops[0].(*txnbuild.ManageData)
	require.True(t, ok, "first op must be manage_data")
	assert.Equal(t, clientKP.Address(), op0.SourceAccount, "first op source must be client account")
	assert.Contains(t, op0.Name, "auth")

	op1, ok := ops[1].(*txnbuild.ManageData)
	require.True(t, ok, "second op must be manage_data")
	assert.Equal(t, cfg.AnchorSigningKey, op1.SourceAccount, "second op source must be anchor")
	assert.Equal(t, "web_auth_domain", op1.Name)
	assert.Equal(t, []byte(cfg.WebAuthDomain), op1.Value)
}

func TestPostChallenge_MissingBody(t *testing.T) {
	cfg := testConfig(t)
	handler := sep10.PostChallenge(cfg)

	req := httptest.NewRequest(http.MethodPost, "/sep10/auth", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostChallenge_InvalidXDR(t *testing.T) {
	cfg := testConfig(t)
	handler := sep10.PostChallenge(cfg)

	body, _ := json.Marshal(map[string]string{"transaction": "not-valid-xdr"})
	req := httptest.NewRequest(http.MethodPost, "/sep10/auth", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostChallenge_ClientSignatureVerification(t *testing.T) {
	cfg := testConfig(t)
	getHandler := sep10.GetChallenge(cfg)
	postHandler := sep10.PostChallenge(cfg)

	clientKP := stellarkeypair.MustRandom()

	// Step 1: get challenge
	getReq := httptest.NewRequest(http.MethodGet, "/sep10/auth?account="+clientKP.Address(), nil)
	getW := httptest.NewRecorder()
	getHandler(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code)

	var challengeResp struct {
		Transaction string `json:"transaction"`
	}
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &challengeResp))

	// Step 2: client signs the challenge
	parsed, err := txnbuild.TransactionFromXDR(challengeResp.Transaction)
	require.NoError(t, err)
	tx, ok := parsed.Transaction()
	require.True(t, ok)
	tx, err = tx.Sign(cfg.NetworkPassphrase, clientKP)
	require.NoError(t, err)
	signedXDR, err := tx.Base64()
	require.NoError(t, err)

	// Step 3: submit signed challenge
	postBody, _ := json.Marshal(map[string]string{"transaction": signedXDR})
	postReq := httptest.NewRequest(http.MethodPost, "/sep10/auth", bytes.NewBuffer(postBody))
	postReq.Header.Set("Content-Type", "application/json")
	postW := httptest.NewRecorder()
	postHandler(postW, postReq)

	require.Equal(t, http.StatusOK, postW.Code)
	var tokenResp struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(postW.Body.Bytes(), &tokenResp))
	assert.NotEmpty(t, tokenResp.Token, "JWT must be returned on successful verification")
}

func TestPostChallenge_UnsignedChallengeFails(t *testing.T) {
	cfg := testConfig(t)
	getHandler := sep10.GetChallenge(cfg)
	postHandler := sep10.PostChallenge(cfg)

	clientKP := stellarkeypair.MustRandom()
	getReq := httptest.NewRequest(http.MethodGet, "/sep10/auth?account="+clientKP.Address(), nil)
	getW := httptest.NewRecorder()
	getHandler(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code)

	var challengeResp struct {
		Transaction string `json:"transaction"`
	}
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &challengeResp))

	// Submit without signing (only anchor sig present)
	postBody, _ := json.Marshal(map[string]string{"transaction": challengeResp.Transaction})
	postReq := httptest.NewRequest(http.MethodPost, "/sep10/auth", bytes.NewBuffer(postBody))
	postReq.Header.Set("Content-Type", "application/json")
	postW := httptest.NewRecorder()
	postHandler(postW, postReq)

	assert.Equal(t, http.StatusUnauthorized, postW.Code)
}

func TestPostChallenge_WrongKeyFails(t *testing.T) {
	cfg := testConfig(t)
	getHandler := sep10.GetChallenge(cfg)
	postHandler := sep10.PostChallenge(cfg)

	clientKP := stellarkeypair.MustRandom()
	wrongKP := stellarkeypair.MustRandom()

	getReq := httptest.NewRequest(http.MethodGet, "/sep10/auth?account="+clientKP.Address(), nil)
	getW := httptest.NewRecorder()
	getHandler(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code)

	var challengeResp struct {
		Transaction string `json:"transaction"`
	}
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &challengeResp))

	// Sign with the WRONG keypair
	parsed, err := txnbuild.TransactionFromXDR(challengeResp.Transaction)
	require.NoError(t, err)
	tx, ok := parsed.Transaction()
	require.True(t, ok)
	tx, err = tx.Sign(cfg.NetworkPassphrase, wrongKP)
	require.NoError(t, err)
	signedXDR, _ := tx.Base64()

	postBody, _ := json.Marshal(map[string]string{"transaction": signedXDR})
	postReq := httptest.NewRequest(http.MethodPost, "/sep10/auth", bytes.NewBuffer(postBody))
	postReq.Header.Set("Content-Type", "application/json")
	postW := httptest.NewRecorder()
	postHandler(postW, postReq)

	assert.Equal(t, http.StatusUnauthorized, postW.Code)
}
