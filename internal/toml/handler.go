// Package toml builds the anchor's stellar.toml. SAP serves it to wallets
// (sep1.toml.type is url and points here), and the home domain proxies to SAP.
package toml

import (
	"fmt"
	"net/http"

	"aidmedium/stellar-anchor/internal/config"
)

func Handler(cfg *config.Config) http.HandlerFunc {
	body := Render(cfg)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = fmt.Fprint(w, body)
	}
}

// Render returns the stellar.toml content. Endpoints are on WEB_AUTH_DOMAIN,
// where SAP's SEP server runs.
func Render(cfg *config.Config) string {
	sep := "https://" + cfg.WebAuthDomain
	return fmt.Sprintf(`NETWORK_PASSPHRASE="%s"
HORIZON_URL="%s"
ACCOUNTS=["%s"]
SIGNING_KEY="%s"
WEB_AUTH_ENDPOINT="%s/auth"
TRANSFER_SERVER="%s/sep6"
TRANSFER_SERVER_SEP0024="%s/sep24"
KYC_SERVER="%s/sep12"

[DOCUMENTATION]
ORG_NAME="Slimepay"
ORG_URL="https://%s"
ORG_DESCRIPTION="Withdraw USDC and XLM from Stellar to Nigerian bank accounts."
ORG_TWITTER="slimepay"

[[CURRENCIES]]
code="USDC"
issuer="%s"
status="test"
is_asset_anchored=true
anchor_asset_type="fiat"
anchor_asset="USD"
display_decimals=2
desc="USD Coin. Withdraw to a Nigerian bank account in naira."

[[CURRENCIES]]
code="native"
status="test"
is_asset_anchored=false
anchor_asset_type="crypto"
display_decimals=7
desc="Stellar Lumens. Withdraw to a Nigerian bank account in naira."
`,
		cfg.NetworkPassphrase,
		cfg.HorizonURL,
		cfg.DistributionAccount,
		cfg.SigningKey,
		sep, sep, sep, sep,
		cfg.HomeDomain,
		cfg.USDCIssuer,
	)
}
