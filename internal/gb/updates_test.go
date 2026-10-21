package gb

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// updateFileJSON is one downloadable file in an update reply.
const updateFileJSON = `{
	"_idRow": %d,
	"_sFile": %q,
	"_nFilesize": %d,
	"_tsDateAdded": %d,
	"_sDownloadUrl": "https://gamebanana.com/dl/%d",
	"_sMd5Checksum": "ABC123",
	"_bIsArchived": %t
}`

func updateFileEntry(id int, name string, added int64, archived bool) string {
	return fmt.Sprintf(updateFileJSON, id, name, 1024*id, added, id, archived)
}

// A single submission is answered as its three fields rather than as a list of
// one, and its files are read from the array the API sends.
func TestUpdatesReadOneSubmission(t *testing.T) {
	files := "[" + updateFileEntry(7, "skin.zip", 1_700_000_000, false) + "]"
	c, s := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusOK, fmt.Sprintf(
			`["https://gamebanana.com/mods/42", %d, %s]`, 1_700_000_500, files)
	})
	c.LegacyBase = s.URL + "/"

	snapshots, err := c.Updates(t.Context(), Mod, []int{42})
	if err != nil {
		t.Fatal(err)
	}

	if len(snapshots) != 1 || snapshots[0] == nil {
		t.Fatalf("Updates = %+v, want one snapshot", snapshots)
	}
	snapshot := snapshots[0]
	if want := time.Unix(1_700_000_500, 0); !snapshot.Updated.Equal(want) {
		t.Errorf("Updated = %v, want %v", snapshot.Updated, want)
	}
	if len(snapshot.Files) != 1 {
		t.Fatalf("the snapshot holds %d files, want 1", len(snapshot.Files))
	}
	file := snapshot.Files[0]
	if file.ID != 7 || file.Name != "skin.zip" || file.Size != 1024*7 {
		t.Errorf("the file reads as %+v", file)
	}
	if want := time.Unix(1_700_000_000, 0); !file.Added.Equal(want) {
		t.Errorf("Added = %v, want %v", file.Added, want)
	}
	// The API writes its checksums in either case, and the download check
	// compares them without.
	if file.MD5 != "abc123" {
		t.Errorf("MD5 = %q, want abc123", file.MD5)
	}

	if len(s.requests) != 1 {
		t.Fatalf("the API was asked %d times, want 1", len(s.requests))
	}
	if got := s.paths[0]; got != "/Core/Item/Data" {
		t.Errorf("the request went to %q", got)
	}
	want := url.Values{
		"itemtype[]": {"Mod"},
		"itemid[]":   {"42"},
		"fields[]":   {updateFields},
	}
	if got := s.requests[0]; got.Encode() != want.Encode() {
		t.Errorf("the request was asked for %v, want %v", got, want)
	}
}

// Several submissions are answered as a list, and each is matched to the number
// that was asked about rather than trusted to arrive in order. Files may come
// as an object keyed by id, which the endpoint does when a submission has
// several.
func TestUpdatesReadSeveralSubmissions(t *testing.T) {
	first := "[" + updateFileEntry(1, "base.zip", 1_700_000_000, true) + "]"
	second := fmt.Sprintf(`{
		"2": %s,
		"3": %s
	}`,
		updateFileEntry(2, "voice.zip", 1_700_000_100, false),
		updateFileEntry(3, "extra.zip", 1_700_000_200, true))
	c, s := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusOK, fmt.Sprintf(`[
			["https://gamebanana.com/mods/10", 0, %s],
			["https://gamebanana.com/sounds/20", 0, %s],
			["https://gamebanana.com/mods/99", 0, []]
		]`, first, second)
	})
	c.LegacyBase = s.URL + "/"

	snapshots, err := c.Updates(t.Context(), Mod, []int{10, 20, 99})
	if err != nil {
		t.Fatal(err)
	}

	if len(snapshots) != 3 {
		t.Fatalf("Updates = %d snapshots, want 3", len(snapshots))
	}
	// The second record is a sound, not the mod that was asked about there.
	if snapshots[1] != nil {
		t.Errorf("snapshot 2 = %+v, want nil for a mismatched page", snapshots[1])
	}
	if snapshots[0] == nil || len(snapshots[0].Files) != 1 || !snapshots[0].Files[0].Archived {
		t.Errorf("snapshot 1 = %+v, want the archived base file", snapshots[0])
	}
	if snapshots[2] == nil || len(snapshots[2].Files) != 0 {
		t.Errorf("snapshot 3 = %+v, want a submission with no files", snapshots[2])
	}
}

// A sound pack is filed under its own section, and a reply naming the mod page
// of the same number is not its page.
func TestUpdatesMatchTheKindOfSubmission(t *testing.T) {
	c, s := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusOK, `[["https://gamebanana.com/mods/42", 0, []]]`
	})
	c.LegacyBase = s.URL + "/"

	snapshots, err := c.Updates(t.Context(), Sound, []int{42})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0] != nil {
		t.Errorf("Updates = %+v, want no snapshot for a mod page asked about as a sound", snapshots)
	}
}

// The API leaves out a submission that is gone, and the others are still
// matched to the numbers asked about rather than shifted up by one.
func TestUpdatesLeaveOutASubmissionThatIsGone(t *testing.T) {
	files := "[" + updateFileEntry(7, "skin.zip", 1_700_000_000, false) + "]"
	c, s := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusOK, fmt.Sprintf(
			`[["https://gamebanana.com/mods/43", 0, %s]]`, files)
	})
	c.LegacyBase = s.URL + "/"

	snapshots, err := c.Updates(t.Context(), Mod, []int{42, 43})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("Updates = %d snapshots, want 2", len(snapshots))
	}
	if snapshots[0] != nil {
		t.Errorf("snapshot 1 = %+v, want nil for the submission the API left out", snapshots[0])
	}
	if snapshots[1] == nil || len(snapshots[1].Files) != 1 {
		t.Errorf("snapshot 2 = %+v, want the submission that arrived", snapshots[1])
	}
}

// A reply whose records arrive in another order is still read against the
// numbers asked for.
func TestUpdatesMatchRecordsOutOfOrder(t *testing.T) {
	first := "[" + updateFileEntry(1, "base.zip", 1_700_000_000, false) + "]"
	second := "[" + updateFileEntry(2, "voice.zip", 1_700_000_100, false) + "]"
	c, s := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusOK, fmt.Sprintf(`[
			["https://gamebanana.com/mods/44", 0, %s],
			["https://gamebanana.com/mods/43", 0, %s]
		]`, second, first)
	})
	c.LegacyBase = s.URL + "/"

	snapshots, err := c.Updates(t.Context(), Mod, []int{43, 44})
	if err != nil {
		t.Fatal(err)
	}
	if snapshots[0] == nil || snapshots[0].Files[0].ID != 1 {
		t.Errorf("snapshot 1 = %+v, want the record for mod 43", snapshots[0])
	}
	if snapshots[1] == nil || snapshots[1].Files[0].ID != 2 {
		t.Errorf("snapshot 2 = %+v, want the record for mod 44", snapshots[1])
	}
}

// Asking about nothing makes no request at all.
func TestUpdatesWithNoSubmissions(t *testing.T) {
	c := New()
	snapshots, err := c.Updates(t.Context(), Mod, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 0 {
		t.Errorf("Updates = %+v, want none", snapshots)
	}
}
