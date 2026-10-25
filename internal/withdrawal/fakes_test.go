package withdrawal

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"

	"aidmedium/stellar-anchor/internal/audit"
	"aidmedium/stellar-anchor/internal/chain"
	"aidmedium/stellar-anchor/internal/customer"
	"aidmedium/stellar-anchor/internal/payout"
	"aidmedium/stellar-anchor/internal/sap"
)

const (
	usdcIssuer   = "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5"
	assetUSDC    = "stellar:USDC:" + usdcIssuer
	distribution = "GDISTRIBUTION"
	user         = "GUSER"
)

type fakePlatform struct {
	mu      sync.Mutex
	txs     map[string]*sap.Transaction
	calls   []string
	request *sap.RequestOnchainFunds
	failOn  string
}

func newPlatform(txs ...*sap.Transaction) *fakePlatform {
	p := &fakePlatform{txs: map[string]*sap.Transaction{}}
	for _, tx := range txs {
		p.txs[tx.ID] = tx
	}
	return p
}

func (p *fakePlatform) record(method, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, method+":"+id)
	if p.failOn == method {
		return errors.New("platform unavailable")
	}
	return nil
}

func (p *fakePlatform) setStatus(id, status string) {
	if tx, ok := p.txs[id]; ok {
		tx.Status = status
	}
}

func (p *fakePlatform) GetTransaction(_ context.Context, id string) (*sap.Transaction, error) {
	tx, ok := p.txs[id]
	if !ok {
		return nil, &sap.RPCError{Code: -32600, Message: "not found"}
	}
	cp := *tx
	return &cp, nil
}

func (p *fakePlatform) ListTransactions(_ context.Context, sep string, statuses []string, page, _ int) ([]sap.Transaction, error) {
	if page > 0 {
		return nil, nil
	}
	var out []sap.Transaction
	for _, tx := range p.txs {
		for _, s := range statuses {
			if tx.Sep == sep && tx.Status == s {
				out = append(out, *tx)
			}
		}
	}
	return out, nil
}

func (p *fakePlatform) RequestOnchainFunds(_ context.Context, r sap.RequestOnchainFunds) (*sap.Transaction, error) {
	if err := p.record("request_onchain_funds", r.TransactionID); err != nil {
		return nil, err
	}
	p.request = &r
	p.setStatus(r.TransactionID, sap.StatusPendingUserTransferStart)
	return &sap.Transaction{ID: r.TransactionID, Status: sap.StatusPendingUserTransferStart, Memo: "777", MemoType: "id"}, nil
}

func (p *fakePlatform) NotifyOffchainFundsPending(_ context.Context, id, ref, _ string) error {
	if err := p.record("pending", id); err != nil {
		return err
	}
	p.setStatus(id, sap.StatusPendingExternal)
	p.txs[id].ExternalTransactionID = ref
	return nil
}

func (p *fakePlatform) NotifyOffchainFundsSent(_ context.Context, id, ref, _ string) error {
	if err := p.record("sent", id); err != nil {
		return err
	}
	p.setStatus(id, sap.StatusCompleted)
	p.txs[id].ExternalTransactionID = ref
	return nil
}

func (p *fakePlatform) NotifyTransactionError(_ context.Context, id, _ string) error {
	if err := p.record("error", id); err != nil {
		return err
	}
	p.setStatus(id, sap.StatusError)
	return nil
}

func (p *fakePlatform) NotifyTransactionOnHold(_ context.Context, id, _ string) error {
	if err := p.record("on_hold", id); err != nil {
		return err
	}
	p.setStatus(id, sap.StatusOnHold)
	return nil
}

type fakeCore struct {
	payouts     map[string]*payout.Payout
	transfers   int
	payStatus   string
	payErr      error
	rejectPay   bool
	rejectName  bool
	amountDrift string
	lastNetwork string
}

func newCore() *fakeCore {
	return &fakeCore{payouts: map[string]*payout.Payout{}, payStatus: payout.StatusCompleted}
}

func (c *fakeCore) Banks(context.Context) ([]payout.Bank, error) {
	return []payout.Bank{{Code: "000013", Name: "GTBank"}}, nil
}

func (c *fakeCore) ResolveAccount(context.Context, string, string) (string, error) {
	if c.rejectName {
		return "", &payout.RejectedError{Message: "account not found"}
	}
	return "ADA OBI", nil
}

func (c *fakeCore) Pay(_ context.Context, req payout.Request) (*payout.Payout, error) {
	c.lastNetwork = req.Network
	if c.payErr != nil {
		return nil, c.payErr
	}
	if c.rejectPay {
		return nil, &payout.RejectedError{Message: "invalid bank code"}
	}
	if p, ok := c.payouts[req.TransactionID]; ok {
		cp := *p
		return &cp, nil
	}
	c.transfers++
	amount := req.Amount
	if c.amountDrift != "" {
		amount = c.amountDrift
	}
	p := &payout.Payout{TransactionID: req.TransactionID, Status: c.payStatus, Reference: "ref-" + req.TransactionID, Amount: amount,
		BankCode: req.BankCode, AccountNumber: req.AccountNumber}
	c.payouts[req.TransactionID] = p
	cp := *p
	return &cp, nil
}

func (c *fakeCore) Get(_ context.Context, id string) (*payout.Payout, error) {
	p, ok := c.payouts[id]
	if !ok {
		return nil, payout.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

type fakeChain struct {
	txs      map[string]*chain.Transaction
	incoming []chain.Payment
	cursors  []string
}

func (f *fakeChain) Transaction(_ context.Context, hash string) (*chain.Transaction, error) {
	if tx, ok := f.txs[hash]; ok {
		return tx, nil
	}
	return nil, chain.ErrNotFound
}

func (f *fakeChain) IncomingPayments(_ context.Context, _ string, cursor string, _ int) ([]chain.Payment, string, error) {
	f.cursors = append(f.cursors, cursor)
	if cursor == "" && len(f.incoming) > 0 {
		return f.incoming, "page-1", nil
	}
	return nil, cursor, nil
}

type memStore struct {
	m map[string]Withdrawal
}

func (s *memStore) Create(_ context.Context, w *Withdrawal) error {
	if _, ok := s.m[w.TransactionID]; ok {
		return ErrExists
	}
	s.m[w.TransactionID] = *w
	return nil
}

func (s *memStore) Get(_ context.Context, id string) (*Withdrawal, error) {
	w, ok := s.m[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &w, nil
}

func (s *memStore) Update(_ context.Context, w *Withdrawal) error {
	if _, ok := s.m[w.TransactionID]; !ok {
		return ErrNotFound
	}
	s.m[w.TransactionID] = *w
	return nil
}

func (s *memStore) FindByStellarMemo(_ context.Context, memo string) (*Withdrawal, error) {
	for _, w := range s.m {
		if w.StellarMemo == memo {
			cp := w
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

type fakeCustomers map[string]*customer.Customer

func (f fakeCustomers) Lookup(_ context.Context, account, memo string) (*customer.Customer, error) {
	if c, ok := f[account+":"+memo]; ok {
		return c, nil
	}
	return nil, customer.ErrNotFound
}

type alerts struct{ got []string }

func (a *alerts) Alert(_ context.Context, subject, detail string) {
	a.got = append(a.got, fmt.Sprintf("%s: %s", subject, detail))
}

type env struct {
	platform  *fakePlatform
	core      *fakeCore
	chain     *fakeChain
	store     *memStore
	customers fakeCustomers
	alerts    *alerts
	svc       *Service
	proc      *Processor
}

func newEnv(txs ...*sap.Transaction) *env {
	e := &env{
		platform: newPlatform(txs...),
		core:     newCore(),
		chain:    &fakeChain{txs: map[string]*chain.Transaction{}},
		store:    &memStore{m: map[string]Withdrawal{}},
		customers: fakeCustomers{user + ":": {
			ID:     "cust-1",
			Status: customer.StatusAccepted,
			Fields: map[string]string{"bank_number": "000013", "bank_account_number": "0123456789"},
		}},
		alerts: &alerts{},
	}
	e.svc = NewService(e.store, e.platform, e.customers, e.core, audit.Nop{}, "testnet", assetUSDC, big.NewRat(1500, 1), big.NewRat(250, 1))
	e.proc = NewProcessor(e.svc, e.chain, e.alerts, distribution)
	return e
}

func incomplete(id, sep, asset string) *sap.Transaction {
	return &sap.Transaction{
		ID: id, Sep: sep, Kind: sap.KindWithdrawal, Status: sap.StatusIncomplete,
		AmountExpected: &sap.Amount{Asset: asset},
		Creator:        &sap.StellarID{Account: user},
	}
}

// funded moves a started withdrawal to pending_anchor as SAP's observer would,
// with a matching payment on the ledger.
func (e *env) funded(id, amount, memo string) {
	tx := e.platform.txs[id]
	w := e.store.m[id]
	hash := "hash-" + id
	tx.Status = sap.StatusPendingAnchor
	tx.AmountIn = &sap.Amount{Amount: w.AmountIn, Asset: w.Asset}
	tx.AmountOut = &sap.Amount{Amount: w.AmountOut, Asset: AssetNGN}
	tx.StellarTransactions = []sap.StellarTransaction{{ID: hash, Memo: memo, MemoType: "id"}}
	e.chain.txs[hash] = &chain.Transaction{Hash: hash, Successful: true, Memo: memo, Payments: []chain.Payment{
		{ID: "op-" + id, TxHash: hash, From: user, To: distribution, Asset: w.Asset, Amount: amount, Memo: memo},
	}}
}
