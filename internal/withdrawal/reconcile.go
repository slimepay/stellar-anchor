package withdrawal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"aidmedium/stellar-anchor/internal/audit"
	"aidmedium/stellar-anchor/internal/chain"
	"aidmedium/stellar-anchor/internal/money"
	"aidmedium/stellar-anchor/internal/payout"
	"aidmedium/stellar-anchor/internal/sap"

	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const maxReconcilePages = 20

type CursorStore interface {
	Cursor(ctx context.Context) (string, error)
	SetCursor(ctx context.Context, cursor string) error
}

// Reconciler compares three records for every finished withdrawal: the SAP
// transaction, the payment on the ledger and the payout in core. It also
// flags payments to the distribution account that no withdrawal explains.
type Reconciler struct {
	proc    *Processor
	cursors CursorStore
}

func NewReconciler(proc *Processor, cursors CursorStore) *Reconciler {
	return &Reconciler{proc: proc, cursors: cursors}
}

func (r *Reconciler) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		r.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (r *Reconciler) Tick(ctx context.Context) {
	for _, sep := range []string{"24", "6"} {
		for page := 0; page < maxReconcilePages; page++ {
			txs, err := r.proc.svc.platform.ListTransactions(ctx, sep, []string{sap.StatusCompleted}, page, pageSize)
			if err != nil {
				log.Error().Err(err).Str("sep", sep).Msg("reconcile: failed to list transactions")
				break
			}
			for i := range txs {
				if txs[i].Kind == sap.KindWithdrawal {
					r.checkCompleted(ctx, &txs[i])
				}
			}
			if len(txs) < pageSize {
				break
			}
		}
	}
	if err := r.checkIncoming(ctx); err != nil {
		log.Error().Err(err).Msg("reconcile: failed to scan incoming payments")
	}
}

func (r *Reconciler) checkCompleted(ctx context.Context, tx *sap.Transaction) {
	w, err := r.proc.svc.store.Get(ctx, tx.ID)
	if errors.Is(err, ErrNotFound) {
		r.proc.alerts.Alert(ctx, "reconciliation mismatch", tx.ID+": completed in SAP but no withdrawal record")
		return
	}
	if err != nil {
		log.Error().Err(err).Str("tx", tx.ID).Msg("reconcile: failed to load withdrawal")
		return
	}
	if w.Reconcile != ReconcilePending {
		return
	}

	problems, err := r.compare(ctx, tx, w)
	if err != nil {
		log.Error().Err(err).Str("tx", tx.ID).Msg("reconcile: check failed, will retry")
		return
	}

	w.Reconcile, w.ReconcileNote = ReconcileOK, ""
	if len(problems) > 0 {
		w.Reconcile, w.ReconcileNote = ReconcileMismatch, strings.Join(problems, "; ")
		r.proc.alerts.Alert(ctx, "reconciliation mismatch", tx.ID+": "+w.ReconcileNote)
	}
	if err := r.proc.save(ctx, w); err != nil {
		log.Error().Err(err).Str("tx", tx.ID).Msg("reconcile: failed to save result")
		return
	}
	r.proc.svc.audit.Record(ctx, audit.Entry{Action: "reconcile." + w.Reconcile, TransactionID: tx.ID,
		Detail: map[string]string{"note": w.ReconcileNote}})
}

func (r *Reconciler) compare(ctx context.Context, tx *sap.Transaction, w *Withdrawal) ([]string, error) {
	var problems []string

	onLedger, err := r.proc.checkFunds(ctx, tx, w)
	if err != nil {
		return nil, err
	}
	if onLedger != "" {
		problems = append(problems, onLedger)
	}

	if tx.AmountOut == nil || !money.Equal(tx.AmountOut.Amount, w.AmountOut) {
		problems = append(problems, "amount_out in SAP does not match the withdrawal record")
	}

	res, err := r.proc.svc.core.Get(ctx, tx.ID)
	switch {
	case errors.Is(err, payout.ErrNotFound):
		problems = append(problems, "no payout in core")
	case err != nil:
		return nil, err
	default:
		if res.Status != payout.StatusCompleted {
			problems = append(problems, "payout status is "+res.Status)
		}
		if !money.Equal(res.Amount, w.AmountOut) {
			problems = append(problems, fmt.Sprintf("payout amount %s, expected %s", res.Amount, w.AmountOut))
		}
		if res.Reference != tx.ExternalTransactionID {
			problems = append(problems, "payout reference does not match SAP external_transaction_id")
		}
	}
	return problems, nil
}

// checkIncoming walks new payments to the distribution account and alerts on
// any whose memo does not belong to a withdrawal.
func (r *Reconciler) checkIncoming(ctx context.Context) error {
	cursor, err := r.cursors.Cursor(ctx)
	if err != nil {
		return err
	}
	for page := 0; page < maxReconcilePages; page++ {
		payments, next, err := r.proc.chain.IncomingPayments(ctx, r.proc.distribution, cursor, 200)
		if err != nil {
			return err
		}
		for _, p := range payments {
			if p.From == r.proc.distribution {
				continue
			}
			if p.Memo == "" {
				r.unmatched(ctx, p, "payment has no memo")
				continue
			}
			if _, err := r.proc.svc.store.FindByStellarMemo(ctx, p.Memo); errors.Is(err, ErrNotFound) {
				r.unmatched(ctx, p, "no withdrawal has memo "+p.Memo)
			} else if err != nil {
				return err
			}
		}
		if next == cursor {
			return nil
		}
		cursor = next
		if err := r.cursors.SetCursor(ctx, cursor); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) unmatched(ctx context.Context, p chain.Payment, why string) {
	r.proc.alerts.Alert(ctx, "unmatched payment", fmt.Sprintf("%s %s from %s in tx %s: %s", p.Amount, p.Asset, p.From, p.TxHash, why))
	r.proc.svc.audit.Record(ctx, audit.Entry{Action: "payment.unmatched",
		Detail: map[string]string{"tx_hash": p.TxHash, "amount": p.Amount, "asset": p.Asset, "reason": why}})
}

type MongoCursorStore struct {
	coll *mongo.Collection
}

func NewMongoCursorStore(db *mongo.Database) *MongoCursorStore {
	return &MongoCursorStore{coll: db.Collection("reconcile_state")}
}

func (s *MongoCursorStore) Cursor(ctx context.Context) (string, error) {
	var doc struct {
		Cursor string `bson:"cursor"`
	}
	err := s.coll.FindOne(ctx, bson.M{"_id": "incoming_payments"}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", nil
	}
	return doc.Cursor, err
}

func (s *MongoCursorStore) SetCursor(ctx context.Context, cursor string) error {
	_, err := s.coll.UpdateOne(ctx, bson.M{"_id": "incoming_payments"},
		bson.M{"$set": bson.M{"cursor": cursor}}, options.Update().SetUpsert(true))
	return err
}
