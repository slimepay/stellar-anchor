package toml_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"aidmedium/stellar-anchor/internal/config"
	"aidmedium/stellar-anchor/internal/toml"

	burnttoml "github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testConfig() *config.Config {
	return &config.Config{
		NetworkPassphrase:   config.TestnetPassphrase,
		HorizonURL:          config.TestnetHorizonURL,
		SigningKey:          "GDQERENWDDSQZS7R7WKHZI3BSOYMV3FSWR7TFUYFTKQ447PIX6NREOJM",
		DistributionAccount: "GBN4NNCDGJO4XW4KQU3CBIESUJWFVBUZPOKUZHT7W7WRB7CWOA7BXVQF",
		USDCIssuer:          config.TestnetUSDCIssuer,
		HomeDomain:          "example.com",
		WebAuthDomain:       "anchor.example.com",
	}
}

type parsed struct {
	NetworkPassphrase     string   `toml:"NETWORK_PASSPHRASE"`
	Accounts              []string `toml:"ACCOUNTS"`
	SigningKey            string   `toml:"SIGNING_KEY"`
	WebAuthEndpoint       string   `toml:"WEB_AUTH_ENDPOINT"`
	TransferServer        string   `toml:"TRANSFER_SERVER"`
	TransferServerSep0024 string   `toml:"TRANSFER_SERVER_SEP0024"`
	KYCServer             string   `toml:"KYC_SERVER"`
	Currencies            []struct {
		Code   string `toml:"code"`
		Issuer string `toml:"issuer"`
		Status string `toml:"status"`
	} `toml:"CURRENCIES"`
}

func render(t *testing.T) (*httptest.ResponseRecorder, parsed) {
	t.Helper()
	w := httptest.NewRecorder()
	toml.Handler(testConfig())(w, httptest.NewRequest(http.MethodGet, "/.well-known/stellar.toml", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var p parsed
	_, err := burnttoml.Decode(w.Body.String(), &p)
	require.NoError(t, err, "stellar.toml must be valid TOML")
	return w, p
}

func TestHeaders(t *testing.T) {
	w, _ := render(t)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
}

func TestEndpointsPointAtSAP(t *testing.T) {
	_, p := render(t)
	assert.Equal(t, "https://anchor.example.com/auth", p.WebAuthEndpoint)
	assert.Equal(t, "https://anchor.example.com/sep6", p.TransferServer)
	assert.Equal(t, "https://anchor.example.com/sep24", p.TransferServerSep0024)
	assert.Equal(t, "https://anchor.example.com/sep12", p.KYCServer)
}

func TestKeysAndNetwork(t *testing.T) {
	_, p := render(t)
	cfg := testConfig()
	assert.Equal(t, cfg.NetworkPassphrase, p.NetworkPassphrase)
	assert.Equal(t, cfg.SigningKey, p.SigningKey)
	assert.Equal(t, []string{cfg.DistributionAccount}, p.Accounts)
}

func TestMainnetAssetsAreLive(t *testing.T) {
	cfg := testConfig()
	cfg.Network = config.Mainnet
	var p parsed
	_, err := burnttoml.Decode(toml.Render(cfg), &p)
	require.NoError(t, err)
	for _, c := range p.Currencies {
		assert.Equal(t, "live", c.Status)
	}
}

func TestCurrenciesAreTestAssets(t *testing.T) {
	_, p := render(t)
	require.Len(t, p.Currencies, 2)
	assert.Equal(t, "USDC", p.Currencies[0].Code)
	assert.Equal(t, config.TestnetUSDCIssuer, p.Currencies[0].Issuer)
	assert.Equal(t, "native", p.Currencies[1].Code)
	for _, c := range p.Currencies {
		assert.Equal(t, "test", c.Status)
	}
}
