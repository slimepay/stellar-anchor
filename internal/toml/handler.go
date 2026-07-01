package toml

import (
	"fmt"
	"net/http"

	"aidmedium/stellar-anchor/internal/config"
)

// Handler serves /.well-known/stellar.toml — the anchor discovery file.
// Stellar wallets, SEP validators, and other anchors fetch this first.
func Handler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		// CORS required: any Stellar wallet must be able to fetch this
		w.Header().Set("Access-Control-Allow-Origin", "*")

		network := "testnet"
		if cfg.Env == "mainnet" {
			network = "mainnet"
		}

		toml := fmt.Sprintf(`# Slimepay Stellar Anchor
# https://%s/.well-known/stellar.toml

NETWORK_PASSPHRASE="%s"
HORIZON_URL="%s"
ACCOUNTS=["%s"]
VERSION="0.1.0"
SIGNING_KEY="%s"

WEB_AUTH_ENDPOINT="https://%s/sep10/auth"
TRANSFER_SERVER_SEP0024="https://%s/sep24"
TRANSFER_SERVER="https://%s/sep6"
KYC_SERVER="https://%s/sep12"
DIRECT_PAYMENT_SERVER="https://%s/sep31"

[[CURRENCIES]]
code="USDC"
issuer="%s"
status="live"
is_asset_anchored=true
anchor_asset_type="fiat"
anchor_asset="USD"
desc="USD Coin on Stellar"
display_decimals=2

[[CURRENCIES]]
code="XLM"
status="live"
is_asset_anchored=false
desc="Stellar Lumens"
display_decimals=7

[DOCUMENTATION]
ORG_NAME="Slimepay"
ORG_URL="https://%s"
ORG_DESCRIPTION="Slimepay Stellar Anchor — NGN settlement via USDC and XLM"
ORG_TWITTER="@slimepay"

# Environment: %s
`,
			cfg.HomeDomain,
			cfg.NetworkPassphrase,
			cfg.HorizonURL,
			cfg.DistributionKey,
			cfg.AnchorSigningKey,
			cfg.HomeDomain,
			cfg.HomeDomain,
			cfg.HomeDomain,
			cfg.HomeDomain,
			cfg.HomeDomain,
			cfg.USDCIssuer,
			cfg.HomeDomain,
			network,
		)

		fmt.Fprint(w, toml)
	}
}
