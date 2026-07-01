package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	// Server
	Port string
	Env  string // "testnet" | "mainnet"

	// Stellar
	NetworkPassphrase  string
	HorizonURL         string
	AnchorSigningSeed  string // S... seed for SEP-10 signing
	AnchorSigningKey   string // G... public key (derived, set at startup)
	DistributionSeed   string // S... seed for funded distribution account
	DistributionKey    string // G... public key

	// USDC
	USDCIssuer string

	// SEP-10
	WebAuthDomain  string // e.g. "anchor.slimepay.com"
	HomeDomain     string // e.g. "slimepay.com"
	JWTSecret      string
	JWTExpiryHours int

	// Database
	DatabaseURL string

	// Slimepay core backend (internal API for KYC, payouts)
	CoreAPIURL    string
	CoreAPISecret string
}

func Load() (*Config, error) {
	env := getEnv("STELLAR_ENV", "testnet")

	var horizonURL, passphrase, usdcIssuer string
	switch strings.ToLower(env) {
	case "mainnet":
		horizonURL = "https://horizon.stellar.org"
		passphrase = "Public Global Stellar Network ; September 2015"
		usdcIssuer = getEnv("USDC_ISSUER", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	default:
		horizonURL = "https://horizon-testnet.stellar.org"
		passphrase = "Test SDF Network ; September 2015"
		usdcIssuer = getEnv("USDC_ISSUER", "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5")
	}

	cfg := &Config{
		Port:              getEnv("PORT", "8081"),
		Env:               env,
		NetworkPassphrase: passphrase,
		HorizonURL:        getEnv("HORIZON_URL", horizonURL),
		AnchorSigningSeed: mustEnv("ANCHOR_SIGNING_SEED"),
		DistributionSeed:  mustEnv("DISTRIBUTION_SEED"),
		USDCIssuer:        usdcIssuer,
		WebAuthDomain:     mustEnv("WEB_AUTH_DOMAIN"),
		HomeDomain:        mustEnv("HOME_DOMAIN"),
		JWTSecret:         mustEnv("JWT_SECRET"),
		JWTExpiryHours:    24,
		DatabaseURL:       mustEnv("DATABASE_URL"),
		CoreAPIURL:        getEnv("CORE_API_URL", "http://localhost:8080"),
		CoreAPISecret:     mustEnv("CORE_API_SECRET"),
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required env var %s is not set", key))
	}
	return v
}
