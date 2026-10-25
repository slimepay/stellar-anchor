// End-to-end SEP-24 withdrawal on Stellar testnet against anchor.slimepay.com.
// Usage: node e2e.js setup | start <amount> | pay | wait
const fs = require("fs");
const path = require("path");
const StellarSdk = require("@stellar/stellar-sdk");
const { Keypair, Horizon, TransactionBuilder, Networks, Operation, Asset, Memo, BASE_FEE } = StellarSdk;

const DIR = __dirname;
const STATE = path.join(DIR, "wallet-state.json");
const LOG = path.join(DIR, "e2e-log.jsonl");
const HOME = "https://slimepay.com";
const ANCHOR = "https://anchor.slimepay.com";
const USDC = new Asset("USDC", "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5");
const horizon = new Horizon.Server("https://horizon-testnet.stellar.org");

const state = fs.existsSync(STATE) ? JSON.parse(fs.readFileSync(STATE, "utf8")) : {};
const save = () => fs.writeFileSync(STATE, JSON.stringify(state, null, 2));
const redact = (s) => JSON.parse(JSON.stringify(s).replace(/"token":"[^"]+"/g, '"token":"<redacted>"').replace(/token=[A-Za-z0-9._-]+/g, "token=<redacted>"));
function log(step, data) {
  const entry = { at: new Date().toISOString(), step, ...redact(data) };
  fs.appendFileSync(LOG, JSON.stringify(entry) + "\n");
  console.log(step, JSON.stringify(redact(data)).slice(0, 600));
}

async function http(method, url, body, token) {
  const res = await fetch(url, {
    method,
    headers: { "content-type": "application/json", ...(token ? { authorization: `Bearer ${token}` } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    json = { raw: text.slice(0, 500) };
  }
  return { status: res.status, json };
}

async function submit(ops, memo) {
  const kp = Keypair.fromSecret(state.secret);
  const account = await horizon.loadAccount(kp.publicKey());
  const b = new TransactionBuilder(account, { fee: BASE_FEE, networkPassphrase: Networks.TESTNET }).setTimeout(120);
  ops.forEach((op) => b.addOperation(op));
  if (memo) b.addMemo(memo);
  const tx = b.build();
  tx.sign(kp);
  return horizon.submitTransaction(tx);
}

async function auth() {
  const kp = Keypair.fromSecret(state.secret);
  const c = await http("GET", `${ANCHOR}/auth?account=${kp.publicKey()}&home_domain=slimepay.com`);
  log("sep10.challenge", { status: c.status, network_passphrase: c.json.network_passphrase });
  const tx = TransactionBuilder.fromXDR(c.json.transaction, Networks.TESTNET);
  tx.sign(kp);
  const t = await http("POST", `${ANCHOR}/auth`, { transaction: tx.toXDR() });
  log("sep10.token", { status: t.status, token_received: Boolean(t.json.token) });
  if (!t.json.token) throw new Error("no SEP-10 token");
  return t.json.token;
}

async function setup() {
  if (!state.secret) {
    const kp = Keypair.random();
    state.secret = kp.secret();
    state.account = kp.publicKey();
    save();
  }
  log("wallet", { account: state.account });
  const fb = await fetch(`https://friendbot.stellar.org?addr=${state.account}`);
  log("friendbot", { status: fb.status });
  const trust = await submit([Operation.changeTrust({ asset: USDC })]);
  log("trustline", { hash: trust.hash });
  const buy = await submit([
    Operation.pathPaymentStrictSend({
      sendAsset: Asset.native(),
      sendAmount: "200",
      destination: state.account,
      destAsset: USDC,
      destMin: "50",
      path: [],
    }),
  ]);
  log("buy_usdc", { hash: buy.hash });
  const acc = await horizon.loadAccount(state.account);
  log("balances", { balances: acc.balances.map((b) => `${b.balance} ${b.asset_code || "XLM"}`) });
}

async function start(amount) {
  const token = await auth();
  const r = await http("POST", `${ANCHOR}/sep24/transactions/withdraw/interactive`, {
    asset_code: "USDC",
    asset_issuer: USDC.getIssuer(),
    account: state.account,
    amount,
    lang: "en",
  }, token);
  log("sep24.withdraw_interactive", { status: r.status, id: r.json.id, type: r.json.type });
  state.txId = r.json.id;
  state.interactiveUrl = r.json.url;
  state.token = token;
  save();
  fs.writeFileSync(path.join(DIR, "interactive-url.txt"), r.json.url);
}

async function txStatus() {
  const r = await http("GET", `${ANCHOR}/sep24/transaction?id=${state.txId}`, null, state.token);
  return r;
}

async function pay() {
  const r = await txStatus();
  const t = r.json.transaction;
  log("sep24.transaction_before_payment", { status: r.status, transaction: t });
  if (t.status !== "pending_user_transfer_start") throw new Error(`status is ${t.status}, not pending_user_transfer_start`);
  const memoType = t.withdraw_memo_type;
  const memo = memoType === "id" ? Memo.id(t.withdraw_memo) : memoType === "hash" ? Memo.hash(Buffer.from(t.withdraw_memo, "base64").toString("hex")) : Memo.text(t.withdraw_memo);
  const res = await submit([Operation.payment({ destination: t.withdraw_anchor_account, asset: USDC, amount: t.amount_in })], memo);
  state.paymentHash = res.hash;
  save();
  log("stellar.payment", { hash: res.hash, to: t.withdraw_anchor_account, amount: t.amount_in, memo: t.withdraw_memo, memo_type: memoType, explorer: `https://stellar.expert/explorer/testnet/tx/${res.hash}` });
}

async function wait() {
  const seen = new Set();
  for (let i = 0; i < 60; i++) {
    const r = await txStatus();
    if (r.status === 401 || r.status === 403) {
      state.token = await auth();
      save();
      continue;
    }
    const t = r.json.transaction || {};
    if (!seen.has(t.status)) {
      seen.add(t.status);
      log("sep24.status", { status: t.status, message: t.message });
    }
    if (["completed", "error", "refunded", "expired", "pending_anchor"].includes(t.status) && t.status !== "pending_anchor") {
      log("sep24.final_transaction", { transaction: t });
      fs.writeFileSync(path.join(DIR, "final-transaction.json"), JSON.stringify(t, null, 2));
      return;
    }
    await new Promise((r) => setTimeout(r, 5000));
  }
  log("sep24.timeout", {});
}

const [cmd, arg] = process.argv.slice(2);
({ setup, start: () => start(arg || "5"), pay, wait })[cmd]().catch((e) => {
  log("error", { step: cmd, message: e?.response?.data?.extras?.result_codes ? JSON.stringify(e.response.data.extras.result_codes) : e.message });
  process.exit(1);
});
