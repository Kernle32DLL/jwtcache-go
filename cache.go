package jwt

import (
	"github.com/dgrijalva/jwt-go"
	"github.com/sirupsen/logrus"

	"errors"
	"fmt"
	"time"
)

// Cache is a simple caching implementation to reuse JWTs till they expire.
type Cache struct {
	jwt      string
	validity time.Time

	name      string
	logger    *logrus.Logger
	headroom  time.Duration
	tokenFunc func() (string, error)
}

// NewCache returns a new JWT cache.
func NewCache(opts ...Option) *Cache {
	//default
	config := &config{
		name:     "",
		headroom: time.Second,
		logger:   logrus.StandardLogger(),
		tokenFunc: func() (s string, e error) {
			return "", errors.New("not implemented")
		},
	}

	//apply opts
	for _, opt := range opts {
		opt(config)
	}

	return &Cache{
		name:      config.name,
		logger:    config.logger,
		headroom:  config.headroom,
		tokenFunc: config.tokenFunc,
	}
}

type config struct {
	name      string
	logger    *logrus.Logger
	headroom  time.Duration
	tokenFunc func() (string, error)
}

// Option represents an option for the cache.
type Option func(*config)

// Name sets the name of the cache.
// The default is an empty string.
func Name(name string) Option {
	return func(c *config) {
		c.name = name
	}
}

// Name sets the logger to be used.
// The default is the logrus default logger.
func Logger(logger *logrus.Logger) Option {
	return func(c *config) {
		c.logger = logger
	}
}

// Headroom sets the headroom on how much earlier the cached
// token should be considered expired.
// The default is 1 second.
func Headroom(headroom time.Duration) Option {
	return func(c *config) {
		c.headroom = headroom
	}
}

// TokenFunction set the function which is called to retrieve a new
// JWT when required.
// The default always returns an error with "not implemented".
func TokenFunction(tokenFunc func() (string, error)) Option {
	return func(c *config) {
		c.tokenFunc = tokenFunc
	}
}

// EnsureToken returns either the cached token if existing and still valid,
// or calls the internal token function to fetch a new token. If an error
// occurs in the latter case, it is passed trough.
func (jwtCache *Cache) EnsureToken() (string, error) {
	// Do we have a cached jwt, and its still valid?
	if jwtCache.jwt != "" && time.Now().Before(jwtCache.validity) {
		return jwtCache.jwt, nil
	}

	token, err := jwtCache.tokenFunc()
	if err != nil {
		return "", err
	}

	// Work with the parsed token - but don't fail, if we encounter an error
	parsedToken, _, err := new(jwt.Parser).ParseUnverified(token, &jwt.StandardClaims{})
	if err != nil {
		jwtCache.logger.Debugf("Error while parsing %s: %s", jwtCache.name, err)
		return token, nil
	}

	jwtCache.handleParsedToken(parsedToken)
	return token, nil
}

// EnsureTokenSafe returns either the cached token if existing and still valid,
// or calls the internal token function to fetch a new token. If an error
// occurs in the latter case, it is passed trough.
// In contrast to EnsureToken, this function also validates the received JWT.
func (jwtCache *Cache) EnsureTokenSafe(secret interface{}, method string) (string, error) {
	// Do we have a cached jwt, and its still valid?
	if jwtCache.jwt != "" && time.Now().Before(jwtCache.validity) {
		return jwtCache.jwt, nil
	}

	algMethod := jwt.GetSigningMethod(method)
	if algMethod == nil {
		return "", fmt.Errorf("unknown signing method: %s", method)
	}

	token, err := jwtCache.tokenFunc()
	if err != nil {
		return "", err
	}

	// Work with the parsed token
	parsedToken, err := jwt.ParseWithClaims(token, &jwt.StandardClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Don't forget to validate the alg is what you expect:
		if token.Method != jwt.GetSigningMethod(method) {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		// Note: In contrast to EnsureToken, we do not return the token, if we failed to parse or validate it!
		jwtCache.logger.Debugf("Error while parsing %s: %s", jwtCache.name, err)
		return "", err
	}

	jwtCache.handleParsedToken(parsedToken)
	return token, nil
}

func (jwtCache *Cache) handleParsedToken(parsedToken *jwt.Token) {
	// Note: According to https://tools.ietf.org/html/rfc7519,
	// a "NumericDate" is defined as a UTC unix timestamp.
	iat := parsedToken.Claims.(*jwt.StandardClaims).IssuedAt
	exp := parsedToken.Claims.(*jwt.StandardClaims).ExpiresAt

	if exp == 0 {
		jwtCache.jwt = ""
		jwtCache.logger.Infof("New %s received. exp header not set, so not caching", jwtCache.name)
	} else {
		// Cache the new token (and leave some headroom)
		jwtCache.jwt = parsedToken.Raw
		jwtCache.validity = time.Unix(exp, 0).Add(-jwtCache.headroom)

		if iat != 0 {
			jwtCache.logger.Debugf("New %s received. Caching for %s", jwtCache.name, jwtCache.validity.Sub(time.Unix(iat, 0).Add(-jwtCache.headroom)))
		} else {
			jwtCache.logger.Debugf("New %s received. Caching till %s", jwtCache.name, jwtCache.validity.Add(-jwtCache.headroom))
		}
	}
}