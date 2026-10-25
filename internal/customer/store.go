package customer

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

var ErrNotFound = errors.New("customer not found")

type Customer struct {
	ID      string
	Account string
	Memo    string
	Status  string
	Message string
	// Fields holds the SEP-9 values the customer provided.
	Fields map[string]string
	// Verified holds the identity fields as they were when KYC last ran, so a
	// change to any of them triggers a new check.
	Verified map[string]string
	// KYCPending is set when Dojah could not be reached and the check must be retried.
	KYCPending bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Store interface {
	Get(ctx context.Context, id string) (*Customer, error)
	FindByAccount(ctx context.Context, account, memo string) (*Customer, error)
	Save(ctx context.Context, c *Customer) error
	Delete(ctx context.Context, id string) error
}

type document struct {
	ID         string    `bson:"_id"`
	Account    string    `bson:"account"`
	Memo       string    `bson:"memo"`
	Status     string    `bson:"status"`
	Message    string    `bson:"message,omitempty"`
	KYCPending bool      `bson:"kyc_pending"`
	Sealed     []byte    `bson:"sealed"`
	CreatedAt  time.Time `bson:"created_at"`
	UpdatedAt  time.Time `bson:"updated_at"`
}

type sealedData struct {
	Fields   map[string]string `json:"fields"`
	Verified map[string]string `json:"verified,omitempty"`
}

// MongoStore keeps every SEP-9 value encrypted. Only the Stellar account,
// memo and KYC status are stored in the clear, since lookups need them.
type MongoStore struct {
	coll *mongo.Collection
	box  *secure.Box
}

func NewMongoStore(ctx context.Context, db *mongo.Database, box *secure.Box) (*MongoStore, error) {
	coll := db.Collection("customers")
	_, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "account", Value: 1}, {Key: "memo", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return nil, err
	}
	return &MongoStore{coll: coll, box: box}, nil
}

func (s *MongoStore) Get(ctx context.Context, id string) (*Customer, error) {
	return s.findOne(ctx, bson.M{"_id": id})
}

func (s *MongoStore) FindByAccount(ctx context.Context, account, memo string) (*Customer, error) {
	return s.findOne(ctx, bson.M{"account": account, "memo": memo})
}

func (s *MongoStore) findOne(ctx context.Context, filter bson.M) (*Customer, error) {
	var doc document
	err := s.coll.FindOne(ctx, filter).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	plain, err := s.box.Open(doc.Sealed, []byte(doc.ID))
	if err != nil {
		return nil, err
	}
	var data sealedData
	if err := json.Unmarshal(plain, &data); err != nil {
		return nil, err
	}
	return &Customer{
		ID:         doc.ID,
		Account:    doc.Account,
		Memo:       doc.Memo,
		Status:     doc.Status,
		Message:    doc.Message,
		Fields:     data.Fields,
		Verified:   data.Verified,
		KYCPending: doc.KYCPending,
		CreatedAt:  doc.CreatedAt,
		UpdatedAt:  doc.UpdatedAt,
	}, nil
}

func (s *MongoStore) Save(ctx context.Context, c *Customer) error {
	plain, err := json.Marshal(sealedData{Fields: c.Fields, Verified: c.Verified})
	if err != nil {
		return err
	}
	sealed, err := s.box.Seal(plain, []byte(c.ID))
	if err != nil {
		return err
	}
	doc := document{
		ID:         c.ID,
		Account:    c.Account,
		Memo:       c.Memo,
		Status:     c.Status,
		Message:    c.Message,
		KYCPending: c.KYCPending,
		Sealed:     sealed,
		CreatedAt:  c.CreatedAt,
		UpdatedAt:  c.UpdatedAt,
	}
	_, err = s.coll.ReplaceOne(ctx, bson.M{"_id": c.ID}, doc, options.Replace().SetUpsert(true))
	return err
}

func (s *MongoStore) Delete(ctx context.Context, id string) error {
	res, err := s.coll.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}
