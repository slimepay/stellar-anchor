package secure

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSealOpen(t *testing.T) {
	box, err := NewBox(bytes.Repeat([]byte{7}, 32))
	require.NoError(t, err)

	sealed, err := box.Seal([]byte("22222222222"), []byte("cust-1"))
	require.NoError(t, err)
	assert.NotContains(t, string(sealed), "22222222222")

	plain, err := box.Open(sealed, []byte("cust-1"))
	require.NoError(t, err)
	assert.Equal(t, "22222222222", string(plain))
}

func TestOpenRejectsOtherRecord(t *testing.T) {
	box, err := NewBox(bytes.Repeat([]byte{7}, 32))
	require.NoError(t, err)
	sealed, err := box.Seal([]byte("secret"), []byte("cust-1"))
	require.NoError(t, err)

	_, err = box.Open(sealed, []byte("cust-2"))
	assert.Error(t, err)
}

func TestOpenRejectsWrongKey(t *testing.T) {
	a, _ := NewBox(bytes.Repeat([]byte{1}, 32))
	b, _ := NewBox(bytes.Repeat([]byte{2}, 32))
	sealed, err := a.Seal([]byte("secret"), nil)
	require.NoError(t, err)

	_, err = b.Open(sealed, nil)
	assert.Error(t, err)
}

func TestNewBoxKeyLength(t *testing.T) {
	_, err := NewBox([]byte("short"))
	assert.Error(t, err)
}
