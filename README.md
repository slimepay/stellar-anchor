# stellar-anchor

This is Slimepay's Stellar Anchor — a standalone Go service that implements the Stellar Ecosystem Proposals (SEPs) required to operate a regulated fiat-to-Stellar anchor. It runs independently alongside the main Slimepay backend and bridges customer accounts to the Stellar network, handling wallet authentication, KYC, deposits, withdrawals, and cross-border B2B payments.

This service targets **SEP-10**, **SEP-12**, **SEP-24**, **SEP-6**, and **SEP-31**.
---

## What's implemented

| SEP | Description |
|-----|-------------|
| SEP-1 | `stellar.toml` — the discovery file wallets fetch first |
| SEP-10 | Web Authentication — proves a wallet owns a keypair |
| SEP-12 | KYC — collects and stores customer identity information |
| SEP-24 | Interactive transfers — deposit/withdrawal with a hosted UI flow |
| SEP-6 | Programmatic transfers — deposit/withdrawal without a browser redirect |
| SEP-31 | Cross-border B2B payments — USDC in, NGN out |

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
│   ├── sep24/{store,handler}.go    # Interactive deposit/withdrawal
│   ├── sep6/{store,handler}.go     # Programmatic deposit/withdrawal
│   └── sep31/{store,handler}.go    # Cross-border B2B payments
├── .env.example
├── go.mod
└── go.sum
```

---

## How it works

### Authentication (SEP-10)

Before any protected endpoint will respond, a wallet needs to prove it controls a Stellar keypair. The flow:

1. Wallet calls `GET /sep10/auth?account=G...`
2. The anchor builds a Stellar transaction with sequence number `0` — it's intentionally invalid on-chain, so it can never be submitted. It signs this with the anchor's signing keypair and returns the base64 XDR.
3. The wallet signs that same transaction with the customer's private key and posts it back to `POST /sep10/auth`.
4. The anchor checks both signatures: its own (to prevent replay of a forged challenge) and the customer's (to prove keypair ownership). If both verify, it issues a JWT with the Stellar address as the `sub` claim.
5. Every protected endpoint from here on expects `Authorization: Bearer <token>`.

### Two keypairs

You need two separate Stellar keypairs to run this service:

- **Anchor signing key** — only used to sign SEP-10 challenge transactions. It doesn't hold any funds and doesn't need a minimum balance beyond account activation.
- **Distribution key** — the funded account that actually receives deposits and sends withdrawals. On mainnet it needs XLM for transaction fees and a USDC trustline.

Keeping them separate means a compromised signing key doesn't expose customer funds.

### Store interfaces

Each SEP handler takes a `Store` interface rather than a direct handle to MongoDB. In production `main.go` wires in the real `MongoStore`. In tests, each package defines a lightweight in-memory mock. No mocking library, no build tags, no test database required.

### MongoDB collections

The service writes to a dedicated `stellar_anchor` database. Main Slimepay collections are untouched.

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
- Two Stellar keypairs — on testnet you can generate and fund them at [laboratory.stellar.org](https://laboratory.stellar.org/#account-creator?network=test)

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

USDC_ISSUER=                  # Leave blank — defaults to the canonical issuer per network
HORIZON_URL=                  # Leave blank — defaults to the canonical Horizon per network

HOME_DOMAIN=slimepay.com
WEB_AUTH_DOMAIN=anchor.slimepay.com

JWT_SECRET=                   # openssl rand -hex 32

DATABASE_URL=mongodb://localhost:27017

CORE_API_URL=http://localhost:8080
CORE_API_SECRET=
```

The service will panic on startup if any required variable is missing. That's intentional — a half-configured anchor silently doing the wrong thing is harder to debug than one that refuses to start.

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

Quick sanity check — fetch the discovery file:

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
| `sep10` | Challenge structure (op sources, nonce, timebounds), full sign→verify round trip, unsigned rejection, wrong keypair rejection |
| `sep12` | GET with no record, GET with existing record, PUT validation, idempotent update, DELETE, DB errors |
| `sep24` | Deposit/withdrawal initiation, unsupported asset rejection, transaction scoped to account, store errors |
| `sep6` | Deposit with memo uniqueness across concurrent calls, withdrawal dest validation, account-scoped transaction lookup |
| `sep31` | Receiver field validation, USDC-only enforcement, memo ≤32 chars, cross-border transaction creation and retrieval |
| `toml` | CORS wildcard, correct content-type, all SEP endpoint keys present, both USDC and XLM declared, mainnet config |

75 tests total, all green.

---

## API reference

Endpoints marked **[auth]** require `Authorization: Bearer <sep10-jwt>`.

### Discovery

```
GET /.well-known/stellar.toml
```

### SEP-10 — Authentication

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

### SEP-12 — KYC

```
GET    /sep12/customer   [auth]
PUT    /sep12/customer   [auth]
DELETE /sep12/customer   [auth]
```

`PUT` requires `first_name`, `last_name`, `email`. Optional: `id_type`, `id_number`.

### SEP-24 — Interactive Transfers

```
GET  /sep24/info
POST /sep24/transactions/deposit/interactive   [auth]   # body: asset_code=USDC (form-encoded)
POST /sep24/transactions/withdraw/interactive  [auth]
GET  /sep24/transaction?id=<tx-id>             [auth]
```

### SEP-6 — Programmatic Transfers

```
GET  /sep6/info
GET  /sep6/deposit?asset_code=USDC             [auth]
GET  /sep6/withdraw?asset_code=USDC&dest=...   [auth]   # dest = bank account number
GET  /sep6/transaction?id=<tx-id>              [auth]
```

Deposit returns the distribution account address and a unique `DEP`-prefixed memo. The customer must include that memo when sending on-chain so the transfer can be matched to their record.

### SEP-31 — Cross-Border Payments

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

Response includes the anchor's `stellar_account_id` (distribution key) and a `stellar_memo` the sending institution must attach to their Stellar payment.
