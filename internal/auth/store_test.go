package auth

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func isolateConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("APPDATA", dir)
}

func TestStoreLifecycle(t *testing.T) {
	isolateConfig(t)
	if key, err := SessionKey(); err != nil || key != "" {
		t.Fatalf("missing credential: %v", err)
	}
	for _, key := range []string{"usr-test.fake", "usr-other.replacement"} {
		if err := Save(key); err != nil {
			t.Fatal(err)
		}
		got, err := SessionKey()
		if err != nil || got != key {
			t.Fatal("credential did not round trip")
		}
		path, _ := storePath()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatal("credential file is not private")
		}
	}
	if err := Logout(); err != nil {
		t.Fatal(err)
	}
	if err := Logout(); err != nil {
		t.Fatal(err)
	}
	if key, err := SessionKey(); err != nil || key != "" {
		t.Fatalf("logout did not clear credential: %v", err)
	}
}

func TestRejectInvalidKeysWithoutChangingStore(t *testing.T) {
	isolateConfig(t)
	if err := Save("usr-test.fake"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "api-key", "usr-test.fake\n", "usr-test.fake;other=cookie", "usr-.secret", "usr-test."} {
		if !errors.Is(Save(key), ErrInvalidKey) {
			t.Fatal("accepted invalid credential")
		}
	}
	if key, err := SessionKey(); err != nil || key != "usr-test.fake" {
		t.Fatal("invalid save changed stored credential")
	}
}

func TestUnsafeStore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions and symlinks")
	}
	for _, kind := range []string{"permissions", "symlink", "directory", "corrupt", "unsafe parent"} {
		t.Run(kind, func(t *testing.T) {
			isolateConfig(t)
			if err := Save("usr-test.fake"); err != nil {
				t.Fatal(err)
			}
			path, _ := storePath()
			switch kind {
			case "permissions":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "unsafe parent":
				if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(path, []byte("bad credential"), 0600); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink" {
					if err := os.Symlink(filepath.Join(t.TempDir(), "target"), path); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if key, err := SessionKey(); err == nil || key != "" {
				t.Fatal("read unsafe credential")
			}
		})
	}
}

func TestStoreFailureIsRedacted(t *testing.T) {
	isolateConfig(t)
	path, _ := storePath()
	if err := os.WriteFile(filepath.Dir(path), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Save("usr-test.fake"); !errors.Is(err, ErrStore) {
		t.Fatal("expected safe store error")
	}
}
