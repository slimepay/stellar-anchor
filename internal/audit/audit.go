// Package audit records who did what to customers and transactions. Entries
// never contain personal data, only identifiers and outcomes.
package audit

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/mongo"
)

type Entry struct {
	At            time.Time         `bson:"at"`
	Action        string            `bson:"action"`
	CustomerID    string            `bson:"customer_id,omitempty"`
	TransactionID string            `bson:"transaction_id,omitempty"`
	Detail        map[string]string `bson:"detail,omitempty"`
}

type Logger interface {
	Record(ctx context.Context, e Entry)
}

type MongoLogger struct {
	coll *mongo.Collection
}

func NewMongoLogger(db *mongo.Database) *MongoLogger {
	return &MongoLogger{coll: db.Collection("audit_log")}
}

func (l *MongoLogger) Record(ctx context.Context, e Entry) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	if _, err := l.coll.InsertOne(ctx, e); err != nil {
		log.Error().Err(err).Str("action", e.Action).Msg("failed to write audit entry")
	}
}

// Nop discards entries. Used in tests.
type Nop struct{}

func (Nop) Record(context.Context, Entry) {}
