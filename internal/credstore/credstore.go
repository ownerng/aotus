// Package credstore keeps API keys in the operating system's credential store
// (macOS Keychain, Windows Credential Manager, the Secret Service on Linux)
// and nowhere else: never in the database, in a file, in a log or in an event.
//
// It is a leaf. Only the daemon's composition root uses it; providers receive
// a function that returns a key, so they cannot store one.
package credstore

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// service is the name under which entries are filed in the credential store.
const service = "aotus"

// ErrNotFound means there is no key stored under that reference.
var ErrNotFound = errors.New("credstore: no key stored for this reference")

// APIKeyRef is the reference under which a profile's API key is stored. The
// reference is not secret and is what Profile.APIKeyRef holds.
func APIKeyRef(profileID string) string { return "profile/" + profileID + "/api-key" }

// Store is the operating system credential store.
type Store struct{}

// Get returns the key stored under ref.
func (Store) Get(ref string) (string, error) {
	v, err := keyring.Get(service, ref)
	switch {
	case errors.Is(err, keyring.ErrNotFound):
		return "", ErrNotFound
	case err != nil:
		return "", fmt.Errorf("credstore: reading %s: %w", ref, err)
	}
	return v, nil
}

// Set stores a key under ref, replacing any previous one.
func (Store) Set(ref, value string) error {
	if value == "" {
		return errors.New("credstore: refusing to store an empty key")
	}
	if err := keyring.Set(service, ref, value); err != nil {
		return fmt.Errorf("credstore: storing %s: %w", ref, err)
	}
	return nil
}

// Delete removes the key stored under ref. Deleting a missing key is not an
// error.
func (Store) Delete(ref string) error {
	if err := keyring.Delete(service, ref); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("credstore: deleting %s: %w", ref, err)
	}
	return nil
}
