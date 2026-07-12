// Package store implements the bbolt-backed persistence layer for Decoyd tokens.
// All tokens are JSON-serialized and stored in a single "tokens" bucket keyed
// by the token's ID.  The store is safe for concurrent reads but serializes
// writes through bbolt's internal locking.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/arjunjaincs/decoyd/internal/tokens"
)

// ErrNotFound is returned when the requested token ID does not exist.
var ErrNotFound = errors.New("token not found")

var tokenBucket = []byte("tokens")

// ----------------------------------------------------------------------------
// Store
// ----------------------------------------------------------------------------

// Store wraps a bbolt database and exposes CRUD operations for tokens.
type Store struct {
	db *bolt.DB
}

// Open opens (or creates) the bbolt database at dbPath.
// The "tokens" bucket is created if it does not already exist.
// Callers must call Close() when finished.
func Open(dbPath string) (*Store, error) {
	db, err := bolt.Open(dbPath, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open store %q: %w", dbPath, err)
	}

	// Ensure the bucket exists.
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(tokenBucket)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init tokens bucket: %w", err)
	}

	return &Store{db: db}, nil
}

// Close releases the bbolt file lock.
func (s *Store) Close() error {
	return s.db.Close()
}

// ----------------------------------------------------------------------------
// CRUD
// ----------------------------------------------------------------------------

// SaveToken persists t to the store. If a record with t.ID already exists it
// is overwritten (upsert semantics).
func (s *Store) SaveToken(t tokens.Token) error {
	if t.ID == "" {
		return errors.New("save token: ID must not be empty")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(tokenBucket)
		data, err := json.Marshal(t)
		if err != nil {
			return fmt.Errorf("marshal token: %w", err)
		}
		return b.Put([]byte(t.ID), data)
	})
}

// GetToken retrieves the token with the given id.
// Returns ErrNotFound if no such token exists.
func (s *Store) GetToken(id string) (tokens.Token, error) {
	var t tokens.Token
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(tokenBucket)
		data := b.Get([]byte(id))
		if data == nil {
			return ErrNotFound
		}
		return json.Unmarshal(data, &t)
	})
	return t, err
}

// ListTokens returns all tokens in insertion order (bbolt iterates keys in
// byte-sorted order, which for hex IDs is effectively random — callers that
// need a specific order must sort the result themselves).
func (s *Store) ListTokens() ([]tokens.Token, error) {
	var ts []tokens.Token
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(tokenBucket)
		return b.ForEach(func(_, v []byte) error {
			var t tokens.Token
			if err := json.Unmarshal(v, &t); err != nil {
				return fmt.Errorf("unmarshal token: %w", err)
			}
			ts = append(ts, t)
			return nil
		})
	})
	return ts, err
}

// UpdateToken is identical to SaveToken (bbolt is an upsert store).
// It exists as a named alias to signal intent at the call site.
func (s *Store) UpdateToken(t tokens.Token) error {
	return s.SaveToken(t)
}

// DeleteToken removes the token with the given id. It is a no-op if the id
// does not exist (consistent with bbolt's Delete semantics).
func (s *Store) DeleteToken(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(tokenBucket).Delete([]byte(id))
	})
}

// ListByType returns all tokens whose Type matches tokenType.
func (s *Store) ListByType(tokenType string) ([]tokens.Token, error) {
	all, err := s.ListTokens()
	if err != nil {
		return nil, err
	}
	var out []tokens.Token
	for _, t := range all {
		if t.Type == tokenType {
			out = append(out, t)
		}
	}
	return out, nil
}
