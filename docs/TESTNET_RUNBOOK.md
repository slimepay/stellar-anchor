# Testnet Runbook

Steps to deploy the anchor on testnet and check it against the Tranche 1 completion criteria.

## 1. Keys and accounts

Make new testnet keys. Never reuse keys from any other environment.

1. Create two keypairs at [Stellar Lab](https://lab.stellar.org/account/create?$=network$id=testnet) and fund both with Friendbot:
   - SEP-10 signing key: the seed goes in `SEP10_SIGNING_SEED`, the public key in `SEP10_SIGNING_KEY`.
   - Distribution account: only the public key is needed, in `DISTRIBUTION_ACCOUNT`.
2. Add a trustline on the distribution account for testnet USDC (`GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5`).
3. Generate the shared secrets with `openssl rand -hex 32` and the encryption key with `openssl rand -base64 32`.

## 2. Slimepay core

Set these on the core backend and redeploy it:

```
ANCHOR_API_SECRET=                  same value as CORE_API_SECRET on the anchor
ANCHOR_SAFEHAVEN_BASE_URL=          SafeHaven sandbox API URL
ANCHOR_SAFEHAVEN_CLIENT_ID=         sandbox credentials
ANCHOR_SAFEHAVEN_CLIENT_ASSERTION=
ANCHOR_SAFEHAVEN_ACCOUNT_NUMBER=    sandbox debit account
STELLAR_ANCHOR_URL=https://anchor.slimepay.com
```

Set `STELLAR_ANCHOR_URL` on the web app too, so `https://slimepay.com/.well-known/stellar.toml` is served.

The anchor payout API answers 503 until all of the SafeHaven values are set, and refuses SafeHaven's live API.

## 3. Deploy the anchor

On the VM, with DNS for `anchor.slimepay.com` pointing at it:

```bash
cd deploy
cp .env.example .env    # fill in every value
docker compose up -d --build
docker compose logs -f business platform
```

Checks:

```bash
curl https://anchor.slimepay.com/healthz
curl https://anchor.slimepay.com/.well-known/stellar.toml
curl https://slimepay.com/.well-known/stellar.toml
curl "https://anchor.slimepay.com/auth?account=<any G address>"
```

The admin "Stellar anchor" status in core should show both checks green.

## 4. Anchor Validation Suite

Run the SDF suite against the home domain, either at [anchor-tests.stellar.org](https://anchor-tests.stellar.org) or from the CLI:

```bash
npx -y @stellar/anchor-tests --home-domain https://slimepay.com --seps 1 10 12 6 24 --sep-config sep-config.json
```

`sep-config.json` gives the suite SEP-12 customer data. Use Dojah's sandbox test BVN so KYC passes:

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
    "deposit": {},
    "withdraw": { "types": { "bank_account": { "transactionFields": {} } } }
  }
}
```

Save the report as evidence.

## 5. End to end withdrawal

1. In a testnet wallet that supports SEP-24 (for example the [Stellar demo wallet](https://demo-wallet.stellar.org)), add the asset `USDC:GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5` with home domain `slimepay.com`.
2. Fund the wallet account with testnet USDC and start a SEP-24 withdrawal.
3. On the Slimepay page, fill in the Dojah sandbox identity and a SafeHaven sandbox bank account.
4. Let the wallet send the payment.
5. Within a few seconds the Business Server log shows the payout. SAP marks the transaction `completed`, and the transfer appears on the SafeHaven sandbox dashboard.
6. After the next reconciliation run, the withdrawal's `reconcile` field in MongoDB is `ok`.

Keep the Stellar transaction hash, the SAP transaction ID and the SafeHaven reference as evidence.

## When something is on hold

An alert names the transaction and the reason. Check the payment on [stellar.expert](https://stellar.expert/explorer/testnet) and the payout on the SafeHaven dashboard, then resolve it in SAP: complete it, or refund the user.
