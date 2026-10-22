# Tranche 1 Updates

Highlights of the changes made at the start of SCF #45 Tranche 1. They line the repo up with the plan in the [ARCHITECTURE docs](ARCHITECTURE.md): the Stellar Anchor Platform (SAP) serves the SEP endpoints and this service becomes the Business Server behind it.

## Implementation path

- Committed to SAP for all standard SEP functionality, with this repo as the Business Server (KYC through Dojah, naira payouts through SafeHaven, reconciliation).
- Added `ARCHITECTURE.md` covering components, the SEP-24 withdrawal flow, reconciliation, infrastructure, owners and scope.

## Withdrawals only

- A user sends USDC or XLM and receives naira, which is a withdrawal. Deposit routes were removed from SEP-24 and SEP-6.
- `info` endpoints now list withdrawals only, without hardcoded fees. Fees will come from SEP-38 quotes.

## Memos match SAP

- Memos are now random 64 bit memo IDs (`memo_type` `id`), the same format SAP assigns.
- Replaces the old `DEP`/`WD` text memos and an invalid SEP-31 hash memo.
- Memos are stored on the transaction so incoming payments can be matched.

## SEP-10 authentication

- Challenges use `<HOME_DOMAIN> auth` and sequence 0, so wallet SDKs accept them.
- Expired challenges are rejected (15 minute window).
- Signatures are checked against the account's current signers and medium threshold on Horizon. Unfunded accounts must use the master key.
- A signed challenge can only be used once.

## stellar.toml

- SEP endpoints now point at `WEB_AUTH_DOMAIN`, matching the `web_auth_domain` in SEP-10 challenges.
- Assets are marked `test` on testnet.
