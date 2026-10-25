package withdrawal

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"aidmedium/stellar-anchor/internal/secure"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	ErrNotFound = errors.New("withdrawal not found")
	ErrExists   = errors.New("withdrawal already exists")
)

const (
	ReconcilePending  = ""
	ReconcileOK       = "ok"
	ReconcileMismatch = "mismatch"
)

// Withdrawal is the Business Server's record of one SAP withdrawal: what the
// user asked for, where the naira goes, and what happened to the payout.
type Withdrawal struct {
	TransactionID string
	Sep           string
	Account       string
	Memo          string
	CustomerID    string
	Asset         string
	AmountIn      string
	AmountOut     string
	Rate          string
	BankCode      string
	AccountNumber string
	AccountName   string
	StellarMemo   string
	PayoutRef     string
	PayoutStatus  string
	Reconcile     string
	ReconcileNote string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Store interface {
	Create(ctx context.Context, w *Withdrawal) error
	Get(ctx context.Context, transactionID string) (*Withdrawal, error)
	Update(ctx context.Context, w *Withdrawal) error
	FindByStellarMemo(ctx context.Context, memo string) (*Withdrawal, error)
}

type document struct {
	TransactionID string    `bson:"_id"`
	Sep           string    `bson:"sep"`
	Account       string    `bson:"account"`
	Memo          string    `bson:"memo"`
	CustomerID    string    `bson:"customer_id"`
	Asset         string    `bson:"asset"`
	AmountIn      string    `bson:"amount_in"`
	AmountOut     string    `bson:"amount_out"`
	Rate          string    `bson:"rate"`
	BankCode      string    `bson:"bank_code"`
	Sealed        []byte    `bson:"sealed"`
	StellarMemo   string    `bson:"stellar_memo,omitempty"`
	PayoutRef     string    `bson:"payout_ref,omitempty"`
	PayoutStatus  string    `bson:"payout_status,omitempty"`
	Reconcile     string    `bson:"reconcile,omitempty"`
	ReconcileNote string    `bson:"reconcile_note,omitempty"`
	CreatedAt     time.Time `bson:"created_at"`
	UpdatedAt     time.Time `bson:"updated_at"`
}

type bankDetails struct {
	AccountNumber string `json:"account_number"`
	AccountName   string `json:"account_name"`
}

type MongoStore struct {
	coll *mongo.Collection
	box  *secure.Box
}

func NewMongoStore(ctx context.Context, db *mongo.Database, box *secure.Box) (*MongoStore, error) {
	coll := db.Collection("withdrawals")
	_, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "stellar_memo", Value: 1}},
		Options: options.Index().SetSparse(true),
	})
	if err != nil {
		return nil, err
	}
	return &MongoStore{coll: coll, box: box}, nil
}

func (s *MongoStore) Create(ctx context.Context, w *Withdrawal) error {
	doc, err := s.toDoc(w)
	if err != nil {
		return err
	}
	_, err = s.coll.InsertOne(ctx, doc)
	if mongo.IsDuplicateKeyError(err) {
		return ErrExists
	}
	return err
}

func (s *MongoStore) Get(ctx context.Context, transactionID string) (*Withdrawal, error) {
	return s.findOne(ctx, bson.M{"_id": transactionID})
}

func (s *MongoStore) FindByStellarMemo(ctx context.Context, memo string) (*Withdrawal, error) {
	return s.findOne(ctx, bson.M{"stellar_memo": memo})
}

func (s *MongoStore) Update(ctx context.Context, w *Withdrawal) error {
	doc, err := s.toDoc(w)
	if err != nil {
		return err
	}
	res, err := s.coll.ReplaceOne(ctx, bson.M{"_id": w.TransactionID}, doc)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *MongoStore) findOne(ctx context.Context, filter bson.M) (*Withdrawal, error) {
	var doc document
	err := s.coll.FindOne(ctx, filter).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	plain, err := s.box.Open(doc.Sealed, []byte(doc.TransactionID))
	if err != nil {
		return nil, err
	}
	var bank bankDetails
	if err := json.Unmarshal(plain, &bank); err != nil {
		return nil, err
	}
	return &Withdrawal{
		TransactionID: doc.TransactionID,
		Sep:           doc.Sep,
		Account:       doc.Account,
		Memo:          doc.Memo,
		CustomerID:    doc.CustomerID,
		Asset:         doc.Asset,
		AmountIn:      doc.AmountIn,
		AmountOut:     doc.AmountOut,
		Rate:          doc.Rate,
		BankCode:      doc.BankCode,
		AccountNumber: bank.AccountNumber,
		AccountName:   bank.AccountName,
		StellarMemo:   doc.StellarMemo,
		PayoutRef:     doc.PayoutRef,
		PayoutStatus:  doc.PayoutStatus,
		Reconcile:     doc.Reconcile,
		ReconcileNote: doc.ReconcileNote,
		CreatedAt:     doc.CreatedAt,
		UpdatedAt:     doc.UpdatedAt,
	}, nil
}

func (s *MongoStore) toDoc(w *Withdrawal) (*document, error) {
	plain, err := json.Marshal(bankDetails{AccountNumber: w.AccountNumber, AccountName: w.AccountName})
	if err != nil {
		return nil, err
	}
	sealed, err := s.box.Seal(plain, []byte(w.TransactionID))
	if err != nil {
		return nil, err
	}
	return &document{
		TransactionID: w.TransactionID,
		Sep:           w.Sep,
		Account:       w.Account,
		Memo:          w.Memo,
		CustomerID:    w.CustomerID,
		Asset:         w.Asset,
		AmountIn:      w.AmountIn,
		AmountOut:     w.AmountOut,
		Rate:          w.Rate,
		BankCode:      w.BankCode,
		Sealed:        sealed,
		StellarMemo:   w.StellarMemo,
		PayoutRef:     w.PayoutRef,
		PayoutStatus:  w.PayoutStatus,
		Reconcile:     w.Reconcile,
		ReconcileNote: w.ReconcileNote,
		CreatedAt:     w.CreatedAt,
		UpdatedAt:     w.UpdatedAt,
	}, nil
}
