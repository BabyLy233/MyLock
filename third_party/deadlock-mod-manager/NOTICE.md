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
| `localization_overlay/asset_compatibility.rs` (skeleton, remapping, mesh, reference subset) | `port/compat/`, `internal/modcompat/analyze.go` |
| `localization_overlay/model_sounds.rs` | `port/modelsounds/` |
| `packages/vpkmanager/src/resource_id.rs` | `port/resource/id.go` |
| `packages/vpkmanager/src/material_repair.rs` (RERL subset) | `port/resource/rerl.go` |
| `localization_overlay/material_compatibility/interface.rs` | `port/compat/material.go` |
| `localization_overlay/material_compatibility.rs` (RERL reference check) | `internal/modcompat/analyze.go` |
| `localization_overlay/animation_skeleton.rs` (pose subset) | `port/rig/` |
| `localization_overlay/animation_model_evidence.rs` (rig subset) | `port/rig/` |
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