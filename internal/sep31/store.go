package sep31

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type Store interface {
	Insert(ctx context.Context, tx *Transaction) error
	FindByID(ctx context.Context, id string) (*Transaction, error)
}

type MongoStore struct {
	col *mongo.Collection
}

func NewMongoStore(db *mongo.Database) *MongoStore {
	return &MongoStore{col: db.Collection("sep31_transactions")}
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

func (s *MongoStore) FindByID(ctx context.Context, id string) (*Transaction, error) {
	var tx Transaction
	err := s.col.FindOne(ctx, bson.M{"_id": id}).Decode(&tx)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	return &tx, err
}
