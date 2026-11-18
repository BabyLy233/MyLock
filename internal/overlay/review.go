// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/manager/localization/review.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa. See
// ../../third_party/deadlock-mod-manager/NOTICE.md.

package overlay

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	portoverlay "github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/overlay"
)

// ReviewName is the file beside a package that records the player's choices
// for conflicting localization tokens and compiled-data rows.
const ReviewName = "compatibility-review.json"

// ReviewRevision invalidates saved reviews whose answers this build may read
// differently. It is independent of Revision: changing repair rules does not
// necessarily change the questions a review answers.
const ReviewRevision uint32 = 1

// Resolution is one reviewed choice. The shape matches the original
// application's saved review so a review can be read after a rebuild.
type Resolution struct {
	ConflictKey     string  `json:"conflictKey"`
	WinnerModID     *string `json:"winnerModId,omitempty"`
	WinnerValue     *string `json:"winnerValue,omitempty"`
	WinnerSourceVPK *string `json:"winnerSourceVpk,omitempty"`
	UseVanilla      bool    `json:"useVanilla,omitempty"`
}

// ReviewInputs identifies exactly what a review answered. Every field must
// match for the saved choices to be reused.
type ReviewInputs struct {
	Revision uint32 `json:"revision"`
	// Subject is a digest of the findings that paused for review, without
	// absolute load priorities, so unrelated mods moving in the load order do
	// not reopen it.
	Subject string `json:"subject"`
	// Mods holds the stamps of the mods the findings name.
	Mods map[string][]InputStamp `json:"mods"`
	// BaseGame is the game input the package was built against.
	BaseGame []InputStamp `json:"baseGame"`
	// HistoryDigest fingerprints the learned game-data history, which can
	// change how a compiled table is merged without changing a stamp.
	HistoryDigest string `json:"historyDigest,omitempty"`
}

// Review is a saved set of answers and the inputs it covers.
type Review struct {
	Inputs      ReviewInputs `json:"inputs"`
	Resolutions []Resolution `json:"resolutions"`
}

// Matches reports whether this review answered exactly inputs.
func (r *Review) Matches(inputs ReviewInputs) bool {
	if r == nil || r.Inputs.Revision != inputs.Revision ||
		r.Inputs.Subject != inputs.Subject ||
		r.Inputs.HistoryDigest != inputs.HistoryDigest ||
		len(r.Inputs.Mods) != len(inputs.Mods) ||
		!slices.Equal(r.Inputs.BaseGame, inputs.BaseGame) {
		return false
	}
	for id, stamps := range inputs.Mods {
		if !slices.Equal(r.Inputs.Mods[id], stamps) {
			return false
		}
	}
	return true
}

// ReviewPath is the saved review beside a package.
func ReviewPath(vpkPath string) string {
	return filepath.Join(filepath.Dir(vpkPath), ReviewName)
}

// LoadReview reads the review beside vpkPath. A missing or unreadable file
// means the player has not reviewed these inputs, so the questions are asked
// again; it is not an error.
func LoadReview(vpkPath string) (*Review, error) {
	data, err := os.ReadFile(ReviewPath(vpkPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading compatibility review: %w", err)
	}
	var review Review
	if err := json.Unmarshal(data, &review); err != nil {
		return nil, nil
	}
	if review.Inputs.Revision != ReviewRevision {
		return nil, nil
	}
	return &review, nil
}

// SaveReview writes the review atomically beside vpkPath.
func SaveReview(vpkPath string, review Review) error {
	data, err := json.MarshalIndent(review, "", "  ")
	if err != nil {
		return fmt.Errorf("recording compatibility review: %w", err)
	}
	data = append(data, '\n')
	return portoverlay.WriteAtomic(ReviewPath(vpkPath), data)
}

// RemoveReview deletes the saved review. A missing file is fine.
func RemoveReview(vpkPath string) error {
	if err := os.Remove(ReviewPath(vpkPath)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing compatibility review: %w", err)
	}
	return nil
}

// ReviewModStamps picks the stamps of the mods a review names, in mod order.
func ReviewModStamps(key Key, modIDs map[string]struct{}) map[string][]InputStamp {
	stamps := make(map[string][]InputStamp, len(modIDs))
	for _, mod := range key.Mods {
		if _, involved := modIDs[mod.ModID]; !involved {
			continue
		}
		stamps[mod.ModID] = slices.Clone(mod.Files)
	}
	return stamps
}