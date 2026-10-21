package memo_test

import (
	"strconv"
	"testing"

	"aidmedium/stellar-anchor/internal/memo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewID_ValidMemoID(t *testing.T) {
	for i := 0; i < 1000; i++ {
		id, err := memo.NewID()
		require.NoError(t, err)

		n, err := strconv.ParseInt(id, 10, 64)
		require.NoError(t, err, "memo must fit in an int64")
		assert.Greater(t, n, int64(0))
	}
}

func TestNewID_Unique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id, err := memo.NewID()
		require.NoError(t, err)
		assert.False(t, seen[id], "memo collision: %s", id)
		seen[id] = true
	}
}
