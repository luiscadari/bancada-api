package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPasswordHash(t *testing.T) {
	hash, err := HashPassword("correta-123")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$"))

	ok, err := VerifyPassword("correta-123", hash)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = VerifyPassword("errada", hash)
	require.NoError(t, err)
	assert.False(t, ok)

	other, _ := HashPassword("correta-123")
	assert.NotEqual(t, hash, other, "salt must be random")

	_, err = VerifyPassword("x", "$bcrypt$whatever")
	assert.ErrorIs(t, err, ErrInvalidHash)
}

func TestAccessToken(t *testing.T) {
	iss := NewIssuer("secret-secret-secret-secret-secret", time.Minute)
	user, account, device := uuid.New(), uuid.New(), uuid.New()

	tok, exp, err := iss.Issue(user, account, &device)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(time.Minute), exp, 2*time.Second)

	claims, err := iss.Parse(tok)
	require.NoError(t, err)
	got, _ := claims.UserID()
	assert.Equal(t, user, got)
	assert.Equal(t, account, claims.AccountID)
	assert.Equal(t, device, *claims.DeviceID)

	// Wrong secret.
	_, err = NewIssuer("another-secret-another-secret-xx", time.Minute).Parse(tok)
	assert.ErrorIs(t, err, ErrInvalidToken)

	// Expired.
	iss.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	_, err = iss.Parse(tok)
	assert.ErrorIs(t, err, ErrInvalidToken)
	iss.now = time.Now

	// "none" algorithm is refused.
	unsigned, _ := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{AccountID: account,
		RegisteredClaims: jwt.RegisteredClaims{Subject: user.String(), Issuer: issuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	_, err = iss.Parse(unsigned)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestSecrets(t *testing.T) {
	secret, hash, err := NewSecret()
	require.NoError(t, err)
	assert.Len(t, secret, 43)
	assert.Equal(t, hash, HashSecret(secret))
	other, _, _ := NewSecret()
	assert.NotEqual(t, secret, other)
}
