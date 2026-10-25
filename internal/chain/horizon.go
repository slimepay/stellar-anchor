// Package chain reads payments from Horizon so the Business Server can check
// SAP's view of a transaction against the ledger.
package chain

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/stellar/go-stellar-sdk/clients/horizonclient"
	"github.com/stellar/go-stellar-sdk/protocols/horizon/base"
	"github.com/stellar/go-stellar-sdk/protocols/horizon/operations"
)

var ErrNotFound = errors.New("transaction not found on the ledger")

// Payment is a payment operation, with the asset in SAP's format
// (stellar:native or stellar:CODE:ISSUER).
type Payment struct {
	ID     string
	TxHash string
	From   string
	To     string
	Asset  string
	Amount string
	Memo   string
}

type Transaction struct {
	Hash       string
	Successful bool
	Memo       string
	Payments   []Payment
}

type Reader interface {
	Transaction(ctx context.Context, hash string) (*Transaction, error)
	// IncomingPayments lists successful payments sent to account after cursor,
	// oldest first, and the cursor to continue from.
	IncomingPayments(ctx context.Context, account, cursor string, limit int) ([]Payment, string, error)
}

type Horizon struct {
	client *horizonclient.Client
}

func NewHorizon(url string) *Horizon {
	return &Horizon{client: &horizonclient.Client{
		HorizonURL: url,
		HTTP:       &http.Client{Timeout: 20 * time.Second},
	}}
}

func (h *Horizon) Transaction(_ context.Context, hash string) (*Transaction, error) {
	tx, err := h.client.TransactionDetail(hash)
	if horizonclient.IsNotFoundError(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	page, err := h.client.Payments(horizonclient.OperationRequest{ForTransaction: hash, Limit: 200, IncludeFailed: true})
	if err != nil {
		return nil, err
	}
	out := &Transaction{Hash: tx.Hash, Successful: tx.Successful, Memo: tx.Memo}
	for _, op := range page.Embedded.Records {
		if p, ok := op.(operations.Payment); ok {
			out.Payments = append(out.Payments, toPayment(p, tx.Memo))
		}
	}
	return out, nil
}

func (h *Horizon) IncomingPayments(_ context.Context, account, cursor string, limit int) ([]Payment, string, error) {
	page, err := h.client.Payments(horizonclient.OperationRequest{
		ForAccount: account,
		Cursor:     cursor,
		Order:      horizonclient.OrderAsc,
		Limit:      uint(limit),
		Join:       "transactions",
	})
	if err != nil {
		return nil, "", err
	}
	var out []Payment
	next := cursor
	for _, op := range page.Embedded.Records {
		next = op.PagingToken()
		p, ok := op.(operations.Payment)
		if !ok || !p.TransactionSuccessful || p.To != account {
			continue
		}
		memo := ""
		if p.Transaction != nil {
			memo = p.Transaction.Memo
		}
		out = append(out, toPayment(p, memo))
	}
	return out, next, nil
}

func toPayment(p operations.Payment, memo string) Payment {
	return Payment{
		ID:     p.ID,
		TxHash: p.TransactionHash,
		From:   p.From,
		To:     p.To,
		Asset:  AssetID(p.Asset),
		Amount: p.Amount,
		Memo:   memo,
	}
}

func AssetID(a base.Asset) string {
	if a.Type == "native" {
		return "stellar:native"
	}
	return "stellar:" + a.Code + ":" + a.Issuer
}
