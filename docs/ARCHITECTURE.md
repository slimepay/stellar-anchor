# Slimepay Anchor Architecture

This document describes how Slimepay runs its Stellar anchor and how this repository fits into it. It is the reference the SCF tranches are measured against.

## Scope

- **Withdrawals only.** A user sends USDC or XLM on Stellar and receives naira in a Nigerian bank account. In SEP terms that is a withdrawal. Slimepay does not offer deposits (naira in, Stellar asset out).
- **Flows:** SEP-24 (interactive withdrawal from a wallet) and SEP-6 (programmatic withdrawal), with SEP-10 for auth and SEP-12 for KYC, in Tranche 1. SEP-31 (B2B: a partner sends USDC, the recipient gets naira) and SEP-38 firm quotes follow in Tranche 2.

## Implementation path: Stellar Anchor Platform

Slimepay uses the [Stellar Anchor Platform](https://github.com/stellar/anchor-platform) (SAP) for everything that is standard across anchors, and keeps its own code for what is specific to Slimepay.

| Concern | Owned by |
|---|---|
| SEP-1, 10, 12, 24 and 6 endpoints (31 and 38 in Tranche 2) | SAP SEP server |
| Transaction records and status machine | SAP platform server (Postgres) |
| Customers (encrypted), withdrawal ledger, reconciliation results, audit log | Business Server (MongoDB) |
| Watching the distribution account for incoming payments | SAP payment observer |
| Memo assignment for incoming payments | SAP (random 64 bit ID memos, one per transaction) |
| KYC data and checks | Business Server (this repo). Mainnet: Slimepay's production Dojah integration. Testnet: the Dojah verification is simulated |
| Quotes and fees | Business Server. Tranche 1 uses a configured naira rate with no fee; SEP-38 firm quotes come in Tranche 2 |
| Interactive withdrawal UI | Business Server page at `/sep24/interactive` (Tranche 1); the full UI with quote acceptance is Tranche 2 |
| Naira payout | Slimepay core backend. Mainnet: SafeHaven, Slimepay's production payout provider. Testnet: the SafeHaven payout is simulated |
| Reconciliation | Business Server |

### Components

```
 Stellar wallet
   |
   |  SEP-10 / 12 / 24 / 6  (SEP-31 and SEP-38 in Tranche 2)
   v
 SAP SEP server ----- SAP platform server ---- Postgres
   |                     ^      |
   | callbacks           |      | Platform API (JSON-RPC)
   | (/customer;         |      |
   |  /rate in Tranche 2)|      |
   v                     |      v
 Business Server (this repo, Go) ---- MongoDB
   |            |
   | Dojah      | core API
   v            v
 KYC         Slimepay core backend --> SafeHaven (NGN payout; simulated on testnet)

 SAP payment observer --> watches the distribution account on Horizon
```

### SEP-24 withdrawal, end to end

1. The wallet authenticates with SEP-10 against SAP.
2. The wallet starts `POST /sep24/transactions/withdraw/interactive`. SAP creates the transaction and returns the interactive URL.
3. The user completes the Slimepay interactive page in two steps. Identity comes first and on its own: the Business Server verifies it as soon as the identity fields are saved, and a verified user skips this step on later withdrawals. Then a short withdrawal form takes the amount and bank account, with a naira quote from the configured rate. Tranche 2 replaces the rate with SEP-38 firm quotes.
4. The Business Server calls the Platform API (`request_onchain_funds`) with the amounts. SAP assigns the memo and moves the transaction to `pending_user_transfer_start`.
5. The wallet sends USDC or XLM to the distribution account with that memo.
6. The SAP payment observer sees the payment, matches it by memo and moves the transaction to `pending_anchor`.
7. The Business Server picks up the transaction, asks the core backend to pay out through SafeHaven, then reports progress with `notify_offchain_funds_pending` and `notify_offchain_funds_sent`, which completes the transaction. Failures go through `notify_transaction_error`.

SEP-6 follows the same steps without the interactive page. SEP-31 is the same from step 5 onward, with the partner as the sender.

### Reconciliation

Reconciliation runs at two points.

Before paying out, the processor checks the payment SAP matched against Horizon: the transaction exists and succeeded, the memo is the one SAP assigned, and the amount of the right asset paid to the distribution account equals the quoted amount. Any difference puts the transaction on hold (`notify_transaction_on_hold`) with an alert, and nothing is paid. A payout whose outcome SafeHaven did not confirm is held the same way.

After completion, a scheduled job compares three sources for every completed withdrawal:

- the SAP transaction (amounts, memo, external transaction ID),
- the on-chain payment to the distribution account (Horizon),
- the payout record in core (status, amount, reference).

A mismatch is recorded on the withdrawal and alerted once. The same job walks every incoming payment to the distribution account and alerts on any whose memo belongs to no withdrawal.

### Infrastructure

Sized for a five person team at current volume:

- Railway, next to the existing Slimepay services: SAP (SEP server, platform server and payment observer) at `anchor.slimepay.com`, Postgres for SAP, the Business Server, and MongoDB for the Business Server. They talk over Railway's private network. Only the SEP server and the Business Server's interactive page are public; the Platform API stays private and the callback API requires SAP's token.
- `deploy/` holds the same stack as a Docker Compose file for a single VM, with Caddy for TLS.
- SAP events stay disabled at first. The Business Server polls the Platform API for transactions that need action. A queue (Kafka or SQS) is only added if polling stops being enough.
- Logs and alerts go to the existing Slimepay monitoring. No separate observability stack.

### Testnet and mainnet

The SCF tranches run on testnet, where no real money or identities are used. Two steps are simulated there: the Dojah verification (the Business Server accepts the customer without calling Dojah) and the SafeHaven payout (core resolves the account and records the payout as sent without moving money). On mainnet the same code calls Slimepay's production Dojah and SafeHaven integrations. Core refuses any payout whose network differs from its own, so testnet withdrawals cannot reach the production payout path.

## Regulatory position (VASP)

Slimepay operates through Slimehaven Limited (CAC RC: 8164075). Nigeria regulates virtual asset service providers, and an anchor that moves customer funds between Stellar assets and Nigerian bank accounts is likely to fall under that regime when it goes live. Slimepay's existing production service already runs identity checks through Dojah and payouts through SafeHaven.

- **Testnet (SCF scope).** No real funds or real customer identity data are used.
- **Before mainnet (self funded).** Slimepay will assess its VASP registration requirements with the SEC and the CBN and complete any required filings before any mainnet launch, including before listing as an anchor.
- **Owners.** Lamine-cisse Ekezie (Founder and CEO) owns regulatory filings and compliance. Muhammad Jamil owns the technical controls: KYC, sanctions screening and the audit log.

## Ownership

| Area | Owner |
|---|---|
| Regulatory filings and compliance (VASP) | Lamine-cisse Ekezie (Founder and CEO) |
| SAP deployment and configuration, Business Server, reconciliation | Muhammad Jamil (Lead Blockchain Engineer) |
| Interactive withdrawal UI, Stellar Wallets Kit, merchant checkout | Tammy Smith (Product Engineer) |
| Transaction monitoring and fraud alerts on reconciliation output | Christian Divinefavour (Data Scientist) |

## Current state of this repository

This repository is the Business Server. The hand written SEP handlers from the prototype have been removed; SAP serves the SEP endpoints.

Built in Tranche 1:

- Customer callback (`GET/PUT/DELETE /customer`) with Dojah BVN or NIN verification and sanctions and PEP screening. Customer data is encrypted at rest.
- SEP-24 withdrawal page that verifies SAP's interactive token, verifies the user's identity once, then takes the amount and bank account and calls `request_onchain_funds`. Tested on testnet with the SDF demo wallet.
- SEP-6 withdrawals started by the processor once KYC has passed.
- Payout processor using the Platform API (`request_onchain_funds`, `notify_offchain_funds_pending`, `notify_offchain_funds_sent`, `notify_transaction_error`, `notify_transaction_on_hold`) and Slimepay core's anchor payout API.
- Reconciliation v1 and alerts, audit log.
- Testnet deployment on Railway (see the [testnet runbook](TESTNET_RUNBOOK.md)), and `stellar.toml` served on the home domain by the Slimepay web app, which proxies SAP.

## Later tranches

- **Tranche 2.** Merchant checkout with Stellar Wallets Kit (Freighter and Lobstr), the B2B settlement API on SEP-31 with partner webhooks, SEP-38 quotes through the rate callback, and the full interactive UI.
- **Tranche 3.** The Stellar Disbursement Platform on testnet, end to end validation across all flows, and the open-source NGN anchor deployment guide with a public testnet metrics report.

The Stellar Disbursement Platform is scheduled for Tranche 3 and is not part of Tranche 1. Its design, including who holds the recipient wallets and how it relates to the bank payout flow, will be added to this document as part of that tranche.

## Out of scope

- **Mainnet.** Self funded, after the SCF tranches.
- **Deposits** (naira to Stellar assets).
