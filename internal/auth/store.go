// Package auth stores the browser session credential used by the Luma CLI.
package auth

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var (
	ErrInvalidKey = errors.New("Session key must be shaped usr-<id>.<secret>.")
	ErrStore      = errors.New("Unable to access the local credential store securely.")
	keyPattern    = regexp.MustCompile(`^usr-[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)
)

// Validate checks the browser cookie format without contacting Luma.
func Validate(key string) error {
	if len(key) > 4096 || !keyPattern.MatchString(key) {
		return ErrInvalidKey
	}
	return nil
}

func storePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", ErrStore
	}
	return filepath.Join(dir, "luma", "session-key"), nil
}

// SessionKey reads the stored credential. An empty string and nil error mean
// no credential is stored. It does not read the environment or contact Luma.
// Errors never include credentials or filesystem paths.
func SessionKey() (string, error) {
	path, err := storePath()
	if err != nil {
		return "", err
	}
	if err := checkDirectory(filepath.Dir(path)); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return "", ErrStore
	}
	f, err := os.Open(path)
	if err != nil {
		return "", ErrStore
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Mode().Perm() != 0600 {
		return "", ErrStore
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return "", ErrStore
	}
	if err := Validate(string(data)); err != nil {
		return "", err
	}
	return string(data), nil
}

func checkDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return ErrStore
	}
	return nil
}

// Save atomically replaces the credential with a file restricted to mode 0600
// in the OS user config directory (luma/session-key).
func Save(key string) error {
	if err := Validate(key); err != nil {
		return err
	}
	path, err := storePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := checkDirectory(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ErrStore
	}
	if err := checkDirectory(dir); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".session-key-*")
	if err != nil {
		return ErrStore
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return ErrStore
	}
	if _, err := f.WriteString(key); err != nil {
		return ErrStore
	}
	if err := f.Sync(); err != nil {
		return ErrStore
	}
	if err := f.Close(); err != nil {
		return ErrStore
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return ErrStore
	}
	return nil
}

// Logout removes the stored credential; an absent credential is a success.
func Logout() error {
	path, err := storePath()
	if err != nil {
		return err
	}
	if err := checkDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrStore
	}
	return nil
}
