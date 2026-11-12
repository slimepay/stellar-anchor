# Tranche 1 Evidence

Evidence for the SCF #45 Tranche 1 completion criteria, produced against the public testnet deployment on 25 October 2026 and updated on 12 November 2026 for the review feedback (see [Review fixes](#review-fixes-12-november-2026)). The raw files are in [evidence/tranche1](evidence/tranche1).

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
| SEP-10, 12, 24 and 6 pass the SDF Anchor Validation Suite | 64 pass, including every SEP-1, SEP-10 and SEP-12 test. The 39 failures and 6 skips are deposit tests, plus withdrawal tests the suite only runs after a deposit test passes. Those withdrawal tests are covered by a withdrawal-only run of the same checks: 29 of 29 pass. See below. |
| A testnet SEP-24 withdrawal completes end to end | Withdrawals 1 to 3 below, plus withdrawal 6 on 12 November after the review fixes. |
| Architecture document matches the code | [ARCHITECTURE.md](ARCHITECTURE.md): SAP with Postgres, Business Server with MongoDB, core for payouts. |

## SDF Anchor Validation Suite

```bash
npx -y @stellar/anchor-tests@0.6.22 --home-domain https://slimepay.com --asset-code USDC --seps 1 10 12 6 24 --sep-config sep-config.json
```

Full output: [validation-suite-report.txt](evidence/tranche1/validation-suite-report.txt). Config: [sep-config.json](evidence/tranche1/sep-config.json).

**Result: 64 passed, 39 failed, 6 skipped.** The 12 November re-run, after the review fixes, gave the same result for every test.

- Every SEP-1, SEP-10 and SEP-12 test passes, including SEP-10 multi-signer support.
- These SEP-6 and SEP-24 withdrawal tests pass: `/info`, the auth and parameter checks on `/withdraw`, starting a withdrawal, the withdrawal schema on `/transaction` and `/transactions`, and the 404s.
- Only two tests fail directly. Both report "USDC is not present in the /info response" for deposits, on SEP-6 and on SEP-24. Slimepay offers withdrawals only (USDC in, naira out), as approved for this tranche, so `/info` lists no deposit assets.
- The other 37 failures say "failed dependency": the suite runs them only after a deposit test passes. Most are deposit tests, but these withdrawal-side tests are in that group and never ran:
  - SEP-24 `/transaction`: has a valid `more_info_url`.
  - SEP-24 and SEP-6 `/transactions`: empty list for an account with no transactions, `limit`, descending order, `no_older_than`, `kind=withdrawal`.
  - SEP-6 `/transaction` and `/transactions`: requires a JWT.
- Skipped for the same reason: the SEP-24 `pending_user_transfer_start` and `completed` withdrawal schemas, and lookup by `stellar_transaction_id`. The two SEP-6 `/withdraw` `account` checks are skipped because the suite runs them only when `authentication_required` is false, and Slimepay requires SEP-10.
- The suite has no withdraw-only mode, so [withdrawal-checks.js](evidence/tranche1/withdrawal-checks.js) runs those blocked checks with the suite's own schemas (`@stellar/anchor-tests` 0.6.22) against the live anchor. **29 of 29 pass**: [withdrawal-checks-report.txt](evidence/tranche1/withdrawal-checks-report.txt).
- The `more_info_url` check is stricter than the suite's, which only asks for `200 text/html`. The old link to the slimepay.com homepage met that too, so the script also requires the page to show the transaction.

The 25 October run was made on a machine whose clock ran about 2.3 seconds fast ([measured](evidence/tranche1/measure-skew.js) against anchor.slimepay.com). The suite's SEP-10 check compares the challenge's start time to the local clock, so that run used [clock-offset.js](evidence/tranche1/clock-offset.js) to correct Node's clock by 2.6 seconds. On 12 November the clock measured 0.66 seconds slow, and the re-run used `CLOCK_OFFSET_MS=660`. The anchor's own clock agrees with Horizon, Google and Cloudflare.

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

- **Dojah verification is simulated.** The Business Server decides without calling Dojah, so testers never submit real identity data. It accepts every ID number except two fixtures: `00000000001` is rejected and `00000000002` goes to manual review, so both outcomes can be shown on testnet. On mainnet the same code calls Slimepay's production Dojah integration (BVN and NIN lookup, name and date of birth match, sanctions and PEP screening, manual review on a possible match), which ignores the fixtures. That integration is in `internal/kyc` and covered by its unit tests.
- **The SafeHaven payout is simulated.** Core resolves the bank account (the page shows "ACCOUNT 6789") and records the naira payout as sent without moving money. On mainnet core pays through SafeHaven, Slimepay's production payout provider. Every payout carries its network and core refuses a mismatch, so testnet withdrawals cannot reach the mainnet payout path.

Everything else is the production code path: SAP's SEP endpoints and its payment observer on Stellar RPC, the Business Server's interactive page, the ledger checks against Horizon, the payout API in core with its once-only guarantee, and the Platform API calls that move the transaction to `completed`.

## Review fixes (12 November 2026)

What changed after the Tranche 1 review, and the evidence for each point. The five approved withdrawals were not repeated.

### more_info_url

SAP builds `more_info_url` from a configured base URL. Both base URLs pointed at `https://slimepay.com`, so the link opened the homepage, which is also where the demo wallet's popup went after "Almost done". The Business Server now serves `/more-info`, a read-only status page. It verifies SAP's token with the SEP-6 or SEP-24 more info secret, according to the SEP the token claims, checks that the token was issued for that transaction and that the transaction belongs to that SEP, and shows its status, amounts, payout reference and Stellar payment. Both base URLs point at it.

- Withdrawal 6 record, with the new link: [withdrawal-6-final-transaction.json](evidence/tranche1/withdrawal-6-final-transaction.json). The page it opens: [more-info-withdrawal-6.png](evidence/tranche1/more-info-withdrawal-6.png).
- Withdrawal-only checks, `more_info_url` on SEP-6 and SEP-24, pending and completed: [withdrawal-checks-report.txt](evidence/tranche1/withdrawal-checks-report.txt).

**Why the field was missing from the earlier records.** SAP did return `more_info_url`: it is in every `sep24.transaction_before_payment` and `sep24.final_transaction` entry of [e2e-log.jsonl](evidence/tranche1/e2e-log.jsonl), which `e2e.js` writes with the token redacted. But `e2e.js` wrote `final-transaction.json` without redaction, so that file held a live token, and when the files were copied into `withdrawal-1..3-final-transaction.json` the whole field was removed instead of just the token. Apart from that field, those three records are byte for byte the SEP-24 `GET /transaction` responses in the log. `e2e.js` now redacts the token in that file as well, and [withdrawal-6-final-transaction.json](evidence/tranche1/withdrawal-6-final-transaction.json) is its unedited output. The earlier three records are left as submitted. Their full responses, `more_info_url` included, are in the log.

### Withdrawal 6

Run on 12 November after the deploy, through the same flow as withdrawals 1 to 3. `e2e.js fill` now submits the interactive page, so the run needs no browser.

| | Withdrawal 6 |
|---|---|
| Wallet | `GD4HDTRVIBHXGINPVFYWW5L577IXTHJQ6P5CCSTEFNINBJEWVDTO7YDB` |
| SAP transaction | `1019f2dd-9083-450c-810a-c2df6f5f2cf4` |
| Sent | 5 USDC |
| Paid out | NGN 7,500.00 |
| Memo (id) | `8566453322519518321` |
| Stellar payment | [a41aea9f…de3a](https://stellar.expert/explorer/testnet/tx/a41aea9f7eeab25adc280789b2d50b593a8cec7d219101179510e4bf3432de3a), ledger 5692718 at 12:40:33 UTC |
| Payout reference | `anchor-1019f2dd-9083-450c-810a-c2df6f5f2cf4` |
| Final status | `completed` at 12:40:37 UTC, about 4 seconds after the ledger closed |
| SAP record | [withdrawal-6-final-transaction.json](evidence/tranche1/withdrawal-6-final-transaction.json) |

SAP's payment observer ran on Stellar RPC for this withdrawal. Monitoring is described in [ARCHITECTURE.md](ARCHITECTURE.md#ledger-monitoring).

### Dojah and SEP-12

The wallet's SEP-12 `PUT /customer` reaches SAP, SAP calls the Business Server's customer callback, and the callback runs the KYC check. Only an `ACCEPTED` customer gets the withdrawal form or a SEP-6 start. Live on testnet, with the fixture IDs ([live-status.jsonl](evidence/tranche1/kyc-fixtures/live-status.jsonl)):

| ID number | SEP-12 `GET /customer` | Interactive page | Withdrawal form posted directly | SEP-24 transaction |
|---|---|---|---|---|
| `00000000001` | `REJECTED` | [Verification Unsuccessful](evidence/tranche1/kyc-fixtures/2-rejected-00000000001.png) | 422, sent back to identity | stays `incomplete`, no memo |
| `00000000002` | `PROCESSING` (manual review) | [Verification in progress](evidence/tranche1/kyc-fixtures/3-review-00000000002.png) | 422, sent back to identity | stays `incomplete`, no memo |
| `22222222222` | `ACCEPTED` | [withdrawal form](evidence/tranche1/kyc-fixtures/4-accepted-withdraw-form.png) | accepted | moves on (withdrawal 6) |

`incomplete` with no memo means `request_onchain_funds` was never called, so the wallet has nowhere to pay. The unit tests behind it all pass: [go-test-kyc.txt](evidence/tranche1/kyc-fixtures/go-test-kyc.txt). They include:
- `TestBVNNameMismatch`, `TestBVNDOBMismatch` and `TestScreeningOutcomes` for the Dojah decisions.
- `TestDojahIgnoresTestnetFixtures` for mainnet.
- `TestTestnetFixturesControlVerification`, `TestIdentityOutcomes` and `TestWithdrawNeedsVerifiedIdentity` for the gate on the page.
- `TestSep6WaitsForKYC` and `TestSep6RejectedKYCNeverStarts` for SEP-6.

### Access control and testnet/production separation

- Live checks without credentials ([live-access-checks.txt](evidence/tranche1/access-and-separation/live-access-checks.txt)):
  - core's admin anchor status returns 403 "Only admins can access this resource".
  - core's internal anchor payout API returns 401 without the shared secret, and also with a wrong one.
  - the Business Server's SAP callback API and `/more-info` return 403 without a valid token.
- Admin views need the `admin` or `superadmin` role and a verified authenticator (TOTP) session (`requireAdmin` in core, `server/router/admin_guard.go`).
- Core refuses a payout for the other network, and on testnet it never reaches SafeHaven: `TestAnchorPayoutRefusesOtherNetwork` and `TestAnchorNetworkSelectsBank` in [go-test-core-anchor.txt](evidence/tranche1/access-and-separation/go-test-core-anchor.txt).
- The deployment runs only on testnet: `STELLAR_NETWORK=testnet` on the Business Server, `ANCHOR_NETWORK=testnet` on core and `STELLAR_NETWORK_NETWORK=TESTNET` on SAP, with keys made for testnet only ([runbook](TESTNET_RUNBOOK.md#1-keys-and-accounts)). Mainnet gets its own signing key, distribution account and secrets. Secrets live only in Railway variables.

## Reproducing

1. In `evidence/tranche1`, run `npm install` (the Stellar SDK and the SDF anchor tests, pinned in `package.json`).
2. `node e2e.js setup` creates and funds a testnet wallet and buys USDC.
3. `node e2e.js start 5` logs in with SEP-10 and starts a SEP-24 withdrawal. `node e2e.js fill` completes the page, or open the URL it saves and complete it by hand.
4. `node e2e.js pay` sends the USDC with SAP's memo, and `node e2e.js wait` follows the transaction to `completed`.
5. `node withdrawal-checks.js` runs the withdrawal-only checks against the completed withdrawal and writes `withdrawal-checks-report.txt`.

`e2e.js` keeps the wallet's secret in `wallet-state.json`, which is gitignored.
