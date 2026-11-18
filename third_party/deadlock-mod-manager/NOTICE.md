# deadlock-mod-manager

This directory contains code and reference material taken from the original
Tauri application:

- Upstream: <https://github.com/deadlock-mod-manager/deadlock-mod-manager>
- Fork used as the baseline: <https://github.com/BabyLy233/deadlock-mod-manager>
- Baseline commit: `41afe0e706726f7c5882fb589f996b84362114fa`
- License: GNU General Public License v3.0 (see `LICENSE`)
- Vendored on: 2026-11-17

MyLock is distributed under GPL-3.0 because it reuses this code. The original
MIT-licensed third-party components that the upstream project vendors are
listed in `THIRD-PARTY-NOTICES.md`; their notices are kept there and in the
ported file headers.

## Layout

`upstream/` holds unmodified copies of the Rust sources used as the reference
for the Go port. They are not compiled; they exist so that the port can be
audited and replayed when the baseline moves.

`port/` holds the Go translation that is compiled into MyLock. Every ported
file records the source path and baseline commit in its header.

## Port mapping

| Upstream source | Go port |
| --- | --- |
| `packages/vpk-parser/src-rs/{parser,types,error}.rs` | `port/vpk/parse.go` |
| `packages/source2-model/src/vpk_extract.rs` | `port/vpk/archive.go` |
| `packages/vpkmanager/src/pack.rs` | `port/vpk/pack.go` |
| `packages/vpkmanager/src/source2/resource/{mod,header}.rs` | `port/resource/` |
| `packages/vpkmanager/src/source2/kv3/{types,reader,writer,rewrap}.rs` | `port/kv3/` |
| `packages/vpkmanager/src/source2/kv3/patch.rs` (camera subset) | `port/kv3/patch.go` |
| `localization_overlay/camera_attachment_frame.rs` | `port/camera/frame.go` |
| `localization_overlay/model_camera.rs` | `port/camera/model.go` |
| `localization_overlay/model_camera/contract.rs` | `port/camera/contract.go` |
| `localization_overlay/camera_controls.rs` | `port/camera/controls.go` |
| `localization_overlay/model_text.rs` | `port/modeltext/` |
| `localization_overlay/asset_compatibility.rs` (skeleton, remapping, mesh, reference, unloaded-model subset) | `port/compat/`, `internal/modcompat/analyze.go`, `internal/modcompat/references.go` |
| `localization_overlay/model_sounds.rs` | `port/modelsounds/` |
| `packages/vpkmanager/src/material_repair.rs` | `port/resource/rerl.go`, `port/material/` |
| `packages/vpkmanager/src/resource_id.rs` | `port/resource/id.go` |
| `localization_overlay/material_compatibility/interface.rs` | `port/compat/material.go` |
| `localization_overlay/material_compatibility.rs` (dependency validation, texture runtime equivalence) | `internal/modcompat/analyze.go`, `port/material/minion_glow.go` |
| `localization_overlay/material_compatibility/minion_glow.rs` | `port/material/minion_glow.go` |
| `localization_overlay/animation_skeleton.rs` (`equivalent_rigs`, `equivalent_layout`, `canonical_masks`, rebase path) | `port/animation/skeleton.go`, `port/animation/rebase.go`, `port/rig/` |
| `localization_overlay/animation_masks.rs` | `port/animation/masks.go` |
| `localization_overlay/animation_clips.rs` | `port/animation/clips.go`, `internal/modcompat/clips.go` |
| `localization_overlay/animation_clips/layout.rs`, `animation_clips/layout/tree.rs` | `port/animation/clips_layout.go`, `port/animation/clips_tree.go` |
| `localization_overlay/animation_model_evidence.rs` (rig subset) | `port/rig/` |
| `localization_overlay/animation_poses.rs` (reference-pose consistency and preserve) | `port/animation/proof.go`, `port/animation/rebase.go` |
| `localization_overlay/animation_contract.rs` (dependency closure subset) | `internal/modcompat/animation_contract.go` |
| `localization_overlay/model_animation.rs` (`prove_current_bindings` and `restore_bindings_from` subsets) | `port/animation/` |
| `localization_overlay/model_animation/rig_rebase.rs`, `rig_rebase/{bind_frames,preservation,transforms}.rs` | `port/animation/rig_rebase.go`, `port/animation/transforms.go` |
| `localization_overlay/model_animation/sampled_controls.rs` | `port/animation/restore.go` |
| `localization_overlay/model_animation/metadata.rs` | `port/animation/restore.go` |
| `localization_overlay/model_animation/scaled_rig.rs` | `port/animation/proof.go` |
| `localization_overlay/model_relocation.rs` (archived bindings and prop-model subset) | `port/relocation/`, `internal/modcompat/relocation.go`, `internal/modcompat/references.go` |
| `localization_overlay/animation_contract/cache.rs` | `internal/modcompat/animation_contract.go` |
| `assets/hero-model-bindings.json` | `port/relocation/hero-model-bindings.json` |
| `localization_overlay/compiled_resource.rs` | `port/compiled/` |
| `localization_overlay/vdata_compatibility.rs`, `vdata_compatibility/{semantics,inheritance,reference_fallback}.rs` | `port/vdata/`, `internal/modcompat/data.go` |
| `localization_overlay/enum_compatibility.rs`, `enum_maps.rs`, `hero_rebase.rs` | `port/vdata/` |
| `mod_manager/localization_overlay.rs` (compiled-data table model, source collection, merge, hero IDs) | `port/vdata/`, `internal/modcompat/data.go` |
| `mod_manager/localization_overlay.rs` (localization parsing, historical index, merge) | `port/localization/`, `internal/modcompat/localization.go` |
| `assets/localization-history.bin` | `port/localization/localization-history.bin` |
| `mod_manager/vdata_history.rs` (observed strings, baseline matching, recording) | `port/history/` |
| `localization_overlay/observed_data.rs` (observation cache and learning) | `internal/modcompat/history.go` |
| `localization_overlay/repair_scope.rs` | `internal/modcompat/scope.go`, `internal/app/health.go` |
| `manager/localization/inventory.rs` (mounted unmanaged archives) | `internal/addons/profile.go`, `internal/app/health.go` |
| `mod_manager/manager/localization/history.rs` (learned history path, load, save) | `internal/modcompat/history.go`, `internal/config/history.go` |
| `assets/vdata-history.bin` | `port/history/vdata-history.bin` |
| `mod_manager/mod_health.rs` | `port/health/model.go` |
| `mod_manager/manager/localization/health.rs` | `internal/modhealth/check.go` |
| `mod_manager/manager/localization/receipt.rs` | `port/overlay/receipt.go`, `internal/overlay/apply.go` |
| `mod_manager/manager/localization.rs` (plan key, input stamps) | `internal/overlay/plan.go` |
| `mod_manager/manager/localization.rs` (overlay naming) | `port/overlay/path.go` |
| `mod_manager/gameinfo_search_paths.rs` (legacy/v2 markers) | `internal/game/gameinfo.go` |

Later compatibility categories are ported from the same baseline and added to
this table when they land.

## Modifications

The port keeps the upstream constants, rule ordering, and test expectations.
The differences are limited to Go idioms and the boundaries of this project:

- Rust `Result`/`Option` becomes Go `(value, error)`/`(value, bool)`.
- Rust `BTreeMap`/`BTreeSet` becomes sorted Go slices or maps with explicit
  ordering; places where the upstream relies on `BTreeMap` iteration order are
  documented and kept deterministic.
- Rust `f64`/`f32` conversions are written out explicitly so that narrowing and
  rounding match the Rust code (`math.Float32frombits`/`float64(float32(x))`).
- Error values are translated to this project's error wrapping; no error case is
  dropped.
- File-system access is kept out of `port/`; callers pass bytes or readers.
  Limits on untrusted input live in `internal/source2`.
- The learned `VdataHistory` index is decoded in full: observed string
  bindings, per-build row/field fingerprints, closest-baseline matching, and
  delta recording with the same 64-build compaction. Model relocation consults
  the learned string bindings before the archived JSON; the data compatibility
  path uses the baseline match to tell authored edits from compiler noise.
- Game tables read by a check or an overlay build are also recorded into a
  per-install learned history, with the same whole-resource SHA-256 cache and
  64 MiB / 8-entry bounds. The file lives in the platform cache directory under
  this project's `mylock` directory (the upstream uses the Tauri bundle id) and
  is keyed by the citadel path. A check that persists newly learned data
  reports `HistoryChanged`, which rebuilds the active profile's package.
- The repair scope protects resources shipped by an installed mod that did not
  opt in, using the same rule as upstream: merged localization/data tables are
  protected by any other copy, a binary asset only when the other mod wins the
  load order. Background builds skip protected files. This port has no review
  dialog, so an explicit fix refuses with an error naming the other mod instead
  of silently producing a package without that repair.
- Game-data compatibility merges every enabled mod's `scripts/*.vdata_c` table
  with the current game table at profile level, then writes one merged table
  into the overlay. Hero rows, inherited definitions, enum aliases, retired
  enum-map keys and hero IDs follow the upstream rules; rows or dependencies
  that cannot be proven are reported instead of being retained.
- Localization `.txt` files are parsed with the same KeyValues/line fallback,
  merged per token across enabled mods, and rendered in the canonical BOM/CRLF
  form. A mod copy of a game file is seeded from the current game text so it
  cannot hide tokens it omits; the two protected hero-name files drop values
  seen in the shipped historical index. HUD checks are not ported yet.
- Animation bindings are restored by copying the current graph and skeleton
  references, retiring unavailable legacy selectors and appending missing
  identity sampled controls. When the sampled hierarchy differs, the
  deformation-rig rebase path rebuilds it from the current game rig while
  preserving authored bind frames, meshes, camera attachments and hitboxes;
  any step that cannot be proven declines the relocation.
- The camera interface repair takes the same animation evidence as upstream
  through `camera.ModelOptions`: after a relocation restored or rebased the
  animation rig, a camera point whose dependency chain is supplied by the
  current animation skeleton needs no pose proof, and a uniform resize that
  `scaled_rig` accepts is honoured by the whole-rig comparison.
- Skeleton refreshes use the semantic-equivalence path (`AnimationSkeleton`) or
  the rebase path (`AnimationSkeletonRebased`), which preserves authored primary
  mask weights and reference poses. Masks with secondary weight lists or custom
  metadata are declined. The model-evidence path is not ported, so a refresh
  only runs when no enabled mod ships animation overrides that could sample the
  skeleton.

Test fixtures are translated from the upstream `tests.rs` modules. Real game
assets are never committed; the optional corpus tests use
`MYLOCK_TEST_CITADEL` and skip by default.

## Updating

When moving the baseline, update the commit above, replace `upstream/` as a
whole, re-run the ported tests, and record any new files in the mapping table.
Do not cherry-pick individual functions.

The KV3 codec files in the upstream tree carry
`SPDX-License-Identifier: MIT` and are derived from `vpkmerge` and
ValveResourceFormat. Their copyright notices are preserved in the ported files
even though the combined MyLock distribution is GPL-3.0.