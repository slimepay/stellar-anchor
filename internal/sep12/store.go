package sep12

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Store is the data access interface for SEP-12 KYC records.
// Tests use a mock implementation; production uses MongoStore.
type Store interface {
	FindByAccount(ctx context.Context, account string) (*Customer, error)
	Upsert(ctx context.Context, account string, fields UpsertFields) (id string, err error)
	Delete(ctx context.Context, account string) error
}

type UpsertFields struct {
	FirstName string
	LastName  string
	Email     string
	IDType    string
	IDNumber  string
}

// MongoStore is the production MongoDB-backed Store implementation.
type MongoStore struct {
	col *mongo.Collection
}

func NewMongoStore(db *mongo.Database) *MongoStore {
	return &MongoStore{col: db.Collection("sep12_customers")}
}

func (s *MongoStore) FindByAccount(ctx context.Context, account string) (*Customer, error) {
	var c Customer
	err := s.col.FindOne(ctx, bson.M{"stellar_account": account}).Decode(&c)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	return &c, err
}

func (s *MongoStore) Upsert(ctx context.Context, account string, fields UpsertFields) (string, error) {
	now := time.Now().UTC()
	newID := uuid.New().String()

	_, err := s.col.UpdateOne(ctx,
		bson.M{"stellar_account": account},
		bson.M{
			"$set": bson.M{
				"status":     StatusProcessing,
				"first_name": fields.FirstName,
				"last_name":  fields.LastName,
				"email":      fields.Email,
				"id_type":    fields.IDType,
				"id_number":  fields.IDNumber,
				"updated_at": now,
			},
			"$setOnInsert": bson.M{
				"_id":             newID,
				"stellar_account": account,
				"created_at":      now,
			},
		},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		return "", err
	}

	var existing struct {
		ID string `bson:"_id"`
	}
	_ = s.col.FindOne(ctx, bson.M{"stellar_account": account}).Decode(&existing)
	return existing.ID, nil
}

func (s *MongoStore) Delete(ctx context.Context, account string) error {
	_, err := s.col.DeleteOne(ctx, bson.M{"stellar_account": account})
	return err
}
