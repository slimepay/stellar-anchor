# Tranche 1 Evidence

Evidence for the SCF #45 Tranche 1 completion criteria. Everything here was produced against the public testnet deployment on 25 October 2026. The raw files are in [evidence/tranche1](evidence/tranche1).

## Links

| What | Where |
|---|---|
| Repository | https://github.com/slimepay/stellar-anchor (this repo, `main`) |
| stellar.toml on the home domain | https://slimepay.com/.well-known/stellar.toml |
| Anchor endpoint (SAP) | https://anchor.slimepay.com |
| SEP-24 info | https://anchor.slimepay.com/sep24/info |
| SEP-6 info | https://anchor.slimepay.com/sep6/info |
| Architecture | [ARCHITECTURE.md](ARCHITECTURE.md) |

The stellar.toml uses the testnet passphrase, points `WEB_AUTH_ENDPOINT`, `KYC_SERVER`, `TRANSFER_SERVER` and `TRANSFER_SERVER_SEP0024` at anchor.slimepay.com, and marks every asset `status="test"`.

## Completion criteria

| Criterion | Evidence |
|---|---|
| SAP running on testnet with a public anchor endpoint | Links above. SAP 5.0.0 on Railway serves SEP-1, 10, 12, 6 and 24. |
| SEP-10, 12, 24 and 6 pass the SDF Anchor Validation Suite | Every test the suite runs against a withdraw-only anchor passes (64). The 39 failures are deposit tests and the tests that depend on them. See below. |
| A testnet SEP-24 withdrawal completes end to end | Two withdrawals completed. See below. |
| Architecture document matches the code | [ARCHITECTURE.md](ARCHITECTURE.md): SAP with Postgres, Business Server with MongoDB, core for payouts. |

## SDF Anchor Validation Suite

```bash
npx -y @stellar/anchor-tests@0.6.22 --home-domain https://slimepay.com --asset-code USDC --seps 1 10 12 6 24 --sep-config sep-config.json
```

Full output: [validation-suite-report.txt](evidence/tranche1/validation-suite-report.txt). Config: [sep-config.json](evidence/tranche1/sep-config.json).

**Result: 64 passed, 39 failed, 6 skipped.**

- Every SEP-1, SEP-10 and SEP-12 test passes, including SEP-10 multi-signer support.
- Every SEP-6 and SEP-24 withdrawal test passes: info, starting a withdrawal, transaction schema, `/transaction` and `/transactions` lookups and 404s.
- All 39 failures come from deposits. Two tests fail directly, with "USDC is not present in the /info response" for deposits on SEP-6 and SEP-24. The other 37 are reported as "failed dependency": the suite only runs them after a deposit test passes, which includes some `/transactions` list tests. Slimepay offers withdrawals only (USDC in, naira out), as approved for this tranche, so `/info` lists no deposit assets, and the suite has no option to test a withdraw-only anchor without them.

The machine that ran the suite had a clock about 2.3 seconds fast ([measured](evidence/tranche1/measure-skew.js) against anchor.slimepay.com). The suite's SEP-10 check compares the challenge's start time to the local clock, so the run used [clock-offset.js](evidence/tranche1/clock-offset.js) to correct Node's clock by 2.6 seconds. The anchor's own clock agrees with Horizon, Google and Cloudflare.

## End to end SEP-24 withdrawals

A new testnet wallet bought USDC on the testnet DEX, then withdrew it through the full SEP-24 flow: SEP-10 login, the interactive page, payment with SAP's memo, payout, completion. Script: [e2e.js](evidence/tranche1/e2e.js). Log of every step: [e2e-log.jsonl](evidence/tranche1/e2e-log.jsonl).

Wallet: `GDRGQ7VUJSLDK4B5BLR7QPLI2YWDACBSRZ33XHIQOP5KESN72JFFEFJK`. Distribution account: `GBHZJYFAUF3QK47NQQAGWAOUOXFMY6DQ2UUX6IJUDPCKDKKMU4FKTWGE`.

| | Withdrawal 1 | Withdrawal 2 |
|---|---|---|
| SAP transaction | `9939a61b-d465-4e62-a900-1624c019b782` | `7c24d9af-2274-41e9-9cf2-1061cd0cf022` |
| Sent | 5 USDC | 3 USDC |
| Paid out | NGN 7,500.00 | NGN 4,500.00 |
| Memo (id) | `645018067554741524` | `3595894831239418260` |
| Stellar payment | [d81c2536…cb3d](https://stellar.expert/explorer/testnet/tx/d81c2536035a41115964ef5e5dc6d5320cc9306fb9b9850d27854a535007cb3d), ledger 5388166 | [7614911c…2822](https://stellar.expert/explorer/testnet/tx/7614911c7b5d00aa094c548404227511772a85462e63eb7d62898c6117d92822), ledger 5388344 |
| Payout reference | `anchor-9939a61b-d465-4e62-a900-1624c019b782` | `anchor-7c24d9af-2274-41e9-9cf2-1061cd0cf022` |
| Final status | `completed` | `completed` |
| SAP record | [withdrawal-1-final-transaction.json](evidence/tranche1/withdrawal-1-final-transaction.json) | [withdrawal-2-final-transaction.json](evidence/tranche1/withdrawal-2-final-transaction.json) |

A third withdrawal was run later as a re-check: SAP transaction `d68b42e5-b9ee-4165-bbd5-a389c89af447`, 1 USDC to NGN 1,500.00, Stellar payment [b71d53ed…fce6](https://stellar.expert/explorer/testnet/tx/b71d53ede715f95147e364333d32c9871f60fe056066ab43bd10f96c6cfbfce6) in ledger 5388483, `completed` 15 seconds after the payment reached the ledger ([record](evidence/tranche1/withdrawal-3-final-transaction.json)).

A fourth withdrawal was recorded for the demo video through the SDF demo wallet, using the two-step page: identity verified first, then the withdrawal form. Wallet `GBDWVFUFR7M4UX75V6QAYTBAKOEOEG5Z4GPXHDXSV4UKMDK7S2SCMTCG`, 10 USDC to NGN 15,000.00, memo `28672842125495417`, Stellar payment [9dec082c…0b11](https://stellar.expert/explorer/testnet/tx/9dec082c9f0e9f75e418b191f64ff4b88a89b7bcf97dc41e485e0c4b5c960b11) in ledger 5400860, shown as `completed` in the wallet.

Screenshots of the interactive page: [withdrawal page](evidence/tranche1/1-withdrawal-page.jpg), [details entered](evidence/tranche1/2-details-entered.jpg), [ready to pay](evidence/tranche1/3-ready-to-pay.jpg).

Withdrawal 1 exposed a bug. SAP's observer matched the payment within seconds, but the Business Server could not list funded transactions: SAP 5.0's JSON-RPC `get_transactions` has no default for `order_by` and `order` and returns -32603 without them. Commit `29f1286` sends both. After it deployed, withdrawal 1 completed by itself, and withdrawal 2 then ran from start to `completed` with no intervention, the payout following the payment within seconds.

## Testnet and mainnet

The deployment runs on testnet (`STELLAR_NETWORK=testnet` on the Business Server, `ANCHOR_NETWORK=testnet` on core). No real money or real identities are used on testnet, so two steps are simulated:

- **Dojah verification is simulated.** The Business Server accepts the customer without calling Dojah, so testers never submit real identity data. On mainnet the same code calls Slimepay's production Dojah integration (BVN and NIN lookup, name and date of birth match, sanctions and PEP screening, manual review on a possible match). That integration is in `internal/kyc` and covered by its unit tests.
- **The SafeHaven payout is simulated.** Core resolves the bank account (the page shows "ACCOUNT 6789") and records the naira payout as sent without moving money. On mainnet core pays through SafeHaven, Slimepay's production payout provider. Every payout carries its network and core refuses a mismatch, so testnet withdrawals cannot reach the mainnet payout path.

Everything else is the production code path: SAP's SEP endpoints and observer, the Business Server's interactive page, ledger checks against Horizon, the payout API in core with its once-only guarantee, and the Platform API calls that move the transaction to `completed`.

## Reproducing

1. Install the Stellar SDK next to `e2e.js` (`npm install @stellar/stellar-sdk`).
2. `node e2e.js setup` creates and funds a testnet wallet and buys USDC.
3. `node e2e.js start 5` logs in with SEP-10 and starts a SEP-24 withdrawal. Open the URL it saves and complete the page.
4. `node e2e.js pay` sends the USDC with SAP's memo, and `node e2e.js wait` follows the transaction to `completed`.
