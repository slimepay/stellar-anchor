# Testnet Runbook

How the anchor is deployed on testnet, and how to check it against the Tranche 1 completion criteria.

## Live services

The anchor runs on Railway, in the `slimepay` project, `production` environment.

| Railway service | What it is | Public URL | Port |
|---|---|---|---|
| `anchor-platform` | SAP 5.0.0 (`stellar/anchor-platform:5.0.0`): SEP server, platform server, payment observer | `https://anchor.slimepay.com` | 8080 |
| `stellar-anchor` | Business Server (this repo) | `https://stellar-anchor-production.up.railway.app` | 8081 |
| `Postgres` | SAP's database (schema `anchor_platform`) | private | |
| `server` | Slimepay core, which pays out through the SafeHaven sandbox | `https://server.slimepay.com` | |
| `client` | Slimepay web app, which proxies `/.well-known/stellar.toml` | `https://slimepay.com` | |

The services talk to each other over Railway's private network:

- SAP to Business Server: `http://stellar-anchor.railway.internal:8081` (callbacks and stellar.toml)
- Business Server to SAP: `http://anchor-platform.railway.internal:8085` (Platform API)
- Business Server to core: `https://server.slimepay.com` (anchor payout API, shared secret)

The private hostnames come from the service names, so `anchor-platform` and `stellar-anchor` must keep those exact names.

## 1. Keys and accounts

Use keys made for testnet only, never keys from any other environment.

1. Create two keypairs at [Stellar Lab](https://lab.stellar.org/account/create?$=network$id=testnet) and fund both with Friendbot:
   - SEP-10 signing key: the secret goes in `SECRET_SEP10_SIGNING_SEED` on `anchor-platform`, the public key in `SEP10_SIGNING_KEY` on `stellar-anchor`.
   - Distribution account: only the public key is used, in `DISTRIBUTION_ACCOUNT` and in `ASSETS_VALUE`.
2. Add a USDC trustline to the distribution account (issuer `GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5`).
3. Generate the shared secrets with `openssl rand -hex 32` and the encryption key with `openssl rand -base64 32`.

Never paste secrets into chat or commit them. They live only in Railway variables.

## 2. stellar-anchor (Business Server)

Variables, from `.env.example`:

```
PORT=8081
STELLAR_NETWORK=testnet
SEP10_SIGNING_KEY=<signing public key>
DISTRIBUTION_ACCOUNT=<distribution public key>
HOME_DOMAIN=slimepay.com
WEB_AUTH_DOMAIN=anchor.slimepay.com
PLATFORM_API_URL=http://anchor-platform.railway.internal:8085
PLATFORM_API_SECRET=<shared with SAP>
CALLBACK_API_SECRET=<shared with SAP>
SEP24_INTERACTIVE_JWT_SECRET=<shared with SAP>
DATABASE_URL=<MongoDB>
DATA_ENCRYPTION_KEY=<base64, 32 bytes>
DOJAH_BASE_URL=https://sandbox.dojah.io
DOJAH_APP_ID=<Dojah sandbox>
DOJAH_SECRET_KEY=<Dojah sandbox>
CORE_API_URL=https://server.slimepay.com
CORE_API_SECRET=<same as ANCHOR_API_SECRET on core>
NGN_PER_USDC=1500
NGN_PER_XLM=250
```

Its public domain must point at port **8081**. A healthy start logs `business server ready`.

## 3. anchor-platform (SAP)

1. Create it from the Docker image `stellar/anchor-platform:5.0.0` and name it `anchor-platform`.
2. Custom start command:
   ```
   /bin/bash /app/start.sh --sep-server --platform-server --stellar-observer
   ```
3. Public domain `anchor.slimepay.com` on port **8080**, with the CNAME Railway gives added to the slimepay.com DNS.
4. Variables: the `platform` block of `deploy/docker-compose.yml`, with these Railway specific values:

```
SECRET_DATA_USERNAME=${{Postgres.PGUSER}}
SECRET_DATA_PASSWORD=${{Postgres.PGPASSWORD}}
DATA_SERVER=${{Postgres.PGHOST}}:${{Postgres.PGPORT}}
DATA_DATABASE=${{Postgres.PGDATABASE}}
CALLBACK_API_BASE_URL=http://stellar-anchor.railway.internal:8081
PLATFORM_API_BASE_URL=http://anchor-platform.railway.internal:8085
SEP1_TOML_VALUE=http://stellar-anchor.railway.internal:8081/.well-known/stellar.toml
SEP24_INTERACTIVE_URL_BASE_URL=https://stellar-anchor-production.up.railway.app/sep24/interactive
ASSETS_TYPE=json
ASSETS_VALUE=<one line of JSON, see below>
```

`ASSETS_VALUE` is the asset list from `deploy/docker-compose.yml` written as one line of JSON. Add it with **New Variable** rather than the Raw Editor, which can break long JSON. SAP 5.0 is strict about it:

- `min_amount` and `max_amount` must be whole numbers.
- Every asset needs `"sep31":{"enabled":false},"sep38":{"enabled":false}`, including `iso4217:NGN`, or SAP refuses to start.

A healthy start ends with Tomcat listening on 8080. `Cannot load driver class: org.h2.Driver` means the `DATA_*` variables are missing.

## 4. Slimepay core and web app

On `server`:

```
ANCHOR_API_SECRET=<same as CORE_API_SECRET>
ANCHOR_SAFEHAVEN_BASE_URL=https://api.sandbox.safehavenmfb.com
ANCHOR_SAFEHAVEN_CLIENT_ID=<SafeHaven sandbox>
ANCHOR_SAFEHAVEN_CLIENT_ASSERTION=<SafeHaven sandbox>
ANCHOR_SAFEHAVEN_ACCOUNT_NUMBER=<SafeHaven sandbox account>
STELLAR_ANCHOR_URL=https://anchor.slimepay.com
```

On `client`: `STELLAR_ANCHOR_URL=https://anchor.slimepay.com`.

The anchor payout API answers 503 until every SafeHaven value is set, and refuses SafeHaven's live API. Until then the bank list on the withdrawal page is empty.

## 5. Checks

```bash
curl https://slimepay.com/.well-known/stellar.toml
curl https://anchor.slimepay.com/sep24/info
curl https://stellar-anchor-production.up.railway.app/healthz
curl "https://anchor.slimepay.com/auth?account=<any funded G address>"
```

Wallets must send the SEP-24 withdraw request as JSON or multipart form data. SAP 5.0 rejects URL encoded forms.

## 6. Anchor Validation Suite

```bash
npx -y @stellar/anchor-tests --home-domain https://slimepay.com --seps 1 10 12 6 24 --sep-config sep-config.json
```

`sep-config.json` gives the suite SEP-12 customers. Dojah's sandbox BVN `22222222222` passes KYC:

```json
{
  "12": {
    "customers": {
      "toBeCreated": { "first_name": "John", "last_name": "Doe", "email_address": "john@example.com", "birth_date": "1990-01-01", "id_type": "bvn", "id_number": "22222222222", "bank_account_number": "0123456789", "bank_number": "000013" },
      "sendingClient": { "first_name": "John", "last_name": "Doe", "email_address": "john@example.com", "birth_date": "1990-01-01", "id_type": "bvn", "id_number": "22222222222", "bank_account_number": "0123456789", "bank_number": "000013" },
      "receivingClient": { "first_name": "Jane", "last_name": "Doe", "email_address": "jane@example.com", "birth_date": "1990-01-01", "id_type": "bvn", "id_number": "22222222222", "bank_account_number": "0123456789", "bank_number": "000013" },
      "toBeDeleted": { "first_name": "Jim", "last_name": "Doe", "email_address": "jim@example.com", "birth_date": "1990-01-01", "id_type": "bvn", "id_number": "22222222222", "bank_account_number": "0123456789", "bank_number": "000013" }
    },
    "createCustomer": "toBeCreated",
    "deleteCustomer": "toBeDeleted",
    "sameAccountDifferentMemos": ["sendingClient", "receivingClient"]
  },
  "6": {
    "deposit": { "transactionFields": {} },
    "withdraw": { "types": { "bank_account": { "transactionFields": {} } } }
  }
}
```

Save the report as evidence.

## 7. End to end withdrawal

1. In a testnet wallet that supports SEP-24 (for example the [Stellar demo wallet](https://demo-wallet.stellar.org)), add `USDC:GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5` with home domain `slimepay.com`.
2. Fund the wallet with testnet USDC and start a SEP-24 withdrawal.
3. On the Slimepay page, fill in the Dojah sandbox identity and a SafeHaven sandbox bank account.
4. Let the wallet send the payment.
5. Within a few seconds the `stellar-anchor` log shows the payout, SAP marks the transaction `completed`, and the transfer appears on the SafeHaven sandbox dashboard.
6. After the next reconciliation run, the withdrawal's `reconcile` field in MongoDB is `ok`.

Keep the Stellar transaction hash, the SAP transaction ID and the SafeHaven reference as evidence.

## Running it on one machine instead

`deploy/` holds the same stack for a single VM with Docker Compose, with Caddy in front:

```bash
cd deploy
cp .env.example .env
docker compose up -d --build
```

## When something is on hold

An alert names the transaction and the reason. Check the payment on [stellar.expert](https://stellar.expert/explorer/testnet) and the payout on the SafeHaven dashboard, then resolve it in SAP: complete it, or refund the user.
