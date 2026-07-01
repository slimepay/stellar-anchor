package sep6

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type Store interface {
	Insert(ctx context.Context, tx *Transaction) error
	FindByIDAndAccount(ctx context.Context, id, account string) (*Transaction, error)
}

type MongoStore struct {
	col *mongo.Collection
}

func NewMongoStore(db *mongo.Database) *MongoStore {
	return &MongoStore{col: db.Collection("sep6_transactions")}
}

func (s *MongoStore) Insert(ctx context.Context, tx *Transaction) error {
	if tx.ID == "" {
		tx.ID = uuid.New().String()
	}
	if tx.StartedAt.IsZero() {
		tx.StartedAt = time.Now().UTC()
	}
	_, err := s.col.InsertOne(ctx, tx)
	return err
}

func (s *MongoStore) FindByIDAndAccount(ctx context.Context, id, account string) (*Transaction, error) {
	var tx Transaction
	err := s.col.FindOne(ctx, bson.M{"_id": id, "stellar_account": account}).Decode(&tx)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	return &tx, err
}
