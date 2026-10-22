# stellar-anchor

This is Slimepay's Stellar Anchor, a standalone Go service that implements the Stellar Ecosystem Proposals (SEPs) needed to run a regulated fiat anchor. It runs alongside the main Slimepay backend and handles wallet authentication, KYC, withdrawals, and cross-border B2B payments.

This service targets **SEP-10**, **SEP-12**, **SEP-24**, **SEP-6**, and **SEP-31**.

> **Direction of the plan:** Slimepay is moving to the [Stellar Anchor Platform](https://github.com/stellar/anchor-platform) (SAP). SAP will serve the SEP endpoints, and this repo becomes the Business Server behind it: KYC through Dojah, naira payouts through SafeHaven, and transaction status updates. The handlers here are the testnet prototype. See [ARCHITECTURE.md](docs/ARCHITECTURE.md) for the target design and migration plan, and [TRANCHE1_UPDATES.md](docs/TRANCHE1_UPDATES.md) for the fixes made so far.

Only **withdrawals** are offered: a user sends USDC or XLM and receives naira in a Nigerian bank account. Deposits (naira in, Stellar asset out) are not supported.

---

## What's implemented

| SEP | Description |
|-----|-------------|
| SEP-1 | `stellar.toml`: the discovery file wallets fetch first |
| SEP-10 | Web Authentication: proves a wallet owns a keypair |
| SEP-12 | KYC: collects and stores customer identity information |
| SEP-24 | Interactive transfers: withdrawal with a hosted UI flow (the UI is not built yet) |
| SEP-6 | Programmatic transfers: withdrawal without a browser redirect |
| SEP-31 | Cross-border B2B payments: USDC in, NGN out |

---

## Project layout

```
stellar-anchor/
├── cmd/server/main.go              # Entry point. Reads config, connects DB, registers routes.
├── internal/
│   ├── config/config.go            # Env loading with testnet/mainnet defaults
│   ├── db/mongo.go                 # MongoDB connection (pings on startup)
│   ├── middleware/jwt.go           # SEP-10 JWT middleware + context helpers
│   ├── toml/handler.go             # Serves /.well-known/stellar.toml
│   ├── sep10/handler.go            # Challenge generation and signature verification
│   ├── sep12/{store,handler}.go    # KYC CRUD
│   ├── memo/memo.go                # Memo IDs in the same format SAP assigns
│   ├── sep24/{store,handler}.go    # Interactive withdrawal
│   ├── sep6/{store,handler}.go     # Programmatic withdrawal
│   └── sep31/{store,handler}.go    # Cross-border B2B payments
├── docs/
│   ├── ARCHITECTURE.md             # Target design: SAP + Business Server
│   └── TRANCHE1_UPDATES.md         # Tranche 1 highlights
├── .env.example
├── go.mod
└── go.sum
```

---

## How it works

### Authentication (SEP-10)

Before any protected endpoint will respond, a wallet needs to prove it controls a Stellar keypair. The flow:

1. Wallet calls `GET /sep10/auth?account=G...`
2. The anchor builds a Stellar transaction with sequence number `0`, so it can never be submitted on-chain. The first operation is a `<HOME_DOMAIN> auth` manage_data op with the wallet as source, the second carries `web_auth_domain`, and the challenge is valid for 15 minutes. The anchor signs it and returns the base64 XDR.
3. The wallet signs that same transaction with the customer's private key and posts it back to `POST /sep10/auth`.
4. The anchor validates the challenge: its own signature, sequence number, time bounds, home domain and `web_auth_domain`. If the account exists on the network, the wallet's signatures must meet the account's medium threshold using its current signers from Horizon. If the account doesn't exist yet, the master key signature is required. A signed challenge can only be used once. If everything checks out, it issues a JWT with the Stellar address as the `sub` claim.
5. Every protected endpoint from here on expects `Authorization: Bearer <token>`.

### Two keypairs

You need two separate Stellar keypairs to run this service:

- **Anchor signing key**: only used to sign SEP-10 challenge transactions. It doesn't hold any funds and doesn't need a minimum balance beyond account activation.
- **Distribution key**: the funded account that receives the USDC or XLM users send for withdrawals. It needs XLM for transaction fees and a USDC trustline.

### Memos

Every withdrawal and SEP-31 payment gets a random 64 bit memo ID (`memo_type` `id`), the same format the Anchor Platform assigns. The memo is stored on the transaction so the incoming payment can be matched to it.

### Domains

The SEP endpoints are served from `WEB_AUTH_DOMAIN` (the host this service runs on). Wallets fetch `stellar.toml` from `HOME_DOMAIN`, so that host must serve or proxy `/.well-known/stellar.toml` to this service. `WEB_AUTH_ENDPOINT` in the toml points at `WEB_AUTH_DOMAIN`, which is what wallets check against the `web_auth_domain` in the SEP-10 challenge.

Keeping them separate means a compromised signing key doesn't expose customer funds.

### Store interfaces

Each SEP handler takes a `Store` interface rather than a direct handle to MongoDB. In production `main.go` wires in the real `MongoStore`. In tests, each package defines a lightweight in-memory mock. No mocking library, no build tags, no test database required.

### MongoDB collections

The service writes to a dedicated `stellar_anchor` database.

| Collection | Written by |
|------------|------------|
| `sep12_customers` | SEP-12 KYC |
| `sep24_transactions` | SEP-24 interactive flows |
| `sep6_transactions` | SEP-6 programmatic flows |
| `sep31_transactions` | SEP-31 cross-border payments |

---

## Setup

### Prerequisites

- Go 1.21+
- MongoDB 6+ (the same instance the main server uses is fine)
- Two Stellar keypairs. On testnet you can generate and fund them at [laboratory.stellar.org](https://laboratory.stellar.org/#account-creator?network=test)

### Environment

```bash
cp .env.example .env
```

Open `.env` and fill in these values:

```env
STELLAR_ENV=testnet           # or mainnet

PORT=8081

ANCHOR_SIGNING_SEED=SXXX...   # Keypair used only for SEP-10 challenge signing
DISTRIBUTION_SEED=SXXX...     # Funded account that handles actual transfers

USDC_ISSUER=                  # Leave blank to use the default to the canonical issuer per network
HORIZON_URL=                  # Leave blank to use the default to the canonical Horizon per network

HOME_DOMAIN=slimepay.com
WEB_AUTH_DOMAIN=anchor.slimepay.com

JWT_SECRET=                   # openssl rand -hex 32

DATABASE_URL=mongodb://localhost:27017

CORE_API_URL=http://localhost:8080
CORE_API_SECRET=
```

The service will panic on startup if any required variable is missing. That's intentional, a half-configured anchor silently doing the wrong thing is harder to debug than one that refuses to start.

---

## Running the service

```bash
# Download dependencies
go mod download

# Run (picks up .env from the working directory)
go run ./cmd/server
```

On startup you'll see something like:

```
INF stellar-anchor starting env=testnet signing_key=GABC... distribution_key=GDEF...
INF connected to MongoDB
INF stellar-anchor listening addr=:8081
```

Check that the printed public keys match the keypairs you intended to use before sending any traffic.

To build a binary instead:

```bash
go build -o stellar-anchor ./cmd/server
./stellar-anchor
```

Quick sanity check, fetch the discovery file:

```bash
curl http://localhost:8081/.well-known/stellar.toml
```

---

## Running the tests

No database connection needed. No `.env` file needed. Every handler test uses an in-memory mock store, and SEP-10 tests generate fresh ephemeral keypairs on each run.

```bash
# All packages
go test ./...

# Verbose output
go test ./... -v

# Single package
go test ./internal/sep10/... -v
go test ./internal/sep24/... -v

# Single test
go test ./internal/sep10/... -run TestPostChallenge_ClientSignatureVerification -v
```

| Package | What's covered |
|---------|----------------|
| `middleware` | Missing header, malformed token, wrong secret, expired token, valid token, context propagation |
| `sep10` | Challenge structure (op sources, nonce, timebounds), full sign→verify round trip, unsigned and wrong keypair rejection, expired, replayed, wrong home domain and wrong web_auth_domain challenges, master key removed, multisig thresholds, Horizon errors |
| `sep12` | GET with no record, GET with existing record, PUT validation, idempotent update, DELETE, DB errors |
| `memo` | Memo IDs parse as 64 bit integers and don't collide |
| `sep24` | Info lists withdrawals only, withdrawal initiation for USDC and XLM, memo ID assignment, unsupported asset rejection, transaction scoped to account, store errors |
| `sep6` | Info lists withdrawals only, withdrawal dest validation, memo ID format and uniqueness, account-scoped transaction lookup, store errors |
| `sep31` | Receiver field validation, USDC-only enforcement, memo ID stored on the transaction, cross-border transaction creation and retrieval |
| `toml` | CORS wildcard, correct content-type, SEP endpoints on the web auth domain, assets marked `test` on testnet, mainnet config |

87 tests total

---

## API reference

Endpoints marked **[auth]** require `Authorization: Bearer <sep10-jwt>`.

### Discovery

```
GET /.well-known/stellar.toml
```

### SEP-10: Authentication

```
GET  /sep10/auth?account=G...    # Get challenge transaction
POST /sep10/auth                 # Submit signed challenge → receive JWT
```

`POST` body:
```json
{ "transaction": "<base64 XDR>" }
```

Response:
```json
{ "token": "<JWT>" }
```

### SEP-12: KYC

```
GET    /sep12/customer   [auth]
PUT    /sep12/customer   [auth]
DELETE /sep12/customer   [auth]
```

`PUT` requires `first_name`, `last_name`, `email`. Optional: `id_type`, `id_number`.

### SEP-24: Interactive Transfers

```
GET  /sep24/info
POST /sep24/transactions/withdraw/interactive  [auth]   # body: asset_code=USDC or XLM (form-encoded)
GET  /sep24/transaction?id=<tx-id>             [auth]
```

The transaction carries `withdraw_anchor_account` and a `withdraw_memo` (type `id`) the wallet must use when sending the funds.

### SEP-6: Programmatic Transfers

```
GET  /sep6/info
GET  /sep6/withdraw?asset_code=USDC&dest=...   [auth]   # dest = bank account number
GET  /sep6/transaction?id=<tx-id>              [auth]
```

Withdraw returns the distribution account address and a memo ID (`memo_type` `id`). The customer must include that memo when sending on-chain so the transfer can be matched to their record.

### SEP-31: Cross-Border Payments

```
GET  /sep31/info
POST /sep31/transactions         [auth]
GET  /sep31/transactions/{id}    [auth]
```

`POST` body:
```json
{
  "asset_code": "USDC",
  "amount": "500.00",
  "receiver_account_number": "1234567890",
  "receiver_bank_code": "058",
  "receiver_name": "Ade Okafor"
}
```

Response includes the anchor's `stellar_account_id` (distribution key) and a `stellar_memo` (type `id`) the sending institution must attach to their Stellar payment.
