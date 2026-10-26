# stellar-anchor

Slimepay's Stellar anchor. Users send USDC or XLM on Stellar and receive naira in a Nigerian bank account.

The anchor runs on the [Stellar Anchor Platform](https://github.com/stellar/anchor-platform) (SAP). SAP serves every SEP endpoint wallets talk to (SEP-1, 10, 12, 6 and 24), keeps the transaction records and watches the ledger for incoming payments. This repository is the **Business Server** behind it: the part that is specific to Slimepay.

- **KYC.** SAP's customer callback (`/customer`). On mainnet it uses Slimepay's production Dojah integration: BVN or NIN check plus sanctions and PEP screening. On testnet the Dojah verification is simulated.
- **SEP-24 withdrawal page.** The interactive page SAP sends wallets to. A new user verifies their identity first, on its own step. A verified user goes straight to a short withdrawal form: amount, naira rate and bank account.
- **Payouts.** Polls SAP for funded withdrawals, checks the payment against the ledger, asks Slimepay core to pay the naira out (through SafeHaven on mainnet, simulated on testnet), then reports back to SAP.
- **Reconciliation.** Compares SAP, the ledger and core for every finished withdrawal, and flags payments nobody can explain.

Only withdrawals are offered. The anchor runs on **testnet** during the SCF tranches.

See [ARCHITECTURE.md](docs/ARCHITECTURE.md) for the design, [TRANCHE1_UPDATES.md](docs/TRANCHE1_UPDATES.md) for what changed in Tranche 1, [TRANCHE1_EVIDENCE.md](docs/TRANCHE1_EVIDENCE.md) for the Validation Suite report and completed testnet withdrawals, and [TESTNET_RUNBOOK.md](docs/TESTNET_RUNBOOK.md) to deploy and verify it.

---

## How a withdrawal works

1. The wallet authenticates with SAP (SEP-10) and starts a SEP-24 withdrawal. SAP creates the transaction and sends the user to `/sep24/interactive` on this service.
2. The page checks SAP's token. If the user is not verified yet, it shows the identity step: name, email, date of birth and BVN or NIN. Dojah verifies the BVN or NIN and screens the name as soon as those are saved, before any bank details are asked for.
3. Once identity is verified, the user enters the amount and bank account. The bank account is resolved through core, the naira amount is quoted, and the Business Server calls `request_onchain_funds`. SAP assigns the memo.
4. The wallet pays the distribution account. SAP's observer matches the payment and moves the transaction to `pending_anchor`.
5. The processor checks the payment on Horizon (amount, asset, destination, memo), asks core to pay out, and calls `notify_offchain_funds_pending` then `notify_offchain_funds_sent`. SAP marks the withdrawal completed.

SEP-6 is the same without the page: the processor starts the withdrawal once the customer has passed KYC through SEP-12.

Anything that does not add up puts the transaction on hold (`notify_transaction_on_hold`) and sends an alert, and no naira is sent. That covers a short or missing payment, a wrong memo or asset, or a payout whose outcome is unknown. A payout core refuses ends the transaction with `notify_transaction_error`.

### Payouts are paid once

Core keys every payout on the SAP transaction ID, with a unique index, and claims it with a conditional update before calling SafeHaven. Calling it again, at the same time or after a crash, returns the same payout and never sends money twice. If SafeHaven does not answer, the payout is held for someone to check rather than retried.

### Testnet and mainnet

The SCF deliverables run on **testnet**. No real money and no real identities are used there, so two steps are simulated on testnet:

- **Dojah verification.** The Business Server accepts the customer without calling Dojah.
- **SafeHaven payout.** Slimepay core resolves the bank account and records the naira payout as sent. SafeHaven is not called and no money moves.

Everything else is the production code path: SAP's SEP endpoints and payment observer, the interactive page, the ledger checks, the payout API in core with its once-only guarantee, and the Platform API calls that complete the transaction.

On **mainnet** the same code uses Slimepay's production Dojah and SafeHaven integrations, the ones the live Slimepay app already uses. `STELLAR_NETWORK` on the Business Server and `ANCHOR_NETWORK` on core select the network. Every payout carries its network and core refuses one for a different network, so a testnet withdrawal can never reach the production payout path. Mainnet is outside the SCF tranches.

---

## Project layout

```
cmd/server/main.go       Wiring: config, stores, HTTP routes, processor and reconciler
internal/
  config/                Environment config (testnet or mainnet)
  auth/                  SAP tokens: callback auth, Platform API token, SEP-24 interactive token
  sap/                   Platform API client (JSON-RPC)
  customer/              /customer callback, KYC state, encrypted customer store
  kyc/                   Dojah BVN, NIN and AML screening
  interactive/           SEP-24 withdrawal page
  withdrawal/            Quotes, request_onchain_funds, payout processor, reconciliation
  payout/                Client for Slimepay core's anchor payout API
  chain/                 Horizon reader for ledger checks
  secure/                AES-256-GCM encryption for customer and bank data
  audit/                 Audit log (no personal data)
  alert/                 Alerts to a Slack compatible webhook
  money/                 Exact decimal amounts
  toml/                  stellar.toml, served to SAP
  db/                    MongoDB connection
deploy/                  Docker Compose stack for running everything on one machine
docs/                    Architecture, Tranche 1 notes, testnet runbook
```

### Endpoints

| Path | Who calls it | Auth |
|---|---|---|
| `GET /customer`, `PUT /customer`, `DELETE /customer/{id}` | SAP | SAP callback JWT |
| `GET, POST /sep24/interactive` | User's browser, opened by the wallet | SAP's interactive JWT |
| `GET /.well-known/stellar.toml` | SAP (`sep1.toml.type: url`) | none |
| `GET /healthz` | Monitoring | none |

### Data

MongoDB database `stellar_anchor`:

| Collection | Contents |
|---|---|
| `customers` | Stellar account, memo and KYC status in the clear; all SEP-9 fields encrypted |
| `withdrawals` | Amounts, quote, memo, payout and reconciliation state; bank account encrypted |
| `audit_log` | KYC decisions, withdrawals, holds, reconciliation results |
| `reconcile_state` | Horizon cursor for the unmatched payment scan |

---

## Configuration

Copy `.env.example` to `.env` to run the service on its own, or `deploy/.env.example` to `deploy/.env` for the full stack. The service refuses to start if anything required is missing or malformed, and lists every problem at once.

Shared secrets with SAP must match:

| Business Server | SAP |
|---|---|
| `PLATFORM_API_SECRET` | `SECRET_PLATFORM_API_AUTH_SECRET` |
| `CALLBACK_API_SECRET` | `SECRET_CALLBACK_API_AUTH_SECRET` |
| `SEP24_INTERACTIVE_JWT_SECRET` | `SECRET_SEP24_INTERACTIVE_URL_JWT_SECRET` |

`CORE_API_SECRET` must match `ANCHOR_API_SECRET` in Slimepay core.

---

## Live on testnet

| | URL |
|---|---|
| Home domain (stellar.toml) | https://slimepay.com/.well-known/stellar.toml |
| SEP endpoints (SAP) | https://anchor.slimepay.com |
| Business Server | https://stellar-anchor-production.up.railway.app |

These run on Railway as separate services (SAP with its Postgres, this Business Server, Slimepay core and the web app). The Business Server stores its data in MongoDB, set through `DATABASE_URL`. [TESTNET_RUNBOOK.md](docs/TESTNET_RUNBOOK.md) has the full setup, the variables for each service and the checks.

## Running

```bash
go run ./cmd/server
```

Everything on one machine with Docker Compose:

```bash
cd deploy
cp .env.example .env
docker compose up -d --build
```

## Tests

```bash
go test ./...
```

No database or network is needed. The tests cover token checks, every Platform API call, Dojah outcomes, the customer callback, the SEP-24 page, quoting, payouts (including retries, races and every hold condition), reconciliation and the stellar.toml.
