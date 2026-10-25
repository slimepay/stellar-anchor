package money

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	for _, s := range []string{"1", "0.5", "12.3456789", "1000000"} {
		_, err := Parse(s)
		assert.NoError(t, err, s)
	}
	for _, s := range []string{"", "-1", "1e3", "1/2", ".5", "5.", "abc", "1,000"} {
		_, err := Parse(s)
		assert.Error(t, err, s)
	}
}

func TestFormatRoundsDown(t *testing.T) {
	r, err := Parse("10.129")
	require.NoError(t, err)
	assert.Equal(t, "10.12", Format(r, 2))
	assert.Equal(t, "10.1290000", Format(r, 7))
	assert.Equal(t, "3.33", Format(big.NewRat(10, 3), 2))
}

func TestDecimals(t *testing.T) {
	assert.Equal(t, 0, Decimals("10"))
	assert.Equal(t, 2, Decimals("10.25"))
	assert.Equal(t, 7, Decimals("0.0000001"))
}

func TestEqual(t *testing.T) {
	assert.True(t, Equal("10", "10.0000000"))
	assert.False(t, Equal("10", "10.0000001"))
	assert.False(t, Equal("10", "bad"))
}
