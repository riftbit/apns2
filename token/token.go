package token

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/ioutil"
	"sync"
	"time"

	jwt "github.com/dgrijalva/jwt-go"
)

const (
	// TokenTimeout is the period of time in seconds that a token is valid for.
	// If the timestamp for token issue is not within the last hour, APNs
	// rejects subsequent push messages. This is set to under an hour so that
	// we generate a new token before the existing one expires.
	TokenTimeout = 3000
)

// Possible errors when parsing a .p8 file.
var (
	ErrAuthKeyNotPem   = errors.New("token: AuthKey must be a valid .p8 PEM file")
	ErrAuthKeyNotECDSA = errors.New("token: AuthKey must be of type ecdsa.PrivateKey")
	ErrAuthKeyNil      = errors.New("token: AuthKey was nil")
)

// Token represents an Apple Provider Authentication Token (JSON Web Token).
type Token struct {
	sync.Mutex
	AuthKey  *ecdsa.PrivateKey
	KeyID    string
	TeamID   string
	IssuedAt int64
	Bearer   string
}

// AuthKeyFromFile loads a .p8 certificate from a local file and returns a
// *ecdsa.PrivateKey.
func AuthKeyFromFile(filename string) (*ecdsa.PrivateKey, error) {
	bytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return AuthKeyFromBytes(bytes)
}

// AuthKeyFromBytes loads a .p8 certificate from an in memory byte array and
// returns an *ecdsa.PrivateKey.
func AuthKeyFromBytes(bytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(bytes)
	if block == nil {
		return nil, ErrAuthKeyNotPem
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	switch pk := key.(type) {
	case *ecdsa.PrivateKey:
		return pk, nil
	default:
		return nil, ErrAuthKeyNotECDSA
	}
}

// GenerateIfExpired checks to see if the token is about to expire and
// generates a new token.
func (t *Token) GenerateIfExpired() (bool, error) {
	t.Lock()
	defer t.Unlock()
	if t.Expired() {
		return t.Generate()
	}
	return false, nil
}

// Expired checks to see if the token has expired.
func (t *Token) Expired() bool {
	return time.Now().Unix() >= (t.IssuedAt + TokenTimeout)
}

// Generate creates a new token.
func (t *Token) Generate() (bool, error) {
	if t.AuthKey == nil {
		return false, ErrAuthKeyNil
	}
	issuedAt := time.Now().Unix()
	jwtToken := &jwt.Token{
		Header: map[string]interface{}{
			"alg": "ES256",
			"kid": t.KeyID,
		},
		Claims: jwt.MapClaims{
			"iss": t.TeamID,
			"iat": issuedAt,
		},
		Method: jwt.SigningMethodES256,
	}
	bearer, err := jwtToken.SignedString(t.AuthKey)
	if err != nil {
		return false, err
	}
	t.IssuedAt = issuedAt
	t.Bearer = bearer
	return true, nil
}

// Manager ...
type Manager struct {
	mu    sync.Mutex
	token map[interface{}]*Token
}

// NewTokenManager ...
func NewTokenManager() *Manager {
	return &Manager{
		token: make(map[interface{}]*Token),
	}
}

// IsExist ...
func (c *Manager) IsExist(key interface{}) bool {
	c.mu.Lock()
	_, ok := c.token[key]
	c.mu.Unlock()
	return ok
}

// Get ...
func (c *Manager) Get(key interface{}) (*Token, bool) {
	c.mu.Lock()

	val, ok := c.token[key]
	if !ok || val == nil {
		delete(c.token, key)
		return nil, false
	}

	changed, err := val.GenerateIfExpired()
	if err != nil {
		delete(c.token, key)
		return nil, false
	}

	if changed {
		c.token[key] = val
	}

	c.mu.Unlock()
	return val, ok
}

// Set ...
func (c *Manager) Set(key interface{}, value *Token) {
	c.mu.Lock()
	c.token[key] = value
	c.mu.Unlock()
}

// Remove ...
func (c *Manager) Remove(key interface{}, value *Token) {
	c.mu.Lock()
	delete(c.token, key)
	c.mu.Unlock()
}

// RegenerateAllIfExpired ...
func (c *Manager) RegenerateAllIfExpired() error {
	var err error
	if c.token != nil {
		for key, tokenData := range c.token {
			tokenData.Lock()
			_, err := tokenData.GenerateIfExpired()
			if err != nil {
				err = fmt.Errorf("%v - %v", key, err)
				break
			}
			c.token[key] = tokenData
			tokenData.Unlock()
		}
	}
	return err
}
