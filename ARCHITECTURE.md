# Slimepay Anchor Architecture

This document describes how Slimepay runs its Stellar anchor and how this repository fits into it. It is the reference the SCF tranches are measured against.

## Scope

- **Testnet only.** All SCF tranches are delivered on Stellar testnet. No real funds move. Any mainnet launch is self funded and outside SCF scope.
- **Withdrawals only.** A user sends USDC or XLM on Stellar and receives naira in a Nigerian bank account. In SEP terms that is a withdrawal. Slimepay does not offer deposits (naira in, Stellar asset out).
- **Flows:** SEP-24 (interactive withdrawal from a wallet), SEP-6 (programmatic withdrawal), SEP-31 (B2B: a partner sends USDC, the recipient gets naira), with SEP-10 for auth, SEP-12 for KYC and SEP-38 for firm quotes.

## Implementation path: Stellar Anchor Platform

Slimepay uses the [Stellar Anchor Platform](https://github.com/stellar/anchor-platform) (SAP) for everything that is standard across anchors, and keeps its own code for what is specific to Slimepay.

| Concern | Owned by |
|---|---|
| SEP-1, 10, 12, 24, 6, 31, 38 endpoints | SAP SEP server |
| Transaction records and status machine | SAP platform server (Postgres) |
| Watching the distribution account for incoming payments | SAP payment observer |
| Memo assignment for incoming payments | SAP (`deposit_info_generator_type: self`, random 64 bit memo IDs) |
| KYC data and checks | Business Server (this repo), calling Dojah |
| Quotes and fees | Business Server, answering SAP's rate callback |
| Interactive withdrawal UI | Slimepay web app, opened from SAP's `interactive_url` |
| Naira payout | Slimepay core backend, through SafeHaven |
| Reconciliation | Business Server |

### Components

```
 Wallet (Freighter, Lobstr, ...)
   |
   |  SEP-10 / 12 / 24 / 6 / 31 / 38
   v
 SAP SEP server ----- SAP platform server ---- Postgres
   |                     ^      |
   | callbacks           |      | Platform API (JSON-RPC)
   | (/customer, /rate)  |      |
   v                     |      v
 Business Server (this repo, Go)
   |            |
   | Dojah      | core API
   v            v
 KYC         Slimepay core backend --> SafeHaven (NGN payout)

 SAP payment observer --> watches the distribution account on Horizon
```

### SEP-24 withdrawal, end to end

1. The wallet authenticates with SEP-10 against SAP.
2. The wallet starts `POST /sep24/transactions/withdraw/interactive`. SAP creates the transaction and returns the interactive URL.
3. The user completes the Slimepay interactive page: bank account, KYC (SEP-12 through the Business Server and Dojah) and a firm quote (SEP-38 through the Business Server).
4. The Business Server calls the Platform API (`request_onchain_funds`) with the amounts. SAP assigns the memo and moves the transaction to `pending_user_transfer_start`.
5. The wallet sends USDC or XLM to the distribution account with that memo.
6. The SAP payment observer sees the payment, matches it by memo and moves the transaction to `pending_anchor`.
7. The Business Server picks up the transaction, asks the core backend to pay out through SafeHaven, then reports progress with `notify_offchain_funds_pending` and `notify_offchain_funds_sent`, which completes the transaction. Failures go through `notify_transaction_error`.

SEP-6 follows the same steps without the interactive page. SEP-31 is the same from step 5 onward, with the partner as the sender.

### Reconciliation

A scheduled job in the Business Server compares three sources for every transaction in a terminal state:

- the SAP transaction (amounts, memo, status),
- the on-chain payment to the distribution account (Horizon),
- the SafeHaven payout record.

Any mismatch (payment with no transaction, payout with no payment, amount differences) raises an alert and puts the transaction on hold (`notify_transaction_on_hold`) until someone reviews it.

### Infrastructure

Sized for a five person team at current volume:

- One VM running Docker Compose: SAP SEP server, platform server and payment observer, the Business Server, and Postgres.
- SAP events stay disabled at first. The Business Server polls the Platform API for transactions that need action. A queue (Kafka or SQS) is only added if polling stops being enough.
- Logs and alerts go to the existing Slimepay monitoring. No separate observability stack.

## Ownership

| Area | Owner |
|---|---|
| SAP deployment and configuration, Business Server, reconciliation | Muhammad Jamil (Lead Blockchain Engineer) |
| Interactive withdrawal UI, Stellar Wallets Kit, merchant checkout | Tammy Smith (Product Engineer) |
| Transaction monitoring and fraud alerts on reconciliation output | Christian Divinefavour (Data Scientist) |

## Current state of this repository

This repo started as a hand written Go SEP server. It is the testnet prototype and the starting point for the Business Server.

Already aligned with the target design:

- SEP-10 verification follows the spec (time bounds, home domain, account signers and thresholds, single use challenges).
- SEP-24 and SEP-6 offer withdrawals only.
- Memos are random 64 bit memo IDs (`memo_type` `id`), the same format SAP assigns.
- `stellar.toml` points the SEP endpoints at the anchor host and marks assets as `test` on testnet.

Not built yet (Tranche 1 and 2 work):

- SAP deployment. Once SAP serves the SEP endpoints, the handlers in `internal/sep*` are retired and this service implements SAP's callback API instead.
- Payment observing, payout execution and Dojah KYC. Today the SEP handlers only create transaction records.
- The interactive withdrawal page.
- Reconciliation.

## Out of scope

- **Mainnet.** Self funded, after the SCF tranches.
- **Stellar Disbursement Platform.** Dropped. Payouts go to bank accounts through SafeHaven, so there is no recipient wallet and no reason for an extra on-chain hop.
- **Deposits** (naira to Stellar assets).
