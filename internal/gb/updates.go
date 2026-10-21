package gb

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// updateFields is what one update check asks about a submission: where its page
// is, when it last changed, and the files it offers. The address is asked for
// as well as the number, so that a reply can be matched to the submission it
// belongs to rather than trusted to arrive in order.
const updateFields = "Url().sProfileUrl(),mdate,Files().aFiles()"

// updateBatch is how many submissions one request may ask about. The endpoint
// pairs its parameters by index and answers all of them at once.
const updateBatch = 50

// UpdateSnapshot is what a check learns about one submission: when its page
// last changed, and every file it offers, the archived ones included.
//
// The files matter rather than only the page's date, because editing a
// description bumps that date while the files the user installed may be the
// same. An installed file that is still there and was uploaded before the mod
// was installed is what says there is nothing to do.
type UpdateSnapshot struct {
	// Updated is the page's last modification. It is the zero time when the
	// API did not say, which is also when a page is judged only by its files.
	Updated time.Time
	// Files are the submission's downloads, current and archived, in the order
	// the API gives them.
	Files []File
}

// Updates asks about several submissions of one kind in one request, and
// returns a snapshot for each, aligned with ids. A submission the API no longer
// serves -- taken down, made private, or never there -- has no snapshot.
//
// This is the one request that carries information about more than one
// submission, and it lives on the older API host rather than beside the rest.
func (c *Client) Updates(ctx context.Context, kind Kind, ids []int) ([]*UpdateSnapshot, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > updateBatch {
		return nil, fmt.Errorf("one update check asks about %d submissions at most, not %d", updateBatch, len(ids))
	}

	q := url.Values{}
	for _, id := range ids {
		q.Add("itemtype[]", string(kind))
		q.Add("itemid[]", strconv.Itoa(id))
		q.Add("fields[]", updateFields)
	}

	var reply jsontext.Value
	if err := c.getFrom(ctx, c.LegacyBase, "Core/Item/Data?"+q.Encode(), &reply); err != nil {
		return nil, err
	}
	records, err := updateRecords(reply)
	if err != nil {
		return nil, err
	}
	// A record is placed by the page it names rather than by the position it
	// arrived in: the API leaves out a submission that has been taken down, so
	// a reply can be shorter than the request, and a mod the user still has
	// installed must not make the whole check fail.
	index := make(map[int]int, len(ids))
	for i, id := range ids {
		index[id] = i
	}
	snapshots := make([]*UpdateSnapshot, len(ids))
	for _, record := range records {
		id, ok := updateRecordID(record, kind)
		if !ok {
			continue
		}
		i, wanted := index[id]
		if !wanted {
			continue
		}
		snapshots[i] = updateSnapshot(record, kind, id)
	}
	return snapshots, nil
}

// updateRecordID reads the submission a record is about out of the page its
// first field names.
func updateRecordID(record jsontext.Value, kind Kind) (int, bool) {
	var fields []jsontext.Value
	if err := json.Unmarshal(record, &fields); err != nil || len(fields) == 0 {
		return 0, false
	}
	var page string
	if err := json.Unmarshal(fields[0], &page); err != nil {
		return 0, false
	}
	u, err := url.Parse(page)
	if err != nil || u.Scheme != "https" || u.Host != "gamebanana.com" {
		return 0, false
	}
	prefix := "/" + submissionSection(kind) + "/"
	number, ok := strings.CutPrefix(u.Path, prefix)
	if !ok {
		return 0, false
	}
	id, err := strconv.Atoi(number)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// updateRecords unwraps a reply. One submission is answered as its three fields
// rather than as a list of one, which is the shape that has to be told apart:
// the first field of a record is its page's address, a string, while the first
// element of a list of records is an array.
func updateRecords(reply jsontext.Value) ([]jsontext.Value, error) {
	var records []jsontext.Value
	if err := json.Unmarshal(reply, &records); err != nil {
		return nil, fmt.Errorf("gamebanana Core/Item/Data: reading the reply: %w", err)
	}
	if len(records) == 3 && records[0].Kind() == jsontext.KindString {
		return []jsontext.Value{reply}, nil
	}
	return records, nil
}

// updateSnapshot reads one submission's record. It is nil when the record is
// not the submission that was asked about, which is what a reply that drifted
// out of order or a submission that is gone looks like.
func updateSnapshot(record jsontext.Value, kind Kind, id int) *UpdateSnapshot {
	var fields []jsontext.Value
	if err := json.Unmarshal(record, &fields); err != nil || len(fields) < 3 {
		return nil
	}

	var page string
	if err := json.Unmarshal(fields[0], &page); err != nil || !isSubmissionPage(page, kind, id) {
		return nil
	}

	var modified int64
	// A page the API reports no date for is judged by its files alone, which is
	// what a zero comes to.
	_ = json.Unmarshal(fields[1], &modified)

	return &UpdateSnapshot{Updated: unix(modified), Files: updateFiles(fields[2])}
}

// isSubmissionPage reports whether an address is the page of one submission.
// The number alone is not enough: the three kinds are numbered separately, so
// mod 42 and sound 42 are two pages.
func isSubmissionPage(page string, kind Kind, id int) bool {
	u, err := url.Parse(page)
	if err != nil {
		return false
	}
	return u.Scheme == "https" && u.Host == "gamebanana.com" &&
		u.Path == "/"+submissionSection(kind)+"/"+strconv.Itoa(id)
}

// updateFiles reads a record's files, which the API sends either as a list or,
// when it has several, as an object keyed by file id. A file that cannot be
// read is left out rather than made to hide the rest.
func updateFiles(raw jsontext.Value) []File {
	var entries []jsontext.Value
	switch raw.Kind() {
	case jsontext.KindBeginArray:
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil
		}
	case jsontext.KindBeginObject:
		var byID map[string]jsontext.Value
		if err := json.Unmarshal(raw, &byID); err != nil {
			return nil
		}
		// Sorted, so that the same reply always reads the same way.
		for _, key := range slices.Sorted(maps.Keys(byID)) {
			entries = append(entries, byID[key])
		}
	default:
		return nil
	}

	files := make([]File, 0, len(entries))
	for _, entry := range entries {
		var f fileJSON
		if err := json.Unmarshal(entry, &f); err != nil || f.ID <= 0 {
			continue
		}
		files = append(files, f.file())
	}
	return files
}
