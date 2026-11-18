// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager localization_overlay camera pipeline at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package camera

import (
	"errors"
	"sort"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/modeltext"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
)

// Analysis describes the camera issues found in one model.
type Analysis struct {
	MissingFields []string
	MissingPoints []string
	OutdatedFrame bool
	MixedFrame    bool
	Repairable    bool
	RepairKinds   []RepairKind
	Warnings      []WarningKind
}

// Repair chains the attachment-frame migration and the camera interface or
// control repair. It returns the repaired bytes and the repairs applied.
// changed is false when nothing could be repaired safely.
func Repair(source, base []byte, options ModelOptions) (out []byte, kinds []RepairKind, warnings []WarningKind, changed bool) {
	input := source
	if frameOut, frameChanged, err := RepairFrame(source, base); err != nil {
		warnings = append(warnings, WarningOutdatedAttachments)
	} else if frameChanged {
		input = frameOut
		kinds = append(kinds, RepairAttachmentFrame)
		changed = true
	}
	if modelOut, kind, ok := RepairModelCameraWith(input, base, options); ok {
		input = modelOut
		kinds = append(kinds, kind)
		changed = true
	}
	return input, kinds, warnings, changed
}

// Analyze inspects a model without writing anything. It runs the repair in
// memory to decide whether a safe fix exists.
func Analyze(source, base []byte, options ModelOptions) Analysis {
	var analysis Analysis
	_, model, _, err := DecodeCompiled(source)
	if err != nil {
		return analysis
	}
	_, vanilla, _, err := DecodeCompiled(base)
	if err != nil {
		return analysis
	}
	analysis.MissingFields = MissingCameraFields(&model, &vanilla)

	if needs, err := PlanFrame(source, base); err != nil {
		if errors.Is(err, ErrMixedFrame) {
			analysis.MixedFrame = true
		} else {
			analysis.Warnings = append(analysis.Warnings, WarningOutdatedAttachments)
		}
	} else {
		analysis.OutdatedFrame = needs
	}

	if oldText, ok := modelText(&model); ok {
		cameraText := oldText
		if containsString(analysis.MissingFields, "AttachmentCameraData") {
			if currentText, ok := modelText(&vanilla); ok {
				cameraText = currentText
			}
		}
		if _, vanillaTextOK := modelText(&vanilla); vanillaTextOK {
			res, err := resource.Parse(base)
			if err == nil {
				if baseRaw, ok := res.FindBlock(mdat); ok {
					if baseMesh, err := kv3.Decode(baseRaw); err == nil {
						if names, ok := cameraPointNames(cameraText, &baseMesh); ok {
							analysis.MissingPoints = missingPointsForModel(source, names)
						}
					}
				}
			}
		}
	}

	out, kinds, warnings, changed := Repair(source, base, options)
	_ = out
	analysis.Warnings = append(analysis.Warnings, warnings...)
	if changed {
		analysis.Repairable = true
		analysis.RepairKinds = kinds
		return analysis
	}
	if len(analysis.MissingFields) > 0 {
		analysis.Warnings = append(analysis.Warnings, WarningMissingInterface)
	}
	if len(analysis.MissingPoints) > 0 {
		analysis.Warnings = append(analysis.Warnings, WarningMissingAttachments)
	}
	if analysis.OutdatedFrame || analysis.MixedFrame {
		already := false
		for _, w := range analysis.Warnings {
			if w == WarningOutdatedAttachments {
				already = true
			}
		}
		if !already {
			analysis.Warnings = append(analysis.Warnings, WarningOutdatedAttachments)
		}
	}
	return analysis
}

func missingPointsForModel(source []byte, names []string) []string {
	res, err := resource.Parse(source)
	if err != nil {
		return nil
	}
	missing := make(map[string]struct{})
	for index, block := range res.Blocks() {
		if block.Kind != mdat {
			continue
		}
		raw, ok := res.GetBlockByIndex(index)
		if !ok {
			continue
		}
		mesh, err := kv3.Decode(raw)
		if err != nil {
			continue
		}
		if _, ok := mesh.Get("m_attachments"); !ok {
			continue
		}
		for _, name := range names {
			if !hasPoint(&mesh, name) {
				missing[name] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(missing))
	for name := range missing {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// FindCameraText is exposed for tests and the analysis layer.
func FindCameraText(model *kv3.Value) (string, bool) { return modelText(model) }

// ParseRootFields is exposed for tests and the analysis layer.
func ParseRootFields(text string) (int, map[string]modeltext.Range, bool) {
	return modeltext.RootFields(text)
}
