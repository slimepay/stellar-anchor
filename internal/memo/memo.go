package memo

import (
	"crypto/rand"
	"math"
	"math/big"
	"strconv"
)

// Type is the memo_type returned to wallets for incoming payments.
const Type = "id"

// NewID returns a random memo ID between 1 and MaxInt64-1, the same range
// Anchor Platform uses when it assigns memos itself.
func NewID() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(math.MaxInt64-1))
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(n.Int64()+1, 10), nil
}
