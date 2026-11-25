package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const issuer = "bancada-api"

// Claims are the access token claims.
type Claims struct {
	AccountID uuid.UUID  `json:"account_id"`
	DeviceID  *uuid.UUID `json:"device_id,omitempty"`
	jwt.RegisteredClaims
}

// UserID returns the subject as a UUID.
func (c Claims) UserID() (uuid.UUID, error) { return uuid.Parse(c.Subject) }

type Issuer struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

func NewIssuer(secret string, ttl time.Duration) *Issuer {
	return &Issuer{secret: []byte(secret), ttl: ttl, now: time.Now}
}

// TTL is the access token lifetime.
func (i *Issuer) TTL() time.Duration { return i.ttl }

// Issue signs a short-lived access token (HS256).
func (i *Issuer) Issue(userID, accountID uuid.UUID, deviceID *uuid.UUID) (string, time.Time, error) {
	now := i.now()
	exp := now.Add(i.ttl)
	claims := Claims{
		AccountID: accountID,
		DeviceID:  deviceID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        uuid.NewString(),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(i.secret)
	return signed, exp, err
}

var ErrInvalidToken = errors.New("invalid access token")

// Parse validates an access token and returns its claims.
func (i *Issuer) Parse(token string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		return i.secret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(i.now),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if _, err := claims.UserID(); err != nil || claims.AccountID == uuid.Nil {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// NewSecret returns a random opaque secret (32 bytes, URL-safe) and its SHA-256 hash.
// Only the hash is stored.
func NewSecret() (secret, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	secret = base64.RawURLEncoding.EncodeToString(b)
	return secret, HashSecret(secret), nil
}

// HashSecret hashes an opaque secret for storage and lookup.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
