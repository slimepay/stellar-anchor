// Withdrawal-only checks against the live testnet anchor, for the SDF Validation
// Suite tests that only ran after a deposit test (Slimepay is withdraw-only).
// Uses the suite's own schemas from @stellar/anchor-tests 0.6.22.
// Usage: node withdrawal-checks.js   (needs wallet-state.json from e2e.js with a completed withdrawal)
const fs = require("fs");
const path = require("path");
const { Validator } = require("jsonschema");
const { Keypair, TransactionBuilder, Networks } = require("@stellar/stellar-sdk");
const sep24 = require("@stellar/anchor-tests/lib/schemas/sep24");
const sep6 = require("@stellar/anchor-tests/lib/schemas/sep6");

const DIR = __dirname;
const ANCHOR = "https://anchor.slimepay.com";
const USDC_ISSUER = "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5";
const REPORT = path.join(DIR, "withdrawal-checks-report.txt");
const state = JSON.parse(fs.readFileSync(path.join(DIR, "wallet-state.json"), "utf8"));
const v = new Validator();
const lines = [];
let passed = 0;
let failed = 0;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function check(name, ok, detail) {
  ok ? passed++ : failed++;
  lines.push(`${ok ? "√" : "×"} ${name}${!ok && detail ? `\n    ${detail}` : ""}`);
}

function schema(name, body, s) {
  const r = v.validate(body, s);
  check(name, r.valid, r.errors.map((e) => e.stack).join("; "));
}

async function req(method, url, { token, json, form } = {}) {
  const headers = {};
  if (token) headers.authorization = `Bearer ${token}`;
  let body;
  if (json) (headers["content-type"] = "application/json"), (body = JSON.stringify(json));
  if (form) (headers["content-type"] = "application/x-www-form-urlencoded"), (body = new URLSearchParams(form).toString());
  const res = await fetch(url, { method, headers, body, redirect: "manual" });
  const text = await res.text();
  let parsed;
  try {
    parsed = JSON.parse(text);
  } catch {
    parsed = undefined;
  }
  return { status: res.status, type: res.headers.get("content-type") || "", json: parsed, text };
}

async function auth(kp) {
  const c = await req("GET", `${ANCHOR}/auth?account=${kp.publicKey()}&home_domain=slimepay.com`);
  const tx = TransactionBuilder.fromXDR(c.json.transaction, Networks.TESTNET);
  tx.sign(kp);
  const t = await req("POST", `${ANCHOR}/auth`, { json: { transaction: tx.toXDR() } });
  if (!t.json?.token) throw new Error("SEP-10 failed");
  return t.json.token;
}

async function fresh() {
  const kp = Keypair.random();
  await fetch(`https://friendbot.stellar.org?addr=${kp.publicKey()}`);
  return { kp, token: await auth(kp) };
}

async function moreInfo(name, url) {
  if (!url) return check(name, false, "more_info_url is missing");
  // Stricter than the suite, which only wants 200 text/html: the slimepay.com
  // homepage passes that too, so the page must also show this transaction.
  const r = await req("GET", url);
  const id = new URL(url).searchParams.get("transaction_id");
  check(name, r.status === 200 && r.type.includes("text/html") && Boolean(id) && r.text.includes(id),
    `GET ${url.replace(/token=[^&]+/, "token=<redacted>")} -> ${r.status} ${r.type}, shows the transaction: ${Boolean(id) && r.text.includes(id)}`);
}

// Fills in the Slimepay interactive page, which asks SAP for the on-chain
// payment and so moves the transaction to pending_user_transfer_start. Nothing is paid.
async function interactiveToPending(token, account) {
  const w = await req("POST", `${ANCHOR}/sep24/transactions/withdraw/interactive`, {
    token, json: { asset_code: "USDC", asset_issuer: USDC_ISSUER, account, amount: "1", lang: "en" },
  });
  const u = new URL(w.json.url);
  const base = { transaction_id: u.searchParams.get("transaction_id"), token: u.searchParams.get("token") };
  const post = (form) => req("POST", `${u.origin}${u.pathname}`, { form: { ...base, ...form } });
  await post({ step: "identity", first_name: "John", last_name: "Doe", email_address: "john@example.com", birth_date: "1990-01-01", id_type: "bvn", id_number: "22222222222" });
  await post({ amount: "1", bank_number: "000013", bank_account_number: "0123456789" });
  return w.json.id;
}

async function history(sep, base, token, assetQuery, txSchema, kindIn) {
  const p = `SEP-${sep} » GET /transactions »`;
  const noAuth = await req("GET", `${base}/transactions?${assetQuery}`);
  check(`${p} requires a SEP-10 JWT`, noAuth.status === 401 || noAuth.status === 403, `got ${noAuth.status}`);

  const all = await req("GET", `${base}/transactions?${assetQuery}`, { token });
  const txs = all.json?.transactions || [];
  check(`${p} has a record on /transactions after a withdraw request`, txs.length > 0, `got ${all.status}`);
  schema(`${p} has proper withdraw transaction schema on /transactions`, all.json, txSchema);

  const limited = await req("GET", `${base}/transactions?${assetQuery}&limit=1`, { token });
  check(`${p} returns proper number of transactions when 'limit' parameter is given`, limited.json?.transactions?.length === 1,
    `got ${limited.json?.transactions?.length}`);

  const starts = txs.map((t) => Date.parse(t.started_at));
  check(`${p} transactions are returned in descending order of creation`, txs.length > 1 && starts.every((s, i) => i === 0 || starts[i - 1] >= s),
    `started_at: ${txs.map((t) => t.started_at).join(", ")}`);

  const cutoff = txs[1]?.started_at;
  const newer = await req("GET", `${base}/transactions?${assetQuery}&no_older_than=${encodeURIComponent(cutoff)}`, { token });
  const nt = newer.json?.transactions || [];
  check(`${p} returns proper transactions when 'no_older_than' parameter is given`,
    nt.length >= 1 && nt.length < txs.length && nt.every((t) => Date.parse(t.started_at) >= Date.parse(cutoff)),
    `no_older_than=${cutoff} returned ${nt.length} of ${txs.length}`);

  const kind = await req("GET", `${base}/transactions?${assetQuery}&kind=${kindIn}`, { token });
  const kt = kind.json?.transactions || [];
  check(`${p} only returns withdraw transactions when kind=${kindIn}`, kt.length > 0 && kt.every((t) => t.kind === "withdrawal"),
    `kinds: ${kt.map((t) => t.kind).join(", ")}`);

  const empty = await fresh();
  const none = await req("GET", `${base}/transactions?${assetQuery}`, { token: empty.token });
  check(`${p} returns an empty list for accounts with no transactions`, none.status === 200 && none.json?.transactions?.length === 0,
    `got ${none.status} ${none.text.slice(0, 120)}`);
}

async function runSep24() {
  const base = `${ANCHOR}/sep24`;
  const kp = Keypair.fromSecret(state.secret);
  const token = await auth(kp);
  const p = "SEP-24 » GET /transaction »";

  const noAuth = await req("GET", `${base}/transaction?id=${state.txId}`);
  check(`${p} requires a SEP-10 JWT on /transaction`, noAuth.status === 401 || noAuth.status === 403, `got ${noAuth.status}`);

  const done = (await req("GET", `${base}/transaction?id=${state.txId}`, { token })).json;
  check(`${p} the e2e withdrawal is completed`, done?.transaction?.status === "completed", `status ${done?.transaction?.status}`);
  schema(`${p} has proper 'completed' withdraw transaction schema on /transaction`, done, sep24.getTransactionSchema("withdrawal", "completed"));
  await moreInfo(`${p} has a valid 'more_info_url' (completed)`, done?.transaction?.more_info_url);

  const byHash = await req("GET", `${base}/transaction?stellar_transaction_id=${done?.transaction?.stellar_transaction_id}`, { token });
  check(`${p} returns valid withdraw transaction when using 'stellar_transaction_id' param`, byHash.json?.transaction?.id === state.txId,
    `got ${byHash.status} ${byHash.json?.transaction?.id}`);

  const pendingId = await interactiveToPending(token, kp.publicKey());
  const pending = (await req("GET", `${base}/transaction?id=${pendingId}`, { token })).json;
  check(`${p} interactive withdrawal reached pending_user_transfer_start`, pending?.transaction?.status === "pending_user_transfer_start",
    `status ${pending?.transaction?.status}`);
  schema(`${p} has proper 'pending_user_transfer_start' withdraw transaction schema on /transaction`, pending,
    sep24.getTransactionSchema("withdrawal", "pending_user_transfer_start"));
  await moreInfo(`${p} has a valid 'more_info_url' (pending_user_transfer_start)`, pending?.transaction?.more_info_url);

  await history("24", base, token, `asset_code=USDC`, sep24.transactionsSchema, "withdrawal");
  return { pendingId };
}

async function runSep6() {
  const base = `${ANCHOR}/sep6`;
  const { kp, token } = await fresh();
  const p = "SEP-6 » GET /transaction »";

  const put = await req("PUT", `${ANCHOR}/sep12/customer`, {
    token, json: { account: kp.publicKey(), first_name: "John", last_name: "Doe", email_address: "john@example.com", birth_date: "1990-01-01",
      id_type: "bvn", id_number: "22222222222", bank_account_number: "0123456789", bank_number: "000013" },
  });
  check("SEP-6 » setup » SEP-12 customer accepted", put.status === 202 || put.status === 200, `got ${put.status} ${put.text.slice(0, 120)}`);

  const ids = [];
  for (let i = 0; i < 2; i++) {
    const w = await req("GET", `${base}/withdraw?asset_code=USDC&type=bank_account&amount=1&account=${kp.publicKey()}`, { token });
    ids.push(w.json?.id);
    await sleep(1100);
  }

  const noAuth = await req("GET", `${base}/transaction?id=${ids[0]}`);
  check(`${p} requires a JWT`, noAuth.status === 401 || noAuth.status === 403, `got ${noAuth.status}`);

  let tx;
  for (let i = 0; i < 12; i++) {
    tx = (await req("GET", `${base}/transaction?id=${ids[0]}`, { token })).json;
    if (tx?.transaction?.status === "pending_user_transfer_start") break;
    await sleep(10000);
  }
  check(`${p} the processor started the withdrawal once KYC passed`, tx?.transaction?.status === "pending_user_transfer_start",
    `status ${tx?.transaction?.status}`);
  schema(`${p} has proper withdraw transaction schema on /transaction`, tx, sep6.getTransactionSchema(false));
  await moreInfo(`${p} has a valid 'more_info_url'`, tx?.transaction?.more_info_url);

  await history("6", base, token, `asset_code=USDC`, sep6.transactionsSchema, "withdrawal");
}

(async () => {
  const started = new Date().toISOString();
  const { pendingId } = await runSep24();
  await runSep6();
  const out = [
    `Withdrawal checks against ${ANCHOR} (Stellar testnet), ${started}`,
    `Schemas: @stellar/anchor-tests 0.6.22. Completed withdrawal: ${state.txId}. Pending withdrawal created by this run: ${pendingId}.`,
    "",
    ...lines,
    "",
    "Not applicable: SEP-6 GET /withdraw 'account' parameter checks. The suite runs them only when /info's authentication_required is false; Slimepay requires SEP-10.",
    "",
    `Checks: ${failed} failed, ${passed} passed, ${passed + failed} total`,
  ].join("\n");
  fs.writeFileSync(REPORT, out + "\n");
  console.log(out);
  process.exit(failed ? 1 : 0);
})().catch((e) => {
  console.error(e);
  process.exit(2);
});
