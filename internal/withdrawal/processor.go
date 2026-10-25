package withdrawal

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"aidmedium/stellar-anchor/internal/alert"
	"aidmedium/stellar-anchor/internal/audit"
	"aidmedium/stellar-anchor/internal/chain"
	"aidmedium/stellar-anchor/internal/money"
	"aidmedium/stellar-anchor/internal/payout"
	"aidmedium/stellar-anchor/internal/sap"

	"github.com/rs/zerolog/log"
)

const pageSize = 50

// Processor polls SAP for withdrawals that need the Business Server and moves
// them along. SAP events stay disabled, so polling is the only trigger.
type Processor struct {
	svc          *Service
	chain        chain.Reader
	alerts       alert.Alerter
	distribution string
}

func NewProcessor(svc *Service, reader chain.Reader, alerts alert.Alerter, distributionAccount string) *Processor {
	return &Processor{svc: svc, chain: reader, alerts: alerts, distribution: distributionAccount}
}

func (p *Processor) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		p.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (p *Processor) Tick(ctx context.Context) {
	p.each(ctx, "6", sap.StatusIncomplete, p.startSep6)
	for _, sep := range []string{"24", "6"} {
		p.each(ctx, sep, sap.StatusPendingAnchor, p.payOut)
		p.each(ctx, sep, sap.StatusPendingExternal, p.checkPayout)
	}
}

func (p *Processor) each(ctx context.Context, sep, status string, fn func(context.Context, *sap.Transaction) error) {
	txs, err := p.svc.platform.ListTransactions(ctx, sep, []string{status}, 0, pageSize)
	if err != nil {
		log.Error().Err(err).Str("sep", sep).Str("status", status).Msg("failed to list transactions")
		return
	}
	for i := range txs {
		tx := &txs[i]
		if tx.Kind != sap.KindWithdrawal {
			continue
		}
		if err := fn(ctx, tx); err != nil {
			log.Error().Err(err).Str("tx", tx.ID).Str("status", status).Msg("withdrawal step failed")
		}
	}
}

// startSep6 prices a SEP-6 withdrawal once the customer has passed KYC. The
// wallet supplies the amount when it calls /withdraw.
func (p *Processor) startSep6(ctx context.Context, tx *sap.Transaction) error {
	if tx.Creator == nil || tx.Creator.Account == "" {
		return errors.New("transaction has no creator account")
	}
	if tx.AmountExpected == nil || tx.AmountExpected.Amount == "" {
		return p.svc.platform.NotifyTransactionError(ctx, tx.ID, "amount is required")
	}
	_, err := p.svc.Start(ctx, tx, tx.Creator.Account, tx.Creator.Memo, tx.AmountExpected.Amount)
	if errors.Is(err, ErrKYCIncomplete) {
		return nil
	}
	var uerr *UserError
	if errors.As(err, &uerr) {
		return p.svc.platform.NotifyTransactionError(ctx, tx.ID, uerr.Msg)
	}
	return err
}

func (p *Processor) payOut(ctx context.Context, tx *sap.Transaction) error {
	w, err := p.svc.store.Get(ctx, tx.ID)
	if errors.Is(err, ErrNotFound) {
		return p.hold(ctx, tx.ID, nil, "no withdrawal record for this transaction")
	}
	if err != nil {
		return err
	}

	if problem, err := p.checkFunds(ctx, tx, w); err != nil {
		return err
	} else if problem != "" {
		return p.hold(ctx, tx.ID, w, problem)
	}

	res, err := p.svc.core.Pay(ctx, payout.Request{
		TransactionID: tx.ID,
		Amount:        w.AmountOut,
		BankCode:      w.BankCode,
		AccountNumber: w.AccountNumber,
		Narration:     "Slimepay withdrawal " + shortID(tx.ID),
		Network:       p.svc.network,
	})
	var rejected *payout.RejectedError
	if errors.As(err, &rejected) {
		return p.fail(ctx, tx.ID, w, "payout was rejected: "+rejected.Message)
	}
	if err != nil {
		// The payout may or may not have been created. Core is idempotent on the
		// transaction ID, so the next tick asks again and gets the same answer.
		return err
	}
	if !money.Equal(res.Amount, w.AmountOut) {
		return p.hold(ctx, tx.ID, w, fmt.Sprintf("payout amount %s does not match expected %s", res.Amount, w.AmountOut))
	}

	w.PayoutRef, w.PayoutStatus = res.Reference, res.Status
	if err := p.save(ctx, w); err != nil {
		return err
	}
	if unknownOutcome(res) {
		return p.hold(ctx, tx.ID, w, res.Error)
	}

	switch res.Status {
	case payout.StatusFailed:
		return p.fail(ctx, tx.ID, w, "payout failed: "+res.Error)
	case payout.StatusCompleted, payout.StatusProcessing:
		if err := p.svc.platform.NotifyOffchainFundsPending(ctx, tx.ID, res.Reference, "naira payout in progress"); err != nil {
			return err
		}
		if res.Status == payout.StatusCompleted {
			return p.complete(ctx, tx.ID, w)
		}
		return nil
	default:
		return fmt.Errorf("unknown payout status %q", res.Status)
	}
}

func (p *Processor) checkPayout(ctx context.Context, tx *sap.Transaction) error {
	w, err := p.svc.store.Get(ctx, tx.ID)
	if err != nil {
		return err
	}
	res, err := p.svc.core.Get(ctx, tx.ID)
	if errors.Is(err, payout.ErrNotFound) {
		return p.hold(ctx, tx.ID, w, "core has no payout for a transaction marked pending_external")
	}
	if err != nil {
		return err
	}
	w.PayoutRef, w.PayoutStatus = res.Reference, res.Status
	if err := p.save(ctx, w); err != nil {
		return err
	}
	if unknownOutcome(res) {
		return p.hold(ctx, tx.ID, w, res.Error)
	}
	switch res.Status {
	case payout.StatusCompleted:
		return p.complete(ctx, tx.ID, w)
	case payout.StatusFailed:
		return p.fail(ctx, tx.ID, w, "payout failed: "+res.Error)
	}
	return nil
}

// checkFunds compares the payment SAP matched with what was asked for and with
// the ledger. It returns a description of the first problem found.
func (p *Processor) checkFunds(ctx context.Context, tx *sap.Transaction, w *Withdrawal) (string, error) {
	if tx.AmountIn == nil || !money.Equal(tx.AmountIn.Amount, w.AmountIn) || tx.AmountIn.Asset != w.Asset {
		return "amount_in in SAP does not match the requested amount", nil
	}
	if len(tx.StellarTransactions) == 0 {
		return "SAP reports no Stellar payment", nil
	}

	received := new(big.Rat)
	for _, st := range tx.StellarTransactions {
		onLedger, err := p.chain.Transaction(ctx, st.ID)
		if errors.Is(err, chain.ErrNotFound) {
			return "Stellar transaction " + st.ID + " is not on the ledger", nil
		}
		if err != nil {
			return "", err
		}
		if !onLedger.Successful {
			return "Stellar transaction " + st.ID + " failed", nil
		}
		if w.StellarMemo != "" && onLedger.Memo != w.StellarMemo {
			return "Stellar transaction " + st.ID + " has the wrong memo", nil
		}
		for _, pay := range onLedger.Payments {
			if pay.To != p.distribution || pay.Asset != w.Asset {
				continue
			}
			amt, err := money.Parse(pay.Amount)
			if err != nil {
				return "", err
			}
			received.Add(received, amt)
		}
	}
	expected, err := money.Parse(w.AmountIn)
	if err != nil {
		return "", err
	}
	if received.Cmp(expected) != 0 {
		return fmt.Sprintf("received %s on the ledger, expected %s", received.FloatString(assetDecimals), w.AmountIn), nil
	}
	return "", nil
}

func (p *Processor) complete(ctx context.Context, txID string, w *Withdrawal) error {
	if err := p.svc.platform.NotifyOffchainFundsSent(ctx, txID, w.PayoutRef, "naira sent to your bank account"); err != nil {
		return err
	}
	p.svc.audit.Record(ctx, audit.Entry{Action: "withdrawal.completed", TransactionID: txID, CustomerID: w.CustomerID,
		Detail: map[string]string{"payout_ref": w.PayoutRef, "amount_out": w.AmountOut}})
	return nil
}

func (p *Processor) fail(ctx context.Context, txID string, w *Withdrawal, reason string) error {
	if w != nil {
		w.PayoutStatus = payout.StatusFailed
		if err := p.save(ctx, w); err != nil {
			return err
		}
	}
	p.alerts.Alert(ctx, "withdrawal failed", txID+": "+reason)
	p.svc.audit.Record(ctx, audit.Entry{Action: "withdrawal.failed", TransactionID: txID, Detail: map[string]string{"reason": reason}})
	return p.svc.platform.NotifyTransactionError(ctx, txID, reason)
}

func (p *Processor) hold(ctx context.Context, txID string, w *Withdrawal, reason string) error {
	if w != nil {
		w.Reconcile, w.ReconcileNote = ReconcileMismatch, reason
		if err := p.save(ctx, w); err != nil {
			return err
		}
	}
	p.alerts.Alert(ctx, "withdrawal on hold", txID+": "+reason)
	p.svc.audit.Record(ctx, audit.Entry{Action: "withdrawal.on_hold", TransactionID: txID, Detail: map[string]string{"reason": reason}})
	return p.svc.platform.NotifyTransactionOnHold(ctx, txID, "under review")
}

func (p *Processor) save(ctx context.Context, w *Withdrawal) error {
	w.UpdatedAt = p.svc.now().UTC()
	return p.svc.store.Update(ctx, w)
}

// unknownOutcome is a payout core sent to SafeHaven without getting an answer.
// Only a person checking SafeHaven can tell whether the money went out.
func unknownOutcome(res *payout.Payout) bool {
	return res.Status == payout.StatusProcessing && res.Error != ""
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
