package internal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// override ConfigPath for the duration of a test
func withConfigPath(t *testing.T, path string) {
	t.Helper()
	orig := ConfigPath
	ConfigPath = path
	t.Cleanup(func() { ConfigPath = orig })
}

// userremoteconfigfromfile

func TestUserRemoteConfigFromFile_Valid(t *testing.T) {
	dir := t.TempDir()
	cfg := UserRemoteConfig{User: "u", Hostname: "h", PrivKeyPath: "/k", Port: 22, StorageRoot: "/s"}
	raw, _ := json.Marshal(cfg)
	p := filepath.Join(dir, "wsftp.conf")
	os.WriteFile(p, raw, 0o644)

	withConfigPath(t, p)
	got, err := UserRemoteConfigFromFile()
	if err != nil {
		t.Fatal(err)
	}
	if got.User != "u" || got.Hostname != "h" || got.Port != 22 || got.StorageRoot != "/s" {
		t.Errorf("unexpected config: %+v", got)
	}
}

func TestUserRemoteConfigFromFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "wsftp.conf")
	os.WriteFile(p, []byte("{bad"), 0o644)

	withConfigPath(t, p)
	if _, err := UserRemoteConfigFromFile(); err == nil {
		t.Fatal("expected error")
	}
}

func TestUserRemoteConfigFromFile_Missing(t *testing.T) {
	withConfigPath(t, "/no/such/wsftp.conf")
	if _, err := UserRemoteConfigFromFile(); err == nil {
		t.Fatal("expected error")
	}
}

// writetofile

func TestWriteToFile_CreatesFileAndDir(t *testing.T) {
	// target a nested dir that doesn't exist yet to verify mkdirall
	p := filepath.Join(t.TempDir(), "sub", "wsftp.conf")
	withConfigPath(t, p)

	cfg := &UserRemoteConfig{User: "u", Hostname: "h", PrivKeyPath: "/k", Port: 2222, StorageRoot: "/r"}
	if err := cfg.WriteToFile(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("config file not created: %v", err)
	}
}

func TestUserRemoteConfig_WriteToFile_RoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wsftp.conf")
	withConfigPath(t, p)

	want := &UserRemoteConfig{User: "alice", Hostname: "srv", PrivKeyPath: "/id_ed25519", Port: 22, StorageRoot: "/data"}
	if err := want.WriteToFile(); err != nil {
		t.Fatal(err)
	}
	got, err := UserRemoteConfigFromFile()
	if err != nil {
		t.Fatal(err)
	}
	if got.User != want.User || got.Hostname != want.Hostname || got.Port != want.Port ||
		got.StorageRoot != want.StorageRoot || got.PrivKeyPath != want.PrivKeyPath {
		t.Errorf("round-trip mismatch:\n got:  %+v\n want: %+v", got, want)
	}
}

// newremoteconn — compact string parsing and stat error (pre-i/o error paths only)

func TestNewRemoteConn_MissingAt(t *testing.T) {
	withConfigPath(t, filepath.Join(t.TempDir(), "absent.conf"))
	if _, err := NewRemoteConn("userhostname:22:/s", "/k", "", false); err == nil {
		t.Fatal("expected error for missing '@'")
	}
}

func TestNewRemoteConn_MissingPortAndRoot(t *testing.T) {
	withConfigPath(t, filepath.Join(t.TempDir(), "absent.conf"))
	if _, err := NewRemoteConn("u@hostname", "/k", "", false); err == nil {
		t.Fatal("expected error for missing port and storage_root")
	}
}

func TestNewRemoteConn_InvalidPort(t *testing.T) {
	withConfigPath(t, filepath.Join(t.TempDir(), "absent.conf"))
	if _, err := NewRemoteConn("u@hostname:notaport:/s", "/k", "", false); err == nil {
		t.Fatal("expected error for non-numeric port")
	}
}

func TestNewRemoteConn_StatError(t *testing.T) {
	restricted := filepath.Join(t.TempDir(), "noaccess")
	if err := os.Mkdir(restricted, 0o000); err != nil {
		t.Skip("cannot create restricted dir")
	}
	t.Cleanup(func() { os.Chmod(restricted, 0o755) })
	withConfigPath(t, filepath.Join(restricted, "wsftp.conf"))

	if _, err := NewRemoteConn("u@h:22:/s", "/k", "", false); err == nil {
		t.Fatal("expected error for unreadable config path")
	}
}
