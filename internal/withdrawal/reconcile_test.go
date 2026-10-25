package withdrawal

import (
	"context"
	"testing"

	"aidmedium/stellar-anchor/internal/chain"
	"aidmedium/stellar-anchor/internal/sap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memCursor struct{ v string }

func (m *memCursor) Cursor(context.Context) (string, error)      { return m.v, nil }
func (m *memCursor) SetCursor(_ context.Context, v string) error { m.v = v; return nil }

func completed(t *testing.T, e *env, id string) {
	t.Helper()
	startAndFund(t, e, id, "10")
	e.proc.Tick(ctx)
	require.Equal(t, sap.StatusCompleted, e.platform.txs[id].Status)
}

func TestReconcileOK(t *testing.T) {
	e := newEnv(incomplete("tx-1", "24", assetUSDC))
	completed(t, e, "tx-1")

	NewReconciler(e.proc, &memCursor{}).Tick(ctx)
	assert.Equal(t, ReconcileOK, e.store.m["tx-1"].Reconcile)
	assert.Empty(t, e.alerts.got)
}

func TestReconcileMismatches(t *testing.T) {
	cases := map[string]func(e *env){
		"payout missing in core":    func(e *env) { delete(e.core.payouts, "tx-1") },
		"payout not completed":      func(e *env) { e.core.payouts["tx-1"].Status = "processing" },
		"payout amount differs":     func(e *env) { e.core.payouts["tx-1"].Amount = "1.00" },
		"payout reference differs":  func(e *env) { e.platform.txs["tx-1"].ExternalTransactionID = "other" },
		"SAP amount_out differs":    func(e *env) { e.platform.txs["tx-1"].AmountOut.Amount = "1.00" },
		"ledger payment disappears": func(e *env) { delete(e.chain.txs, "hash-tx-1") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(incomplete("tx-1", "24", assetUSDC))
			completed(t, e, "tx-1")
			mutate(e)

			r := NewReconciler(e.proc, &memCursor{})
			r.Tick(ctx)
			assert.Equal(t, ReconcileMismatch, e.store.m["tx-1"].Reconcile)
			assert.NotEmpty(t, e.store.m["tx-1"].ReconcileNote)
			assert.Len(t, e.alerts.got, 1)

			r.Tick(ctx)
			assert.Len(t, e.alerts.got, 1, "a mismatch is reported once")
		})
	}
}

func TestReconcileUnmatchedPayments(t *testing.T) {
	e := newEnv(incomplete("tx-1", "24", assetUSDC))
	completed(t, e, "tx-1")
	e.chain.incoming = []chain.Payment{
		{TxHash: "known", From: user, To: distribution, Asset: assetUSDC, Amount: "10", Memo: "777"},
		{TxHash: "no-memo", From: "GSOMEONE", To: distribution, Asset: assetUSDC, Amount: "5"},
		{TxHash: "stray", From: "GSOMEONE", To: distribution, Asset: AssetXLM, Amount: "3", Memo: "123"},
	}
	cursor := &memCursor{}

	NewReconciler(e.proc, cursor).Tick(ctx)
	require.Len(t, e.alerts.got, 2)
	assert.Contains(t, e.alerts.got[0], "no-memo")
	assert.Contains(t, e.alerts.got[1], "stray")
	assert.Equal(t, "page-1", cursor.v, "the cursor moves on so payments are reported once")

	NewReconciler(e.proc, cursor).Tick(ctx)
	assert.Len(t, e.alerts.got, 2)
}
