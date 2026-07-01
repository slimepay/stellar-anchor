package toml_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aidmedium/stellar-anchor/internal/config"
	"aidmedium/stellar-anchor/internal/toml"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testConfig() *config.Config {
	return &config.Config{
		Env:               "testnet",
		NetworkPassphrase: "Test SDF Network ; September 2015",
		HorizonURL:        "https://horizon-testnet.stellar.org",
		AnchorSigningKey:  "GDQERENWDDSQZS7R7WKHZI3BSOYMV3FSWR7TFUYFTKQ447PIX6NREOJM",
		DistributionKey:   "GDIST0000EXAMPLEKEY000000000000000000000000000000000000000",
		USDCIssuer:        "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5",
		HomeDomain:        "example.com",
		WebAuthDomain:     "anchor.example.com",
	}
}

func TestHandler_StatusOK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/.well-known/stellar.toml", nil)
	w := httptest.NewRecorder()
	toml.Handler(testConfig())(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHandler_ContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/.well-known/stellar.toml", nil)
	w := httptest.NewRecorder()
	toml.Handler(testConfig())(w, req)
	assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
}

func TestHandler_CORSWildcard(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/.well-known/stellar.toml", nil)
	w := httptest.NewRecorder()
	toml.Handler(testConfig())(w, req)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"),
		"CORS must be wildcard — any Stellar wallet must be able to fetch stellar.toml")
}

func TestHandler_ContainsNetworkPassphrase(t *testing.T) {
	cfg := testConfig()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/stellar.toml", nil)
	w := httptest.NewRecorder()
	toml.Handler(cfg)(w, req)
	assert.Contains(t, w.Body.String(), cfg.NetworkPassphrase)
}

func TestHandler_ContainsSigningKey(t *testing.T) {
	cfg := testConfig()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/stellar.toml", nil)
	w := httptest.NewRecorder()
	toml.Handler(cfg)(w, req)
	assert.Contains(t, w.Body.String(), cfg.AnchorSigningKey)
}

func TestHandler_ContainsSEPEndpoints(t *testing.T) {
	cfg := testConfig()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/stellar.toml", nil)
	w := httptest.NewRecorder()
	toml.Handler(cfg)(w, req)
	body := w.Body.String()

	endpoints := []string{
		"WEB_AUTH_ENDPOINT",
		"TRANSFER_SERVER_SEP0024",
		"TRANSFER_SERVER",
		"KYC_SERVER",
		"DIRECT_PAYMENT_SERVER",
	}
	for _, ep := range endpoints {
		assert.Contains(t, body, ep, "stellar.toml must declare %s", ep)
	}
}

func TestHandler_ContainsCurrencies(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/.well-known/stellar.toml", nil)
	w := httptest.NewRecorder()
	toml.Handler(testConfig())(w, req)
	body := w.Body.String()

	assert.Contains(t, body, `code="USDC"`)
	assert.Contains(t, body, `code="XLM"`)
}

func TestHandler_USDCIssuerPresent(t *testing.T) {
	cfg := testConfig()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/stellar.toml", nil)
	w := httptest.NewRecorder()
	toml.Handler(cfg)(w, req)
	assert.Contains(t, w.Body.String(), cfg.USDCIssuer)
}

func TestHandler_HomeDomainInEndpoints(t *testing.T) {
	cfg := testConfig()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/stellar.toml", nil)
	w := httptest.NewRecorder()
	toml.Handler(cfg)(w, req)
	body := w.Body.String()
	assert.True(t, strings.Contains(body, cfg.HomeDomain),
		"all SEP endpoint URLs must include the home domain")
}

func TestHandler_MainnetConfig(t *testing.T) {
	cfg := testConfig()
	cfg.Env = "mainnet"
	cfg.NetworkPassphrase = "Public Global Stellar Network ; September 2015"
	cfg.USDCIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

	req := httptest.NewRequest(http.MethodGet, "/.well-known/stellar.toml", nil)
	w := httptest.NewRecorder()
	toml.Handler(cfg)(w, req)

	body := w.Body.String()
	assert.Contains(t, body, "Public Global Stellar Network")
	assert.Contains(t, body, cfg.USDCIssuer)
	assert.Contains(t, body, "mainnet")
}
