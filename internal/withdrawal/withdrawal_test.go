package withdrawal

import (
	"context"
	"errors"
	"testing"

	"aidmedium/stellar-anchor/internal/chain"
	"aidmedium/stellar-anchor/internal/customer"
	"aidmedium/stellar-anchor/internal/payout"
	"aidmedium/stellar-anchor/internal/sap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var ctx = context.Background()

func TestQuote(t *testing.T) {
	e := newEnv()

	q, err := e.svc.Quote(assetUSDC, "10.5")
	require.NoError(t, err)
	assert.Equal(t, "10.5000000", q.AmountIn)
	assert.Equal(t, "15750.00", q.AmountOut)
	assert.Equal(t, "1500.00", q.Rate)

	q, err = e.svc.Quote(AssetXLM, "0.4")
	require.NoError(t, err)
	assert.Equal(t, "100.00", q.AmountOut)

	for _, tc := range []struct{ asset, amount string }{
		{AssetXLM, "0.3"},
		{assetUSDC, "0"},
		{assetUSDC, "-5"},
		{assetUSDC, "abc"},
		{assetUSDC, "1.12345678"},
		{"stellar:EURC:GXYZ", "10"},
	} {
		_, err := e.svc.Quote(tc.asset, tc.amount)
		var uerr *UserError
		assert.ErrorAs(t, err, &uerr, "%s %s", tc.asset, tc.amount)
	}
}

func TestStart(t *testing.T) {
	e := newEnv(incomplete("tx-1", "24", assetUSDC))
	tx, _ := e.platform.GetTransaction(ctx, "tx-1")

	w, err := e.svc.Start(ctx, tx, user, "", "10")
	require.NoError(t, err)
	assert.Equal(t, "777", w.StellarMemo)
	assert.Equal(t, "ADA OBI", w.AccountName)
	assert.Equal(t, "cust-1", w.CustomerID)

	r := e.platform.request
	require.NotNil(t, r)
	assert.Equal(t, sap.Amount{Amount: "10.0000000", Asset: assetUSDC}, *r.AmountIn)
	assert.Equal(t, sap.Amount{Amount: "15000.00", Asset: AssetNGN}, *r.AmountOut)
	assert.Equal(t, sap.FeeDetails{Total: "0", Asset: assetUSDC}, *r.FeeDetails)
	assert.Equal(t, "10.0000000", r.AmountExpected.Amount)

	stored := e.store.m["tx-1"]
	assert.Equal(t, "777", stored.StellarMemo)
	assert.Equal(t, "0123456789", stored.AccountNumber)
}

func TestStartRetryKeepsOriginalQuote(t *testing.T) {
	e := newEnv(incomplete("tx-1", "24", assetUSDC))
	e.platform.failOn = "request_onchain_funds"
	tx, _ := e.platform.GetTransaction(ctx, "tx-1")
	_, err := e.svc.Start(ctx, tx, user, "", "10")
	require.Error(t, err)

	e.platform.failOn = ""
	w, err := e.svc.Start(ctx, tx, user, "", "99")
	require.NoError(t, err)
	assert.Equal(t, "10.0000000", w.AmountIn, "the first recorded quote wins")
}

func TestStartRefusals(t *testing.T) {
	e := newEnv(incomplete("tx-1", "24", assetUSDC))
	tx, _ := e.platform.GetTransaction(ctx, "tx-1")

	_, err := e.svc.Start(ctx, tx, "GSTRANGER", "", "10")
	assert.ErrorIs(t, err, ErrKYCIncomplete)

	e.customers[user+":"].Status = customer.StatusProcessing
	_, err = e.svc.Start(ctx, tx, user, "", "10")
	assert.ErrorIs(t, err, ErrKYCIncomplete)
	e.customers[user+":"].Status = customer.StatusAccepted

	e.core.rejectName = true
	_, err = e.svc.Start(ctx, tx, user, "", "10")
	var uerr *UserError
	assert.ErrorAs(t, err, &uerr)
	e.core.rejectName = false

	started := *tx
	started.Status = sap.StatusPendingAnchor
	_, err = e.svc.Start(ctx, &started, user, "", "10")
	assert.ErrorAs(t, err, &uerr)

	deposit := *tx
	deposit.Kind = "deposit"
	_, err = e.svc.Start(ctx, &deposit, user, "", "10")
	assert.ErrorAs(t, err, &uerr)

	assert.Nil(t, e.platform.request, "SAP is never asked for funds on a refused withdrawal")
	assert.Empty(t, e.store.m)
}

func startAndFund(t *testing.T, e *env, id, amount string) {
	t.Helper()
	tx, _ := e.platform.GetTransaction(ctx, id)
	_, err := e.svc.Start(ctx, tx, user, "", amount)
	require.NoError(t, err)
	e.funded(id, amount, "777")
}

func TestPayoutHappyPath(t *testing.T) {
	e := newEnv(incomplete("tx-1", "24", assetUSDC))
	startAndFund(t, e, "tx-1", "10")

	e.proc.Tick(ctx)
	assert.Equal(t, []string{"request_onchain_funds:tx-1", "pending:tx-1", "sent:tx-1"}, e.platform.calls)
	assert.Equal(t, sap.StatusCompleted, e.platform.txs["tx-1"].Status)
	assert.Equal(t, 1, e.core.transfers)
	assert.Equal(t, "testnet", e.core.lastNetwork, "core is told which network the payout belongs to")
	assert.Equal(t, "ref-tx-1", e.store.m["tx-1"].PayoutRef)

	e.proc.Tick(ctx)
	assert.Equal(t, 1, e.core.transfers, "completed withdrawals are left alone")
	assert.Empty(t, e.alerts.got)
}

func TestPayoutWaitsForSlowTransfer(t *testing.T) {
	e := newEnv(incomplete("tx-1", "24", assetUSDC))
	startAndFund(t, e, "tx-1", "10")
	e.core.payStatus = payout.StatusProcessing

	e.proc.Tick(ctx)
	assert.Equal(t, sap.StatusPendingExternal, e.platform.txs["tx-1"].Status)

	e.proc.Tick(ctx)
	assert.Equal(t, sap.StatusPendingExternal, e.platform.txs["tx-1"].Status)

	e.core.payouts["tx-1"].Status = payout.StatusCompleted
	e.proc.Tick(ctx)
	assert.Equal(t, sap.StatusCompleted, e.platform.txs["tx-1"].Status)
	assert.Equal(t, 1, e.core.transfers)
}

func TestPayoutFailedLater(t *testing.T) {
	e := newEnv(incomplete("tx-1", "24", assetUSDC))
	startAndFund(t, e, "tx-1", "10")
	e.core.payStatus = payout.StatusProcessing
	e.proc.Tick(ctx)

	e.core.payouts["tx-1"].Status = payout.StatusFailed
	e.core.payouts["tx-1"].Error = "beneficiary bank unavailable"
	e.proc.Tick(ctx)
	assert.Equal(t, sap.StatusError, e.platform.txs["tx-1"].Status)
	assert.Len(t, e.alerts.got, 1)
}

func TestTransientCoreErrorNeverPaysTwice(t *testing.T) {
	e := newEnv(incomplete("tx-1", "24", assetUSDC))
	startAndFund(t, e, "tx-1", "10")

	e.core.payErr = errors.New("timeout")
	e.proc.Tick(ctx)
	assert.Equal(t, sap.StatusPendingAnchor, e.platform.txs["tx-1"].Status)

	e.core.payErr = nil
	e.platform.failOn = "sent"
	e.proc.Tick(ctx)
	assert.Equal(t, sap.StatusPendingExternal, e.platform.txs["tx-1"].Status)

	e.platform.failOn = ""
	e.proc.Tick(ctx)
	assert.Equal(t, sap.StatusCompleted, e.platform.txs["tx-1"].Status)
	assert.Equal(t, 1, e.core.transfers)
}

func TestCoreRejectsPayout(t *testing.T) {
	e := newEnv(incomplete("tx-1", "24", assetUSDC))
	startAndFund(t, e, "tx-1", "10")
	e.core.rejectPay = true

	e.proc.Tick(ctx)
	assert.Equal(t, sap.StatusError, e.platform.txs["tx-1"].Status)
	assert.Equal(t, payout.StatusFailed, e.store.m["tx-1"].PayoutStatus)
}

func TestHoldsInsteadOfPaying(t *testing.T) {
	cases := map[string]func(e *env){
		"short payment on ledger": func(e *env) {
			e.chain.txs["hash-tx-1"].Payments[0].Amount = "9"
		},
		"wrong memo on ledger": func(e *env) {
			e.chain.txs["hash-tx-1"].Memo = "778"
		},
		"failed ledger transaction": func(e *env) {
			e.chain.txs["hash-tx-1"].Successful = false
		},
		"payment not on ledger": func(e *env) {
			delete(e.chain.txs, "hash-tx-1")
		},
		"paid to another account": func(e *env) {
			e.chain.txs["hash-tx-1"].Payments[0].To = "GOTHER"
		},
		"wrong asset": func(e *env) {
			e.chain.txs["hash-tx-1"].Payments[0].Asset = AssetXLM
		},
		"SAP amount differs from quote": func(e *env) {
			e.platform.txs["tx-1"].AmountIn.Amount = "20"
		},
		"no stellar transaction": func(e *env) {
			e.platform.txs["tx-1"].StellarTransactions = nil
		},
		"no withdrawal record": func(e *env) {
			delete(e.store.m, "tx-1")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(incomplete("tx-1", "24", assetUSDC))
			startAndFund(t, e, "tx-1", "10")
			mutate(e)

			e.proc.Tick(ctx)
			assert.Equal(t, sap.StatusOnHold, e.platform.txs["tx-1"].Status)
			assert.Zero(t, e.core.transfers, "no naira leaves on a mismatch")
			assert.Len(t, e.alerts.got, 1)
		})
	}
}

func TestSplitPaymentsAddUp(t *testing.T) {
	e := newEnv(incomplete("tx-1", "24", assetUSDC))
	startAndFund(t, e, "tx-1", "10")
	led := e.chain.txs["hash-tx-1"]
	led.Payments = []chain.Payment{
		{To: distribution, Asset: assetUSDC, Amount: "4"},
		{To: distribution, Asset: assetUSDC, Amount: "6"},
	}

	e.proc.Tick(ctx)
	assert.Equal(t, sap.StatusCompleted, e.platform.txs["tx-1"].Status)
}

func TestPayoutAmountMismatchHolds(t *testing.T) {
	e := newEnv(incomplete("tx-1", "24", assetUSDC))
	startAndFund(t, e, "tx-1", "10")
	e.core.amountDrift = "16000.00"

	e.proc.Tick(ctx)
	assert.Equal(t, sap.StatusOnHold, e.platform.txs["tx-1"].Status)
}

func TestPendingExternalWithoutPayoutHolds(t *testing.T) {
	e := newEnv(incomplete("tx-1", "24", assetUSDC))
	startAndFund(t, e, "tx-1", "10")
	e.platform.txs["tx-1"].Status = sap.StatusPendingExternal

	e.proc.Tick(ctx)
	assert.Equal(t, sap.StatusOnHold, e.platform.txs["tx-1"].Status)
}

func TestSep6Withdrawal(t *testing.T) {
	tx := incomplete("tx-6", "6", AssetXLM)
	tx.AmountExpected.Amount = "100"
	e := newEnv(tx)

	e.proc.Tick(ctx)
	assert.Equal(t, sap.StatusPendingUserTransferStart, e.platform.txs["tx-6"].Status)
	assert.Equal(t, "25000.00", e.store.m["tx-6"].AmountOut)

	e.funded("tx-6", "100", "777")
	e.proc.Tick(ctx)
	assert.Equal(t, sap.StatusCompleted, e.platform.txs["tx-6"].Status)
}

func TestSep6WaitsForKYC(t *testing.T) {
	tx := incomplete("tx-6", "6", AssetXLM)
	tx.AmountExpected.Amount = "100"
	e := newEnv(tx)
	e.customers[user+":"].Status = customer.StatusProcessing

	e.proc.Tick(ctx)
	assert.Equal(t, sap.StatusIncomplete, e.platform.txs["tx-6"].Status)
	assert.Empty(t, e.platform.calls)
}

func TestSep6BadInputErrors(t *testing.T) {
	noAmount := incomplete("tx-a", "6", AssetXLM)
	tooSmall := incomplete("tx-b", "6", AssetXLM)
	tooSmall.AmountExpected.Amount = "0.1"
	e := newEnv(noAmount, tooSmall)

	e.proc.Tick(ctx)
	assert.Equal(t, sap.StatusError, e.platform.txs["tx-a"].Status)
	assert.Equal(t, sap.StatusError, e.platform.txs["tx-b"].Status)
}

func TestSep24IncompleteIsLeftToTheUser(t *testing.T) {
	tx := incomplete("tx-1", "24", assetUSDC)
	tx.AmountExpected.Amount = "10"
	e := newEnv(tx)

	e.proc.Tick(ctx)
	assert.Empty(t, e.platform.calls, "SEP-24 withdrawals start from the interactive page")
}

func TestUnknownPayoutOutcomeHolds(t *testing.T) {
	e := newEnv(incomplete("tx-1", "24", assetUSDC))
	startAndFund(t, e, "tx-1", "10")
	e.core.payStatus = payout.StatusProcessing
	e.core.payouts["tx-1"] = &payout.Payout{TransactionID: "tx-1", Status: payout.StatusProcessing, Amount: "15000.00",
		Reference: "anchor-tx-1", Error: "transfer outcome unknown: connection reset"}

	e.proc.Tick(ctx)
	assert.Equal(t, sap.StatusOnHold, e.platform.txs["tx-1"].Status)
	require.Len(t, e.alerts.got, 1)
	assert.Contains(t, e.alerts.got[0], "outcome unknown")
}

func TestUnknownOutcomeWhilePendingExternalHolds(t *testing.T) {
	e := newEnv(incomplete("tx-1", "24", assetUSDC))
	startAndFund(t, e, "tx-1", "10")
	e.core.payStatus = payout.StatusProcessing
	e.proc.Tick(ctx)
	require.Equal(t, sap.StatusPendingExternal, e.platform.txs["tx-1"].Status)

	e.core.payouts["tx-1"].Error = "transfer outcome unknown: timeout"
	e.proc.Tick(ctx)
	assert.Equal(t, sap.StatusOnHold, e.platform.txs["tx-1"].Status)
}
