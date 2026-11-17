package overlay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSuffixAndPaths(t *testing.T) {
	if got := Suffix(nil); got != "default" {
		t.Fatalf("default suffix = %q", got)
	}
	folder := "My Profile"
	sum := sha256.Sum256([]byte(folder))
	want := hex.EncodeToString(sum[:8])
	if got := Suffix(&folder); got != want {
		t.Fatalf("suffix = %q, want %q", got, want)
	}
	if got := SearchPath(nil); got != "citadel/dmm_localization_default" {
		t.Fatalf("search path = %q", got)
	}
	if got := SearchPath(&folder); got != "citadel/dmm_localization_"+want {
		t.Fatalf("search path = %q", got)
	}
	dir := t.TempDir()
	vpkPath := VPKPath(dir, nil)
	if vpkPath != filepath.Join(dir, "dmm_localization_default", "pak01_dir.vpk") {
		t.Fatalf("vpk path = %q", vpkPath)
	}
	if got := ReceiptPath(vpkPath); got != filepath.Join(dir, "dmm_localization_default", "compatibility.json") {
		t.Fatalf("receipt path = %q", got)
	}
}

func TestReceiptRequiresKeyRevisionGeneratorAndBytes(t *testing.T) {
	dir := t.TempDir()
	vpkPath := filepath.Join(dir, VPKName)
	if err := os.WriteFile(vpkPath, []byte("overlay"), 0o644); err != nil {
		t.Fatal(err)
	}
	key := json.RawMessage(`{"mods":[{"modId":"a","files":[]}],"gameBuild":7}`)
	hash, err := Digest(vpkPath)
	if err != nil || hash == nil {
		t.Fatal(err)
	}
	if err := Save(vpkPath, Receipt{
		Generator:   "mylock",
		Revision:    1,
		Key:         key,
		OverlayHash: hash,
	}); err != nil {
		t.Fatal(err)
	}
	receipt, err := Load(vpkPath, 1, "mylock")
	if err != nil || receipt == nil {
		t.Fatalf("load = %v, %v", receipt, err)
	}
	current, err := receipt.IsCurrent(vpkPath, json.RawMessage("{\n  \"mods\": [{\"modId\": \"a\", \"files\": []}],\n  \"gameBuild\": 7\n}"))
	if err != nil || !current {
		t.Fatalf("current = %v, %v", current, err)
	}
	other, err := receipt.IsCurrent(vpkPath, json.RawMessage(`{"gameBuild":8}`))
	if err != nil || other {
		t.Fatalf("other key current = %v, %v", other, err)
	}
	if err := os.WriteFile(vpkPath, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if current, err := receipt.IsCurrent(vpkPath, key); err != nil || current {
		t.Fatalf("corrupt overlay current = %v, %v", current, err)
	}
	if receipt, err := Load(vpkPath, 2, "mylock"); err != nil || receipt != nil {
		t.Fatalf("revision mismatch = %v, %v", receipt, err)
	}
	if receipt, err := Load(vpkPath, 1, "other"); err != nil || receipt != nil {
		t.Fatalf("generator mismatch = %v, %v", receipt, err)
	}
	if err := os.WriteFile(ReceiptPath(vpkPath), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if receipt, err := Load(vpkPath, 1, "mylock"); err != nil || receipt != nil {
		t.Fatalf("corrupt receipt = %v, %v", receipt, err)
	}
}

func TestRemoveOverlayAndReceipt(t *testing.T) {
	dir := t.TempDir()
	vpkPath := filepath.Join(dir, VPKName)
	for _, path := range []string{vpkPath, ReceiptPath(vpkPath)} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := Remove(vpkPath); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{vpkPath, ReceiptPath(vpkPath)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists", path)
		}
	}
	if err := Remove(vpkPath); err != nil {
		t.Fatalf("second remove: %v", err)
	}
}

func TestWriteAtomicReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "file.json")
	if err := WriteAtomic(path, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(path, []byte("two")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "two" {
		t.Fatalf("read = %q, %v", data, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("staged files left behind: %v, %v", entries, err)
	}
}
