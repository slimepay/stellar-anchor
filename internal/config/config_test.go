package config

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validEnv(t *testing.T) {
	t.Helper()
	secret := strings.Repeat("s", 32)
	for k, v := range map[string]string{
		"SEP10_SIGNING_KEY":            "GDQERENWDDSQZS7R7WKHZI3BSOYMV3FSWR7TFUYFTKQ447PIX6NREOJM",
		"DISTRIBUTION_ACCOUNT":         "GBN4NNCDGJO4XW4KQU3CBIESUJWFVBUZPOKUZHT7W7WRB7CWOA7BXVQF",
		"HOME_DOMAIN":                  "slimepay.com",
		"WEB_AUTH_DOMAIN":              "anchor.slimepay.com",
		"PLATFORM_API_URL":             "http://platform:8085/",
		"PLATFORM_API_SECRET":          secret,
		"CALLBACK_API_SECRET":          secret,
		"SEP24_INTERACTIVE_JWT_SECRET": secret,
		"DATABASE_URL":                 "mongodb://localhost:27017",
		"DATA_ENCRYPTION_KEY":          base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"DOJAH_APP_ID":                 "app",
		"DOJAH_SECRET_KEY":             "key",
		"CORE_API_URL":                 "https://core.example.com",
		"CORE_API_SECRET":              secret,
		"NGN_PER_USDC":                 "1500",
		"NGN_PER_XLM":                  "250.5",
		"STELLAR_NETWORK":              "",
		"USDC_ISSUER":                  "",
		"DOJAH_BASE_URL":               "",
		"POLL_INTERVAL":                "",
	} {
		t.Setenv(k, v)
	}
}

func TestLoad(t *testing.T) {
	validEnv(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, TestnetPassphrase, cfg.NetworkPassphrase)
	assert.Equal(t, TestnetUSDCIssuer, cfg.USDCIssuer)
	assert.Equal(t, Testnet, cfg.Network)
	assert.Equal(t, DojahURL, cfg.DojahBaseURL)
	assert.Equal(t, "http://platform:8085", cfg.PlatformURL)
	assert.Equal(t, "250.50", cfg.NGNPerXLM.FloatString(2))
	assert.Equal(t, "15s", cfg.PollInterval.String())
}

func TestLoadMainnet(t *testing.T) {
	validEnv(t)
	t.Setenv("STELLAR_NETWORK", "mainnet")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, Mainnet, cfg.Network)
	assert.Equal(t, MainnetPassphrase, cfg.NetworkPassphrase)
	assert.Equal(t, MainnetHorizonURL, cfg.HorizonURL)
	assert.Equal(t, MainnetUSDCIssuer, cfg.USDCIssuer)

	t.Setenv("DOJAH_APP_ID", "")
	_, err = Load()
	assert.ErrorContains(t, err, "DOJAH_APP_ID", "Dojah is required on mainnet")
}

func TestLoadTestnetWithoutDojah(t *testing.T) {
	validEnv(t)
	t.Setenv("DOJAH_APP_ID", "")
	t.Setenv("DOJAH_SECRET_KEY", "")
	_, err := Load()
	assert.NoError(t, err)
}

func TestLoadRejectsUnknownNetwork(t *testing.T) {
	validEnv(t)
	t.Setenv("STELLAR_NETWORK", "futurenet")
	_, err := Load()
	assert.ErrorContains(t, err, "STELLAR_NETWORK")
}

func TestLoadRejectsBadValues(t *testing.T) {
	cases := map[string]string{
		"SEP10_SIGNING_KEY":   "SBADSECRETINSTEADOFPUBLICKEY",
		"PLATFORM_API_SECRET": "short",
		"DATA_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(make([]byte, 16)),
		"NGN_PER_USDC":        "-1",
		"POLL_INTERVAL":       "soon",
	}
	for key, value := range cases {
		t.Run(key, func(t *testing.T) {
			validEnv(t)
			t.Setenv(key, value)
			_, err := Load()
			assert.ErrorContains(t, err, key)
		})
	}
}

func TestLoadListsMissing(t *testing.T) {
	validEnv(t)
	t.Setenv("DATABASE_URL", "")
	t.Setenv("CORE_API_SECRET", "")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_URL")
	assert.Contains(t, err.Error(), "CORE_API_SECRET")
}
