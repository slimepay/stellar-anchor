package config

import (
	"encoding/base64"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
)

const (
	Testnet = "testnet"
	Mainnet = "mainnet"

	TestnetPassphrase = "Test SDF Network ; September 2015"
	TestnetHorizonURL = "https://horizon-testnet.stellar.org"
	TestnetUSDCIssuer = "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5"

	MainnetPassphrase = "Public Global Stellar Network ; September 2015"
	MainnetHorizonURL = "https://horizon.stellar.org"
	MainnetUSDCIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

	DojahURL = "https://api.dojah.io"

	minSharedSecretLen = 32
)

type Config struct {
	Port    string
	Network string

	NetworkPassphrase   string
	HorizonURL          string
	USDCIssuer          string
	SigningKey          string
	DistributionAccount string

	HomeDomain    string
	WebAuthDomain string

	PlatformURL          string
	PlatformAPISecret    string
	CallbackAPISecret    string
	InteractiveJWTSecret string

	DatabaseURL   string
	DatabaseName  string
	EncryptionKey []byte

	DojahBaseURL   string
	DojahAppID     string
	DojahSecretKey string

	CoreAPIURL    string
	CoreAPISecret string

	NGNPerUSDC *big.Rat
	NGNPerXLM  *big.Rat

	PollInterval      time.Duration
	ReconcileInterval time.Duration
	AlertWebhookURL   string
}

// Load reads the Business Server configuration from the environment.
// STELLAR_NETWORK is testnet (the default) or mainnet. Dojah is only used on
// mainnet, so its settings are only required there.
func Load() (*Config, error) {
	var missing []string
	req := func(key string) string {
		v := strings.TrimSpace(os.Getenv(key))
		if v == "" {
			missing = append(missing, key)
		}
		return v
	}

	network := strings.ToLower(getEnv("STELLAR_NETWORK", Testnet))
	passphrase, horizon, usdc := TestnetPassphrase, TestnetHorizonURL, TestnetUSDCIssuer
	switch network {
	case Testnet:
	case Mainnet:
		passphrase, horizon, usdc = MainnetPassphrase, MainnetHorizonURL, MainnetUSDCIssuer
	default:
		return nil, fmt.Errorf("STELLAR_NETWORK must be testnet or mainnet, got %q", network)
	}

	cfg := &Config{
		Port:                 getEnv("PORT", "8081"),
		Network:              network,
		NetworkPassphrase:    passphrase,
		HorizonURL:           getEnv("HORIZON_URL", horizon),
		USDCIssuer:           getEnv("USDC_ISSUER", usdc),
		SigningKey:           req("SEP10_SIGNING_KEY"),
		DistributionAccount:  req("DISTRIBUTION_ACCOUNT"),
		HomeDomain:           req("HOME_DOMAIN"),
		WebAuthDomain:        req("WEB_AUTH_DOMAIN"),
		PlatformURL:          strings.TrimRight(req("PLATFORM_API_URL"), "/"),
		PlatformAPISecret:    req("PLATFORM_API_SECRET"),
		CallbackAPISecret:    req("CALLBACK_API_SECRET"),
		InteractiveJWTSecret: req("SEP24_INTERACTIVE_JWT_SECRET"),
		DatabaseURL:          req("DATABASE_URL"),
		DatabaseName:         getEnv("DATABASE_NAME", "stellar_anchor"),
		DojahBaseURL:         strings.TrimRight(getEnv("DOJAH_BASE_URL", DojahURL), "/"),
		DojahAppID:           strings.TrimSpace(os.Getenv("DOJAH_APP_ID")),
		DojahSecretKey:       strings.TrimSpace(os.Getenv("DOJAH_SECRET_KEY")),
		CoreAPIURL:           strings.TrimRight(req("CORE_API_URL"), "/"),
		CoreAPISecret:        req("CORE_API_SECRET"),
		AlertWebhookURL:      strings.TrimSpace(os.Getenv("ALERT_WEBHOOK_URL")),
	}
	if network == Mainnet {
		req("DOJAH_APP_ID")
		req("DOJAH_SECRET_KEY")
	}
	encKey := req("DATA_ENCRYPTION_KEY")
	usdcRate := req("NGN_PER_USDC")
	xlmRate := req("NGN_PER_XLM")

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}

	for _, kv := range [][2]string{
		{"SEP10_SIGNING_KEY", cfg.SigningKey},
		{"DISTRIBUTION_ACCOUNT", cfg.DistributionAccount},
		{"USDC_ISSUER", cfg.USDCIssuer},
	} {
		if !strkey.IsValidEd25519PublicKey(kv[1]) {
			return nil, fmt.Errorf("%s must be a Stellar public key (G...)", kv[0])
		}
	}

	for _, kv := range [][2]string{
		{"PLATFORM_API_SECRET", cfg.PlatformAPISecret},
		{"CALLBACK_API_SECRET", cfg.CallbackAPISecret},
		{"SEP24_INTERACTIVE_JWT_SECRET", cfg.InteractiveJWTSecret},
		{"CORE_API_SECRET", cfg.CoreAPISecret},
	} {
		if len(kv[1]) < minSharedSecretLen {
			return nil, fmt.Errorf("%s must be at least %d characters", kv[0], minSharedSecretLen)
		}
	}

	key, err := base64.StdEncoding.DecodeString(encKey)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("DATA_ENCRYPTION_KEY must be 32 bytes, base64 encoded")
	}
	cfg.EncryptionKey = key

	if cfg.NGNPerUSDC, err = positiveRate("NGN_PER_USDC", usdcRate); err != nil {
		return nil, err
	}
	if cfg.NGNPerXLM, err = positiveRate("NGN_PER_XLM", xlmRate); err != nil {
		return nil, err
	}
	if cfg.PollInterval, err = duration("POLL_INTERVAL", "15s"); err != nil {
		return nil, err
	}
	if cfg.ReconcileInterval, err = duration("RECONCILE_INTERVAL", "10m"); err != nil {
		return nil, err
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func positiveRate(key, v string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(v)
	if !ok || r.Sign() <= 0 {
		return nil, fmt.Errorf("%s must be a positive number", key)
	}
	return r, nil
}

func duration(key, fallback string) (time.Duration, error) {
	d, err := time.ParseDuration(getEnv(key, fallback))
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration such as 15s or 10m", key)
	}
	return d, nil
}
