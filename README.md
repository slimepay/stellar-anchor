# Stellar Anchor Service

A standalone Go service that implements the Stellar Ecosystem Proposals (SEPs) required to operate a regulated fiat-to-Stellar anchor. It runs independently alongside the main Slimepay backend and bridges customer accounts to the Stellar network — handling wallet authentication, KYC, deposits, withdrawals, and cross-border B2B payments.

This service targets **SEP-10**, **SEP-12**, **SEP-24**, **SEP-6**, and **SEP-31**.

---

## Architecture

```
stellar-anchor/
├── cmd/
│   └── server/
│       └── main.go          # Entry point — wires config, DB, and all routes
├── internal/
│   ├── config/
│   │   └── config.go        # Env-based config with fail-fast validation
│   ├── db/
│   │   └── mongo.go         # MongoDB connection with ping-on-startup
│   ├── middleware/
│   │   └── jwt.go           # SEP-10 JWT validation middleware
│   ├── toml/
│   │   └── handler.go       # /.well-known/stellar.toml discovery endpoint
│   ├── sep10/
│   │   └── handler.go       # Web Authentication (challenge + verify)
│   ├── sep12/
│   │   ├── store.go         # Store interface + MongoStore implementation
│   │   └── handler.go       # KYC customer endpoints
│   ├── sep24/
│   │   ├── store.go
│   │   └── handler.go       # Interactive deposit/withdrawal initiation
│   ├── sep6/
│   │   ├── store.go
│   │   └── handler.go       # Programmatic deposit/withdrawal
│   └── sep31/
│       ├── store.go
│       └── handler.go       # Cross-border B2B payment transactions
├── .env.example
├── go.mod
└── go.sum
```

### How it fits together

Every request that carries a Stellar account identity goes through the same auth gate. A wallet first calls `GET /sep10/auth` to receive a challenge transaction, signs it locally with the customer's keypair, then posts it back. The service verifies both the anchor's original signature and the customer's signature before issuing a short-lived HS256 JWT. Every subsequent endpoint — KYC, deposits, withdrawals — requires that JWT in the `Authorization` header.

The MongoDB database (`stellar_anchor`) is separate from the main Slimepay database but uses the same connection URI. Each SEP gets its own collection:

| Collection | Used by |
|---|---|
| `sep12_customers` | SEP-12 KYC records |
| `sep24_transactions` | SEP-24 interactive flows |
| `sep6_transactions` | SEP-6 programmatic flows |
| `sep31_transactions` | SEP-31 cross-border payments |

Each SEP handler depends on a `Store` interface rather than a concrete MongoDB type. This keeps the handlers testable without a running database — tests use lightweight in-memory mock stores.

### SEP summary

| SEP | What it does |
|---|---|
| SEP-10 | Proves a customer owns a Stellar keypair via a signed challenge transaction |
| SEP-12 | Collects and stores KYC information (name, email, ID documents) |
| SEP-24 | Interactive deposit and withdrawal — redirects customers to a hosted flow |
| SEP-6 | Programmatic deposit and withdrawal — no browser redirect required |
| SEP-31 | Direct payment between two businesses on Stellar (cross-border B2B) |

---

## Prerequisites

- Go 1.21+
- MongoDB 5.0+ (running locally or a remote URI)
- Two funded Stellar keypairs — one for signing challenges, one as the distribution account

To generate keypairs for testnet, use the [Stellar Laboratory](https://laboratory.stellar.org/#account-creator?network=test) or the Stellar CLI. Fund the distribution account on testnet using Friendbot before starting the service.

---

## Configuration

Copy `.env.example` to `.env` and fill in all required values:

```bash
cp .env.example .env
```

```env
# "testnet" or "mainnet"
STELLAR_ENV=testnet

PORT=8081

# Anchor signing keypair — used only to sign SEP-10 challenge transactions
ANCHOR_SIGNING_SEED=SXXX...

# Distribution keypair — the funded account that receives and sends funds
DISTRIBUTION_SEED=SXXX...

# Leave blank to use the canonical issuer for the chosen network
USDC_ISSUER=

# Leave blank to use the canonical Horizon URL for the chosen network
HORIZON_URL=

HOME_DOMAIN=slimepay.com
WEB_AUTH_DOMAIN=anchor.slimepay.com

# Generate with: openssl rand -hex 32
JWT_SECRET=

DATABASE_URL=mongodb://localhost:27017

CORE_API_URL=http://localhost:8080
CORE_API_SECRET=
```

The service panics at startup if any required variable is missing. That is intentional — a misconfigured anchor is worse than one that refuses to start.

---

## Running the service

```bash
# Install dependencies
go mod download

# Run (reads .env from the working directory)
go run ./cmd/server
```

The service binds to `:8081` by default. On startup it prints the derived public keys for both keypairs — confirm they match what you expect before sending any traffic.

```
INF stellar-anchor starting env=testnet signing_key=GABC... distribution_key=GDEF...
INF connected to MongoDB
INF stellar-anchor listening addr=:8081
```

To build a binary:

```bash
go build -o stellar-anchor ./cmd/server
./stellar-anchor
```

---

## Running tests

Tests require no running database or Stellar network connection. All SEP handlers use in-memory mock stores, and SEP-10 tests use real cryptographic operations with ephemeral keypairs generated at test time.

```bash
# Run all tests
go test ./...

# With verbose output
go test ./... -v

# A single package
go test ./internal/sep10/... -v
go test ./internal/sep24/... -v
```

Test coverage by package:

| Package | Tests |
|---|---|
| `middleware` | JWT validation — missing header, malformed, wrong secret, expired, valid |
| `sep10` | Challenge structure, client signing, wrong key rejection, unsigned submission |
| `sep12` | KYC get/put/delete, missing fields, DB errors, idempotent updates |
| `sep24` | Deposit and withdrawal initiation, unsupported assets, transaction lookup |
| `sep6` | Deposit/withdrawal with memo uniqueness, programmatic transaction lookup |
| `sep31` | Cross-border transaction creation, receiver field validation, status lookup |
| `toml` | CORS headers, content-type, all SEP endpoints declared, USDC/XLM currencies |

---

## API endpoints

All endpoints accept and return `application/json` unless noted. Endpoints marked **[auth]** require a `Authorization: Bearer <sep10-jwt>` header.

### Discovery

```
GET  /.well-known/stellar.toml
```

Returns the anchor's TOML configuration. Stellar wallets fetch this first to discover all SEP endpoint URLs, supported currencies, and the anchor's signing key.

### SEP-10 — Web Authentication

```
GET  /sep10/auth?account=G...     # Returns a challenge transaction
POST /sep10/auth                  # Submit signed transaction, receive JWT
```

### SEP-12 — KYC

```
GET    /sep12/customer   [auth]   # Fetch KYC status for the authenticated account
PUT    /sep12/customer   [auth]   # Submit or update KYC information
DELETE /sep12/customer   [auth]   # Delete KYC record
```

Required fields for `PUT`: `first_name`, `last_name`, `email`. Optional: `id_type`, `id_number`.

### SEP-24 — Interactive Transfers

```
GET  /sep24/info
POST /sep24/transactions/deposit/interactive   [auth]
POST /sep24/transactions/withdraw/interactive  [auth]
GET  /sep24/transaction?id=<tx-id>             [auth]
```

### SEP-6 — Programmatic Transfers

```
GET  /sep6/info
GET  /sep6/deposit?asset_code=USDC             [auth]
GET  /sep6/withdraw?asset_code=USDC&dest=...   [auth]
GET  /sep6/transaction?id=<tx-id>              [auth]
```

Deposit returns the distribution account address and a unique text memo. The customer must include that memo when sending funds on-chain so the transfer can be matched to their record.

### SEP-31 — Cross-Border Payments

```
GET  /sep31/info
POST /sep31/transactions           [auth]
GET  /sep31/transactions/{id}      [auth]
```

Required body fields for `POST`: `asset_code` (must be `USDC`), `amount`, `receiver_account_number`, `receiver_bank_code`, `receiver_name`.

---

## Key design decisions

**Store interface pattern** — each SEP handler depends on a small interface (`Insert`, `FindByID`, etc.) rather than a direct MongoDB collection. The production path wires in `MongoStore`; tests wire in a simple in-memory struct. No mocking library needed, no build tags, no test database required.

**Fail-fast config** — required environment variables cause a panic at startup rather than producing silent failures at request time. The alternative — logging a warning and continuing — tends to surface as confusing runtime errors that are harder to diagnose.

**Two keypairs** — the signing key and distribution key are kept separate. The signing key signs SEP-10 challenge transactions and never touches on-chain funds. The distribution key holds the actual USDC and XLM balance. Compromising the signing key does not expose funds.

**JWT scope** — the SEP-10 JWT carries only the customer's Stellar address (`sub` claim). All store lookups are scoped to that address, so one authenticated user cannot read or modify another user's records.
