package vdf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseNestedObjects(t *testing.T) {
	root, err := Parse([]byte(`
"libraryfolders"
{
	"0"
	{
		"path"		"/home/ada/.steam/steam"
		"label"		""
		"apps"
		{
			"1422450"		"12345678"
		}
	}
	"1"
	{
		"path"		"/mnt/games/SteamLibrary"
	}
}
`))
	if err != nil {
		t.Fatal(err)
	}

	folders := root.Get("libraryfolders")
	if folders == nil || !folders.IsObject() {
		t.Fatalf("libraryfolders is %+v, want an object", folders)
	}
	if got := len(folders.Children); got != 2 {
		t.Fatalf("%d library folders, want 2", got)
	}

	first := folders.Get("0")
	if got := first.GetString("path"); got != "/home/ada/.steam/steam" {
		t.Errorf("path = %q", got)
	}
	if got := first.GetString("label"); got != "" {
		t.Errorf("label = %q, want empty", got)
	}
	if got := first.Get("apps").GetString("1422450"); got != "12345678" {
		t.Errorf("the app's size = %q", got)
	}
	if got := folders.Get("1").GetString("path"); got != "/mnt/games/SteamLibrary" {
		t.Errorf("the second path = %q", got)
	}
}

// The older libraryfolders.vdf holds the paths as bare values.
func TestParseBareValues(t *testing.T) {
	root, err := Parse([]byte(`
"LibraryFolders"
{
	"TimeNextStatsReport"		"1700000000"
	"ContentStatsID"		"-1234"
	"1"		"/mnt/games"
	"2"		"D:\\SteamLibrary"
}
`))
	if err != nil {
		t.Fatal(err)
	}
	folders := root.Get("LibraryFolders")
	if got := folders.GetString("TimeNextStatsReport"); got != "1700000000" {
		t.Errorf("TimeNextStatsReport = %q", got)
	}
	if got := folders.GetString("1"); got != "/mnt/games" {
		t.Errorf("library 1 = %q", got)
	}
	if got := folders.GetString("2"); got != `D:\SteamLibrary` {
		t.Errorf("library 2 = %q", got)
	}
}

func TestParseEscapesAndComments(t *testing.T) {
	root, err := Parse([]byte(`
// a comment before the document
"AppState"
{
	"name"		"Dead\"lock"	// a trailing comment
	"path"		"C:\\Games\\Deadlock"
	"tab"		"a\tb"
	"note"		"line\nbreak"
}
`))
	if err != nil {
		t.Fatal(err)
	}
	state := root.Get("AppState")
	for key, want := range map[string]string{
		"name": `Dead"lock`,
		"path": `C:\Games\Deadlock`,
		"tab":  "a\tb",
		"note": "line\nbreak",
	} {
		if got := state.GetString(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestParseUnquotedTokens(t *testing.T) {
	root, err := Parse([]byte(`"AppState" { "buildid" 12345678 }`))
	if err != nil {
		t.Fatal(err)
	}
	if got := root.Get("AppState").GetString("buildid"); got != "12345678" {
		t.Errorf("buildid = %q", got)
	}
}

func TestParseBOM(t *testing.T) {
	root, err := Parse(append([]byte{0xEF, 0xBB, 0xBF}, []byte(`"a" "b"`)...))
	if err != nil {
		t.Fatal(err)
	}
	if got := root.GetString("a"); got != "b" {
		t.Errorf("a = %q, want b", got)
	}
}

func TestParseEmptyObject(t *testing.T) {
	root, err := Parse([]byte(`"a" { } "b" "c"`))
	if err != nil {
		t.Fatal(err)
	}
	if a := root.Get("a"); a == nil || !a.IsObject() || len(a.Children) != 0 {
		t.Errorf("a = %+v, want an empty object", a)
	}
	if got := root.GetString("b"); got != "c" {
		t.Errorf("b = %q, want c", got)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse([]byte(`"a" "unterminated`)); err == nil {
		t.Error("an unterminated string parsed without an error")
	}
}

func TestGetMissing(t *testing.T) {
	root, err := Parse([]byte(`"a" { "b" "c" }`))
	if err != nil {
		t.Fatal(err)
	}
	if got := root.Get("nope"); got != nil {
		t.Errorf("Get of a missing key = %+v, want nil", got)
	}
	if got := root.GetString("nope"); got != "" {
		t.Errorf("GetString of a missing key = %q, want empty", got)
	}
	if got := root.GetString("a"); got != "" {
		t.Errorf("GetString of an object = %q, want empty", got)
	}
}

func TestAll(t *testing.T) {
	root, err := Parse([]byte(`"r" { "k" "1" "k" "2" "other" "3" }`))
	if err != nil {
		t.Fatal(err)
	}
	all := root.Get("r").All("k")
	if len(all) != 2 || all[0].Value != "1" || all[1].Value != "2" {
		t.Errorf("All(k) = %+v, want 1 and 2", all)
	}
}

func TestParseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "libraryfolders.vdf")
	if err := os.WriteFile(path, []byte(`"libraryfolders" { "0" { "path" "/steam" } }`), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := root.Get("libraryfolders").Get("0").GetString("path"); got != "/steam" {
		t.Errorf("path = %q, want /steam", got)
	}

	if _, err := ParseFile(filepath.Join(t.TempDir(), "missing.vdf")); !os.IsNotExist(err) {
		t.Errorf("ParseFile of a missing file = %v, want a not-exist error", err)
	}
}
