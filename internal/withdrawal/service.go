// Package withdrawal runs the Business Server side of a withdrawal: pricing it,
// asking SAP for the on-chain payment, paying naira out through core and
// reconciling the result.
package withdrawal

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"aidmedium/stellar-anchor/internal/audit"
	"aidmedium/stellar-anchor/internal/customer"
	"aidmedium/stellar-anchor/internal/money"
	"aidmedium/stellar-anchor/internal/payout"
	"aidmedium/stellar-anchor/internal/sap"
)

const (
	AssetNGN         = "iso4217:NGN"
	AssetXLM         = "stellar:native"
	assetDecimals    = 7
	ngnDecimals      = 2
	minimumNGNPayout = "100"
)

type Platform interface {
	GetTransaction(ctx context.Context, id string) (*sap.Transaction, error)
	ListTransactions(ctx context.Context, sep string, statuses []string, page, pageSize int) ([]sap.Transaction, error)
	RequestOnchainFunds(ctx context.Context, p sap.RequestOnchainFunds) (*sap.Transaction, error)
	NotifyOffchainFundsPending(ctx context.Context, id, externalID, message string) error
	NotifyOffchainFundsSent(ctx context.Context, id, externalID, message string) error
	NotifyTransactionError(ctx context.Context, id, message string) error
	NotifyTransactionOnHold(ctx context.Context, id, message string) error
}

type Customers interface {
	Lookup(ctx context.Context, account, memo string) (*customer.Customer, error)
}

// UserError is a problem the customer can fix, shown to them as is.
type UserError struct{ Msg string }

func (e *UserError) Error() string { return e.Msg }

// ErrKYCIncomplete means the customer has not passed KYC yet. A SEP-6
// withdrawal waits for it instead of failing.
var ErrKYCIncomplete = &UserError{"complete identity verification first"}

func userErr(format string, args ...any) error {
	return &UserError{fmt.Sprintf(format, args...)}
}

type Quote struct {
	Asset     string
	AmountIn  string
	AmountOut string
	Rate      string
}

type Service struct {
	store     Store
	platform  Platform
	customers Customers
	core      payout.Core
	audit     audit.Logger
	rates     map[string]*big.Rat
	network   string
	now       func() time.Time
}

func NewService(store Store, platform Platform, customers Customers, core payout.Core, auditLog audit.Logger, network, usdcAsset string, ngnPerUSDC, ngnPerXLM *big.Rat) *Service {
	return &Service{
		store:     store,
		platform:  platform,
		customers: customers,
		core:      core,
		audit:     auditLog,
		rates:     map[string]*big.Rat{usdcAsset: ngnPerUSDC, AssetXLM: ngnPerXLM},
		network:   network,
		now:       time.Now,
	}
}

func (s *Service) Quote(asset, amount string) (*Quote, error) {
	rate, ok := s.rates[asset]
	if !ok {
		return nil, userErr("asset %s is not supported", asset)
	}
	in, err := money.Parse(amount)
	if err != nil || in.Sign() <= 0 {
		return nil, userErr("enter a valid amount")
	}
	if money.Decimals(amount) > assetDecimals {
		return nil, userErr("amount can have at most %d decimal places", assetDecimals)
	}
	out := new(big.Rat).Mul(in, rate)
	min, _ := money.Parse(minimumNGNPayout)
	if out.Cmp(min) < 0 {
		return nil, userErr("the naira amount must be at least NGN %s", minimumNGNPayout)
	}
	return &Quote{
		Asset:     asset,
		AmountIn:  money.Format(in, assetDecimals),
		AmountOut: money.Format(out, ngnDecimals),
		Rate:      rate.FloatString(ngnDecimals),
	}, nil
}

// Rate returns the naira rate for one unit of asset, or "" if it is not supported.
func (s *Service) Rate(asset string) string {
	if r, ok := s.rates[asset]; ok {
		return r.FloatString(ngnDecimals)
	}
	return ""
}

// TransactionAsset returns the Stellar asset the user is withdrawing.
func TransactionAsset(tx *sap.Transaction) string {
	for _, a := range []*sap.Amount{tx.AmountIn, tx.AmountExpected} {
		if a != nil && a.Asset != "" {
			return a.Asset
		}
	}
	return ""
}

// Start prices an incomplete withdrawal, records where the naira goes and asks
// SAP to wait for the user's on-chain payment. It is safe to call again for a
// transaction that was already started.
func (s *Service) Start(ctx context.Context, tx *sap.Transaction, account, memo, amount string) (*Withdrawal, error) {
	if tx.Kind != sap.KindWithdrawal {
		return nil, userErr("transaction is not a withdrawal")
	}
	if tx.Status != sap.StatusIncomplete {
		return nil, userErr("this withdrawal has already been started")
	}

	w, err := s.store.Get(ctx, tx.ID)
	switch {
	case errors.Is(err, ErrNotFound):
		if w, err = s.create(ctx, tx, account, memo, amount); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	}

	res, err := s.platform.RequestOnchainFunds(ctx, sap.RequestOnchainFunds{
		TransactionID:  tx.ID,
		Message:        "waiting for your payment",
		AmountIn:       &sap.Amount{Amount: w.AmountIn, Asset: w.Asset},
		AmountOut:      &sap.Amount{Amount: w.AmountOut, Asset: AssetNGN},
		FeeDetails:     &sap.FeeDetails{Total: "0", Asset: w.Asset},
		AmountExpected: &sap.Amount{Amount: w.AmountIn},
	})
	if err != nil {
		return nil, err
	}

	w.StellarMemo = res.Memo
	w.UpdatedAt = s.now().UTC()
	if err := s.store.Update(ctx, w); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{Action: "withdrawal.started", TransactionID: tx.ID, CustomerID: w.CustomerID,
		Detail: map[string]string{"amount_in": w.AmountIn, "asset": w.Asset, "amount_out": w.AmountOut}})
	return w, nil
}

func (s *Service) create(ctx context.Context, tx *sap.Transaction, account, memo, amount string) (*Withdrawal, error) {
	c, err := s.customers.Lookup(ctx, account, memo)
	if errors.Is(err, customer.ErrNotFound) {
		return nil, ErrKYCIncomplete
	}
	if err != nil {
		return nil, err
	}
	if c.Status != customer.StatusAccepted {
		return nil, ErrKYCIncomplete
	}
	bankCode, accountNumber := c.Fields["bank_number"], c.Fields["bank_account_number"]
	if bankCode == "" || accountNumber == "" {
		return nil, userErr("add your bank account details first")
	}

	q, err := s.Quote(TransactionAsset(tx), amount)
	if err != nil {
		return nil, err
	}

	name, err := s.core.ResolveAccount(ctx, bankCode, accountNumber)
	var rejected *payout.RejectedError
	if errors.As(err, &rejected) {
		return nil, userErr("the bank account could not be verified")
	}
	if err != nil {
		return nil, err
	}

	now := s.now().UTC()
	w := &Withdrawal{
		TransactionID: tx.ID,
		Sep:           tx.Sep,
		Account:       account,
		Memo:          memo,
		CustomerID:    c.ID,
		Asset:         q.Asset,
		AmountIn:      q.AmountIn,
		AmountOut:     q.AmountOut,
		Rate:          q.Rate,
		BankCode:      bankCode,
		AccountNumber: accountNumber,
		AccountName:   name,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.store.Create(ctx, w); errors.Is(err, ErrExists) {
		return s.store.Get(ctx, tx.ID)
	} else if err != nil {
		return nil, err
	}
	return w, nil
}
