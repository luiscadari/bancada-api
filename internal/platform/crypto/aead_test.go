package crypto

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSealer(t *testing.T) {
	s, err := NewSealer(bytes.Repeat([]byte{7}, 32))
	require.NoError(t, err)

	sealed, err := s.Seal([]byte("senha-do-notebook"), []byte("order-1"))
	require.NoError(t, err)
	assert.NotContains(t, string(sealed), "senha")

	plain, err := s.Open(sealed, []byte("order-1"))
	require.NoError(t, err)
	assert.Equal(t, "senha-do-notebook", string(plain))

	// Bound to its context: a ciphertext copied to another order does not open.
	_, err = s.Open(sealed, []byte("order-2"))
	assert.Error(t, err)

	_, err = NewSealer([]byte("short"))
	assert.Error(t, err)
}
