package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/addons"
	"github.com/BabyLy233/mylock/internal/config"
	"github.com/BabyLy233/mylock/internal/game"
	"github.com/BabyLy233/mylock/internal/modcompat"
	"github.com/BabyLy233/mylock/internal/modhealth"
	"github.com/BabyLy233/mylock/internal/overlay"
	"github.com/BabyLy233/mylock/internal/source2"
	portoverlay "github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/overlay"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vdata"
)

// healthRuntime is the compatibility check's state. It is guarded by a.mu.
type healthRuntime struct {
	checker *modhealth.Checker
	// analyze is the per-mod analyzer; tests replace it. Nil means the
	// production modcompat.Analyze.
	analyze modhealth.Analyzer
	report  modhealth.Report
	// checking, checked and total describe the check in flight.
	checking bool
	checked  int
	total    int
	err      string
	// checkedAt is when the last check finished, and checkedEpoch the epoch it
	// covered. A check from another epoch is not shown.
	checkedAt    time.Time
	checkedEpoch uint64
	// compatOpen is the compatibility dialog, and busy a compatibility write in
	// flight. compatErr is what the last write failed with.
	compatOpen bool
	busy       bool
	compatErr  string
	// historyPath resolves the learned game-data history file. Nil uses the
	// platform cache directory; tests point it at a temporary directory.
	historyPath func(citadelDir string) (string, error)
	// review is the set of conflicting choices that paused the package, and
	// draft the player's tentative choice per conflict key. A review is kept
	// until it is submitted or the files it covered change.
	review *pendingReview
	draft  map[string]string
}

// pendingReview is a package build that needs the player's choices before it
// can be written. The inputs are captured so a later submission is refused if
// the mods or game changed in the meantime.
type pendingReview struct {
	inputs        overlay.ReviewInputs
	conflicts     []modcompat.LocalizationConflict
	dataConflicts []modcompat.CompiledDataConflict
}

// submittedReview is one player's answers to a pending review.
type submittedReview struct {
	inputs      overlay.ReviewInputs
	resolutions []overlay.Resolution
}

// healthView is one snapshot of the check's state for the view.
type healthView struct {
	report    modhealth.Report
	byID      map[string]modhealth.Health
	checking  bool
	checked   int
	total     int
	err       string
	checkedAt time.Time
	busy      bool
	compatErr string
	review    *pendingReview
	draft     map[string]string
}

// initHealth opens the health cache and check orchestration. It is called once
// while the app starts, before the first frame.
func (a *app) initHealth() {
	path, err := config.HealthPath()
	if err != nil {
		log.Printf("compatibility health cache: %v", err)
		return
	}
	store, err := modhealth.Open(path)
	if err != nil {
		log.Printf("compatibility health cache: %v", err)
		return
	}
	a.health.checker = modhealth.NewChecker(store)
}

// historyPathFor returns the file that remembers the game tables this
// installation has taught the compatibility system.
func (a *app) historyPathFor(install *game.Install) string {
	a.mu.Lock()
	resolve := a.health.historyPath
	a.mu.Unlock()
	if resolve == nil {
		resolve = config.HistoryPath
	}
	path, err := resolve(install.CitadelDir())
	if err != nil {
		log.Printf("compatibility history: %v", err)
		return ""
	}
	return path
}

// maybeCheckHealth runs the page's automatic check when the inputs changed and
// nothing that writes the profile is running.
func (a *app) maybeCheckHealth() {
	a.mu.Lock()
	h := &a.health
	ready := h.checker != nil && a.install != nil && !h.checking && !h.busy &&
		!a.reading && !a.launching && !a.patching
	stale := h.checkedEpoch != a.healthEpoch || h.checkedAt.IsZero()
	a.mu.Unlock()
	if ready && stale {
		a.checkHealth(false)
	}
}

// checkHealth analyses every installed mod and publishes the report. A result
// is only published while the installation, profile and input epoch are still
// the ones the check started under.
func (a *app) checkHealth(force bool) {
	a.mu.Lock()
	if a.health.checker == nil {
		a.health.err = "兼容性检查不可用：没有找到配置目录。"
		a.mu.Unlock()
		return
	}
	if a.health.checking || a.install == nil {
		a.mu.Unlock()
		return
	}
	checker := a.health.checker
	install, folder := a.install, a.profileFolder
	epoch := a.healthEpoch
	total := len(a.mods)
	a.health.checking = true
	a.health.err = ""
	a.health.checked, a.health.total = 0, total
	a.mu.Unlock()
	a.invalidate()

	go func() {
		profile := profileFor(install, folder)
		mods, err := profile.Read()
		if err != nil {
			a.finishHealth(install, folder, epoch, modhealth.CheckResult{}, err)
			return
		}
		gameIndex, err := a.gameIndexFor(install)
		if err != nil {
			a.finishHealth(install, folder, epoch, modhealth.CheckResult{}, err)
			return
		}
		historyPath := a.historyPathFor(install)
		checks := make([]modhealth.CheckMod, 0, len(mods))
		for _, mod := range mods {
			if len(mod.VpkPaths) == 0 {
				continue
			}
			checks = append(checks, modhealth.CheckMod{
				ID:      mod.ID,
				VPKs:    mod.VpkPaths,
				Choice:  compatibilityChoice(mod),
				Enabled: mod.Enabled,
			})
		}
		mounted, mountErr := profileModFiles(profile, mods)
		if mountErr != nil {
			log.Printf("compatibility inputs: %v", mountErr)
		}
		result, err := checker.Check(profileCacheKey(profile), checks, gameIndex,
			modhealth.CheckOptions{
				Force:        force,
				Build:        install.ClientVersion(),
				HistoryPath:  historyPath,
				MountedFiles: mounted,
				OnProgress:   a.healthProgress(install, folder, epoch),
			})
		a.finishHealth(install, folder, epoch, result, err)
	}()
}

func (a *app) healthProgress(install *game.Install, folder string, epoch uint64) func(int, int) {
	return func(checked, total int) {
		a.publish(func() {
			a.mu.Lock()
			if a.install == install && a.profileFolder == folder && a.healthEpoch == epoch {
				a.health.checked, a.health.total = checked, total
			}
			a.mu.Unlock()
			a.invalidate()
		})
	}
}

func (a *app) finishHealth(install *game.Install, folder string, epoch uint64, result modhealth.CheckResult, err error) {
	report := result.Report
	var message string
	if err != nil {
		message = err.Error()
	} else {
		// A manifest choice alone does not make a fix present. The receipt and
		// the package bytes are the evidence.
		report = adjustHealthReport(report, install, folder)
	}
	a.publish(func() {
		a.mu.Lock()
		a.health.checking = false
		current := a.install == install && a.profileFolder == folder && a.healthEpoch == epoch
		if current {
			if err == nil {
				a.health.report = report
			}
			a.health.err = message
			a.health.checkedAt = time.Now()
			a.health.checkedEpoch = epoch
		}
		a.mu.Unlock()
		a.invalidate()
		if err == nil && result.HistoryChanged && current {
			// The check taught the history new game data, so a package built
			// before it may no longer match what a fresh build produces.
			go a.refreshOverlayAfterLearning(install, folder)
		}
	})
}

// refreshOverlayAfterLearning rebuilds a profile's compatibility package after
// a check learned new game tables. Writing while the game runs is refused; the
// next launch rebuilds from the same history anyway.
func (a *app) refreshOverlayAfterLearning(install *game.Install, folder string) {
	if a.isGameRunning() {
		return
	}
	a.profileWriteMu.Lock()
	defer a.profileWriteMu.Unlock()
	if a.isGameRunning() {
		return
	}
	a.refreshOverlay(install, profileFor(install, folder))
}

// adjustHealthReport downgrades "fixed" verdicts whose compatibility package
// is missing, stale or does not record a repair for that mod. The manifest can
// say a mod opted in while a failed write left nothing to mount.
func adjustHealthReport(report modhealth.Report, install *game.Install, folder string) modhealth.Report {
	profile := profileFor(install, folder)
	receipt, installed, err := overlay.Installed(overlayVPKPath(install, profile))
	fixed := make(map[string]bool)
	if err == nil && installed && receipt != nil && len(receipt.Repairs) > 0 {
		var repairs []overlay.Repair
		if json.Unmarshal(receipt.Repairs, &repairs) == nil {
			for _, repair := range repairs {
				fixed[repair.ModID] = true
			}
		}
	}
	for i := range report.Mods {
		health := &report.Mods[i]
		if health.Status != modhealth.StatusFixed || fixed[health.ModID] {
			continue
		}
		if len(health.RemainingSymptoms) > 0 {
			health.Status = modhealth.StatusPartlyFixable
		} else {
			health.Status = modhealth.StatusFixable
		}
	}
	return report
}

// bumpHealth invalidates any check in flight and asks the next frame for a new
// one, which is what every change to the mods or the game does.
func (a *app) bumpHealth() {
	a.mu.Lock()
	a.healthEpoch++
	a.mu.Unlock()
}

func (a *app) healthState() healthView {
	a.mu.Lock()
	defer a.mu.Unlock()
	byID := make(map[string]modhealth.Health, len(a.health.report.Mods))
	for _, health := range a.health.report.Mods {
		byID[health.ModID] = health
	}
	return healthView{
		report:    a.health.report,
		byID:      byID,
		checking:  a.health.checking,
		checked:   a.health.checked,
		total:     a.health.total,
		err:       a.health.err,
		checkedAt: a.health.checkedAt,
		busy:      a.health.busy,
		compatErr: a.health.compatErr,
		review:    a.health.review,
		draft:     maps.Clone(a.health.draft),
	}
}

func compatibilityChoice(mod addons.Mod) modhealth.Choice {
	switch {
	case mod.CompatibilityEnabled:
		return modhealth.ChoiceEnabled
	case mod.CompatibilityOptedOut:
		return modhealth.ChoiceOriginalFiles
	default:
		return modhealth.ChoiceUnset
	}
}

func profileCacheKey(profile addons.Profile) string {
	if profile.Folder == "" {
		return "default"
	}
	return profile.Folder
}

// gameIndexFor returns the shared game index, opening it once per citadel
// directory.
func (a *app) gameIndexFor(install *game.Install) (*source2.GameIndex, error) {
	path := install.CitadelDir()
	a.gameIndexMu.Lock()
	defer a.gameIndexMu.Unlock()
	if a.gameIndex != nil && a.gameIndexPath == path {
		return a.gameIndex, nil
	}
	index, err := source2.OpenGame(path)
	if err != nil {
		a.gameIndex, a.gameIndexPath = nil, ""
		return nil, err
	}
	a.gameIndex, a.gameIndexPath = index, path
	return index, nil
}

// profileFolderPtr spells the default profile as nil, which is how the
// overlay naming distinguishes it.
func profileFolderPtr(folder string) *string {
	if folder == "" {
		return nil
	}
	return &folder
}

// overlayVPKPath is where a profile's compatibility package lives.
func overlayVPKPath(install *game.Install, profile addons.Profile) string {
	return portoverlay.VPKPath(install.CitadelDir(), profileFolderPtr(profile.Folder))
}

// analyzer returns the per-mod analysis function, which tests replace.
func (a *app) analyzer() modhealth.Analyzer {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.health.analyze != nil {
		return a.health.analyze
	}
	return modcompat.Analyze
}

// profileModFiles lists every archive the game loads for a profile. Archives
// the manifest does not own come first, which treats them as the winning
// provider: that can decline a repair, but never lets the overlay overwrite a
// file the app did not install.
func profileModFiles(profile addons.Profile, mods []addons.Mod) ([]modcompat.ModFiles, error) {
	unmanaged, err := profile.UnmanagedVpks()
	if err != nil {
		return nil, err
	}
	files := make([]modcompat.ModFiles, 0, len(unmanaged)+len(mods))
	for _, path := range unmanaged {
		files = append(files, modcompat.ModFiles{ID: "unmanaged:" + path, VPKs: []string{path}})
	}
	for _, mod := range mods {
		if !mod.Enabled || len(mod.VpkPaths) == 0 {
			// A disabled mod is not mounted, so its files cannot conflict with
			// what the game loads.
			continue
		}
		files = append(files, modcompat.ModFiles{ID: mod.ID, VPKs: mod.VpkPaths})
	}
	return files, nil
}

// rebuildOverlayLocked builds and publishes the compatibility package for the
// profile's opt-in mods. It must be called with profileWriteMu held. With
// nothing left to fix it removes the package instead, so a launch falls back to
// the mod files themselves.
func (a *app) rebuildOverlayLocked(install *game.Install, profile addons.Profile) (*pendingReview, error) {
	return a.rebuildOverlayReview(install, profile, nil)
}

// rebuildOverlayReview is rebuildOverlayLocked with the player's reviewed
// choices, which must answer the conflicts of exactly the current inputs.
func (a *app) rebuildOverlayReview(install *game.Install, profile addons.Profile,
	submitted *submittedReview) (*pendingReview, error) {
	vpkPath := overlayVPKPath(install, profile)
	mods, err := profile.Read()
	if err != nil {
		return nil, err
	}
	var selected []addons.Mod
	for _, mod := range mods {
		if mod.Enabled && mod.CompatibilityEnabled && len(mod.VpkPaths) > 0 {
			selected = append(selected, mod)
		}
	}
	if len(selected) == 0 {
		return nil, overlay.Remove(vpkPath)
	}
	gameIndex, err := a.gameIndexFor(install)
	if err != nil {
		return nil, err
	}
	historyPath := a.historyPathFor(install)
	learned := modcompat.LoadHistory(historyPath, install.ClientVersion())
	// Resources another installed mod also ships are left as the game loads
	// them: rewriting a shared file would change that mod without its owner
	// opting in.
	allFiles, err := profileModFiles(profile, mods)
	if err != nil {
		return nil, err
	}
	selectedIDs := make(map[string]struct{}, len(selected))
	for _, mod := range selected {
		selectedIDs[mod.ID] = struct{}{}
	}
	protection, err := modcompat.NewRepairScope(allFiles, selectedIDs)
	if err != nil {
		return nil, err
	}
	protected := make(map[string]struct{}, len(protection.ProtectedPaths()))
	for _, path := range protection.ProtectedPaths() {
		protected[path] = struct{}{}
	}
	// Two mods providing the same model would be collapsed into one fixed file,
	// which silently changes the mod the game loads. Refuse instead.
	owners := make(map[string]string)
	for _, mod := range selected {
		paths, err := modcompat.ModelPaths(mod.VpkPaths)
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			if owner, taken := owners[path]; taken && owner != mod.ID {
				return nil, fmt.Errorf("模组 %s 与 %s 都包含 %s，无法安全修复；请先停用其中一个", owner, mod.ID, path)
			}
			owners[path] = mod.ID
		}
	}
	analyze := a.analyzer()
	files := make([]modcompat.ModFiles, 0, len(selected))
	for _, mod := range selected {
		files = append(files, modcompat.ModFiles{ID: mod.ID, VPKs: mod.VpkPaths})
	}
	scopes, err := modcompat.Scopes(files)
	if err != nil {
		return nil, err
	}
	dataInputs := make([]modcompat.DataInput, 0, len(selected))
	for _, mod := range selected {
		dataInputs = append(dataInputs, modcompat.DataInput{ModID: mod.ID, VPKs: mod.VpkPaths})
	}
	historyDigest := historyFileDigest(historyPath)
	savedReview, err := overlay.LoadReview(vpkPath)
	if err != nil {
		return nil, err
	}
	data, err := modcompat.FixData(dataInputs, gameIndex, learned, protected, nil)
	if err != nil {
		return nil, err
	}
	text, err := modcompat.FixLocalization(dataInputs, gameIndex, protected, nil)
	if err != nil {
		return nil, err
	}
	combined := data
	combined.LocalizationConflicts = text.LocalizationConflicts
	conflicts := len(combined.LocalizationConflicts) + len(combined.DataConflicts)
	var chosen []overlay.Resolution
	reviewed := false
	if conflicts > 0 {
		// The saved answers only count for exactly the findings and files they
		// covered. A choice whose contender disappeared is stale too.
		inputs, inputErr := reviewInputs(install, vpkPath, selected, historyDigest, combined)
		if inputErr != nil {
			return nil, inputErr
		}
		switch {
		case submitted != nil:
			if !(&overlay.Review{Inputs: submitted.inputs}).Matches(inputs) {
				return nil, errors.New("模组或游戏文件在评审期间发生了变化，请重新检查后再选择。")
			}
			applicable := modcompat.ApplicableResolutions(combined, overlayResolutions(submitted.resolutions))
			if len(applicable) != len(submitted.resolutions) {
				return nil, errors.New("评审选择已不再适用于当前模组，请重新检查后再选择。")
			}
			chosen = submitted.resolutions
			reviewed = true
		case savedReview != nil:
			saved := overlayResolutions(savedReview.Resolutions)
			applicable := modcompat.ApplicableResolutions(combined, saved)
			if savedReview.Matches(inputs) && len(applicable) == len(saved) {
				chosen = savedReview.Resolutions
				reviewed = true
			}
		}
		if !reviewed {
			// Writing a package from default winners would apply values the
			// player never chose, so the package is left unmounted until the
			// review is answered.
			if err := overlay.Remove(vpkPath); err != nil {
				return nil, err
			}
			return &pendingReview{
				inputs:        inputs,
				conflicts:     slices.Clone(combined.LocalizationConflicts),
				dataConflicts: slices.Clone(combined.DataConflicts),
			}, nil
		}
		if len(chosen) > 0 {
			data, err = modcompat.FixData(dataInputs, gameIndex, learned, protected, overlayResolutions(chosen))
			if err != nil {
				return nil, err
			}
			text, err = modcompat.FixLocalization(dataInputs, gameIndex, protected, overlayResolutions(chosen))
			if err != nil {
				return nil, err
			}
		}
	}
	shared := make(map[string][]byte, len(data.Files)+len(text.Files))
	for path, bytes := range data.Files {
		shared[path] = bytes
	}
	for path, bytes := range text.Files {
		shared[path] = bytes
	}
	data.Warnings = append(data.Warnings, text.Warnings...)
	dataWarnings := make(map[string][]overlay.Warning)
	for _, warning := range data.Warnings {
		dataWarnings[warning.ModID] = append(dataWarnings[warning.ModID], overlay.Warning{
			ModID: warning.ModID, Path: warning.FilePath, Kind: string(warning.Kind), Detail: warning.Detail,
		})
	}
	var sources []overlay.Source
	anyFixed := len(shared) > 0
	for _, mod := range selected {
		scope := scopes[mod.ID]
		scope.ProfileData = true
		scope.History = learned
		analysis, err := analyze(mod.ID, mod.VpkPaths, gameIndex, scope, modcompat.Fix)
		if err != nil {
			return nil, err
		}
		analysis.RemoveProtected(protection)
		for path := range analysis.Fixed {
			if modcompat.IsDataTablePath(path) {
				delete(analysis.Fixed, path)
			}
		}
		source := overlay.Source{
			ModID:                mod.ID,
			VPKs:                 mod.VpkPaths,
			CompatibilityEnabled: true,
			Fixed:                analysis.Fixed,
		}
		for _, repair := range analysis.Repairs {
			source.Repairs = append(source.Repairs, overlay.Repair{
				ModID: repair.ModID, Path: repair.FilePath, Kind: string(repair.Kind),
			})
		}
		for _, warning := range analysis.Warnings {
			source.Warnings = append(source.Warnings, overlay.Warning{
				ModID: warning.ModID, Path: warning.FilePath,
				Kind: string(warning.Kind), Detail: warning.Detail,
			})
		}
		source.Warnings = append(source.Warnings, dataWarnings[mod.ID]...)
		if len(analysis.Fixed) > 0 {
			anyFixed = true
		}
		sources = append(sources, source)
	}
	if learned.Changed() && historyPath != "" {
		// Learning is reproducible from the same game files, so a cache write
		// failure must not keep the package from being built.
		if err := learned.Save(historyPath); err != nil {
			log.Printf("Could not persist observed compatibility history: %v", err)
		}
	}
	if len(sources) == 0 || !anyFixed {
		if err := overlay.Remove(vpkPath); err != nil {
			return nil, err
		}
		if submitted != nil && reviewed {
			return nil, a.saveReview(vpkPath, historyPath, submitted, chosen)
		}
		return nil, nil
	}
	plan, err := overlay.BuildWithData(install.CitadelDir(), vpkPath, install.ClientVersion(), sources, shared)
	if err != nil {
		return nil, err
	}
	plan.Key.HistoryDigest = historyFileDigest(historyPath)
	if _, err := overlay.Apply(plan, vpkPath); err != nil {
		return nil, err
	}
	if submitted != nil && reviewed {
		if err := a.saveReview(vpkPath, historyPath, submitted, chosen); err != nil {
			// A package without its review would apply answers the next build
			// cannot verify, so leave neither.
			_ = overlay.Remove(vpkPath)
			return nil, err
		}
	} else if conflicts == 0 {
		// A review answers questions that no longer exist; keeping it would
		// only make a later unrelated conflict look reviewed.
		if err := overlay.RemoveReview(vpkPath); err != nil {
			log.Printf("removing the compatibility review: %v", err)
		}
	}
	return nil, nil
}

// saveReview records answers against the inputs they were submitted for. The
// history digest is read again because this build may have taught it new data.
func (a *app) saveReview(vpkPath, historyPath string, submitted *submittedReview, resolutions []overlay.Resolution) error {
	saved := overlay.Review{Inputs: submitted.inputs, Resolutions: resolutions}
	saved.Inputs.HistoryDigest = historyFileDigest(historyPath)
	return overlay.SaveReview(vpkPath, saved)
}

// historyFileDigest fingerprints the learned history file. A missing file has
// no digest, which every review built against it shares.
func historyFileDigest(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// reviewInputs describes the mods and game files a set of conflicts was found
// in, so a saved review is reused only for those exact inputs.
func reviewInputs(install *game.Install, vpkPath string, selected []addons.Mod,
	historyDigest string, result modcompat.DataResult) (overlay.ReviewInputs, error) {
	key := overlay.Key{
		OutputPath: vpkPath, GameBuild: install.ClientVersion(), HistoryDigest: historyDigest,
	}
	for _, mod := range selected {
		stamps, err := overlay.StampArchives(mod.VpkPaths)
		if err != nil {
			return overlay.ReviewInputs{}, err
		}
		key.Mods = append(key.Mods, overlay.ModStamp{ModID: mod.ID, Files: stamps})
	}
	base, err := overlay.BaseStamps(install.CitadelDir())
	if err != nil {
		return overlay.ReviewInputs{}, err
	}
	key.BaseGame = base
	return overlay.ReviewInputs{
		Revision:      overlay.ReviewRevision,
		Subject:       modcompat.ReviewSubject(result),
		Mods:          overlay.ReviewModStamps(key, modcompat.ReviewModIDs(result)),
		BaseGame:      base,
		HistoryDigest: historyDigest,
	}, nil
}

// overlayResolutions converts saved choices to the merger's type.
func overlayResolutions(saved []overlay.Resolution) []vdata.Resolution {
	out := make([]vdata.Resolution, 0, len(saved))
	for _, resolution := range saved {
		out = append(out, vdata.Resolution{
			ConflictKey:     resolution.ConflictKey,
			WinnerModID:     resolution.WinnerModID,
			WinnerValue:     resolution.WinnerValue,
			WinnerSourceVPK: resolution.WinnerSourceVPK,
			UseVanilla:      resolution.UseVanilla,
		})
	}
	return out
}

// refreshOverlay rebuilds the package after a manifest change. It must be
// called with profileWriteMu held. A rebuild that fails removes the package so
// the game never mounts a fix built from files that are no longer there.
func (a *app) refreshOverlay(install *game.Install, profile addons.Profile) {
	prompt, err := a.rebuildOverlayLocked(install, profile)
	if err != nil {
		log.Printf("rebuilding the compatibility overlay: %v", err)
		if removeErr := overlay.Remove(overlayVPKPath(install, profile)); removeErr != nil {
			log.Printf("removing the compatibility overlay: %v", removeErr)
		}
	}
	a.publishReview(prompt, err)
}

// publishReview shows a review the package is waiting on, or clears one whose
// inputs the rebuild no longer has. It may be called with profileWriteMu held:
// it only takes the state lock.
func (a *app) publishReview(prompt *pendingReview, err error) {
	if prompt == nil && err != nil {
		return
	}
	a.publish(func() {
		a.mu.Lock()
		if prompt != nil {
			a.health.review = prompt
			a.health.draft = make(map[string]string, len(prompt.conflicts))
		} else {
			a.health.review = nil
			a.health.draft = nil
		}
		a.mu.Unlock()
		a.invalidate()
	})
}

// reviewChoice is one selectable answer in the compatibility dialog. A nil
// resolution means the load-order default, which continues to be unnamed in a
// saved review.
type reviewChoice struct {
	label      string
	resolution *overlay.Resolution
}

// localizationChoices describes the answers for one conflicting token: the
// default winner, the game's own value, and every mod's value.
func localizationChoices(conflict modcompat.LocalizationConflict, names map[string]string) []reviewChoice {
	first := conflict.Candidates[0]
	choices := []reviewChoice{{label: "沿用加载顺序：" + reviewModName(first.ModID, names)}}
	if conflict.VanillaValue != nil {
		choices = append(choices, reviewChoice{
			label:      "使用游戏原值：" + reviewValue(*conflict.VanillaValue),
			resolution: &overlay.Resolution{ConflictKey: conflict.Key, UseVanilla: true},
		})
	}
	for _, candidate := range conflict.Candidates {
		resolution := overlay.Resolution{
			ConflictKey: conflict.Key, WinnerModID: &candidate.ModID,
			WinnerSourceVPK: &candidate.SourceVPK, WinnerValue: &candidate.Value,
		}
		choices = append(choices, reviewChoice{
			label:      fmt.Sprintf("%d. %s：%s", len(choices), reviewModName(candidate.ModID, names), reviewValue(candidate.Value)),
			resolution: &resolution,
		})
	}
	return choices
}

// dataChoices describes the answers for one conflicting compiled-data field.
func dataChoices(conflict modcompat.CompiledDataConflict, names map[string]string) []reviewChoice {
	first := conflict.Candidates[0]
	choices := []reviewChoice{{label: "沿用加载顺序：" + reviewModName(first.ModID, names)}}
	choices = append(choices, reviewChoice{
		label:      "使用游戏原值",
		resolution: &overlay.Resolution{ConflictKey: conflict.Key, UseVanilla: true},
	})
	for _, candidate := range conflict.Candidates {
		resolution := overlay.Resolution{
			ConflictKey: conflict.Key, WinnerModID: &candidate.ModID,
			WinnerSourceVPK: &candidate.SourceVPK,
		}
		choices = append(choices, reviewChoice{
			label:      fmt.Sprintf("%d. %s", len(choices), reviewModName(candidate.ModID, names)),
			resolution: &resolution,
		})
	}
	return choices
}

// reviewChoices returns the options for a conflict key from either family.
func reviewChoices(review *pendingReview, key string, names map[string]string) []reviewChoice {
	for _, conflict := range review.conflicts {
		if conflict.Key == key {
			return localizationChoices(conflict, names)
		}
	}
	for _, conflict := range review.dataConflicts {
		if conflict.Key == key {
			return dataChoices(conflict, names)
		}
	}
	return nil
}

// reviewModName falls back to the mod id when no display name is known.
func reviewModName(id string, names map[string]string) string {
	if name := names[id]; name != "" {
		return name
	}
	return id
}

// reviewValue shortens a token value for a one-line choice.
func reviewValue(value string) string {
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " ")
	if runes := []rune(value); len(runes) > 48 {
		return string(runes[:48]) + "…"
	}
	return value
}

// draftResolutions turns the dialog's tentative answers into saved choices.
// Options left at the load-order default produce no resolution.
func draftResolutions(review *pendingReview, draft map[string]string, names map[string]string) []overlay.Resolution {
	var resolutions []overlay.Resolution
	collect := func(key string) {
		choices := reviewChoices(review, key, names)
		label, chosen := draft[key]
		if !chosen {
			return
		}
		for _, choice := range choices {
			if choice.label != label || choice.resolution == nil {
				continue
			}
			resolutions = append(resolutions, *choice.resolution)
			return
		}
	}
	for _, conflict := range review.conflicts {
		collect(conflict.Key)
	}
	for _, conflict := range review.dataConflicts {
		collect(conflict.Key)
	}
	return resolutions
}

// setReviewDraft records one tentative answer.
func (a *app) setReviewDraft(key, choice string) {
	a.publish(func() {
		a.mu.Lock()
		if a.health.draft == nil {
			a.health.draft = make(map[string]string)
		}
		a.health.draft[key] = choice
		a.mu.Unlock()
		a.invalidate()
	})
}

// submitReview rebuilds the package with the player's answers and saves the
// review so later rebuilds do not ask again.
func (a *app) submitReview(resolutions []overlay.Resolution) {
	a.mu.Lock()
	if a.install == nil || a.health.busy || a.health.review == nil {
		a.mu.Unlock()
		return
	}
	if a.gameRunning {
		a.health.compatErr = "游戏正在运行，先退出游戏再应用兼容性选择。"
		a.mu.Unlock()
		a.invalidate()
		return
	}
	install, folder := a.install, a.profileFolder
	submitted := submittedReview{inputs: a.health.review.inputs, resolutions: resolutions}
	a.health.busy = true
	a.health.compatErr = ""
	a.mu.Unlock()
	a.invalidate()

	go func() {
		prompt, info, err := a.writeReviewed(install, folder, submitted)
		var message string
		if err != nil {
			message = err.Error()
		}
		a.publish(func() {
			a.mu.Lock()
			a.health.busy = false
			a.health.compatErr = message
			if err == nil && prompt == nil {
				a.health.review, a.health.draft = nil, nil
				a.gameInfo = info
				a.gameInfoErr = ""
			} else if prompt != nil {
				a.health.review = prompt
				a.health.draft = make(map[string]string, len(prompt.conflicts))
			}
			a.healthEpoch++
			a.mu.Unlock()
			a.invalidate()
		})
		if err == nil {
			a.checkHealth(true)
		}
	}()
}

// writeReviewed takes the profile lock and writes the package for one
// reviewed set of answers.
func (a *app) writeReviewed(install *game.Install, folder string, submitted submittedReview) (*pendingReview, game.GameInfo, error) {
	if a.isGameRunning() {
		return nil, game.GameInfo{}, errors.New("游戏正在运行，先退出游戏再应用兼容性选择。")
	}
	a.profileWriteMu.Lock()
	defer a.profileWriteMu.Unlock()
	if a.isGameRunning() {
		return nil, game.GameInfo{}, errors.New("游戏正在运行，先退出游戏再应用兼容性选择。")
	}
	profile := profileFor(install, folder)
	prompt, err := a.rebuildOverlayReview(install, profile, &submitted)
	if err != nil {
		_ = overlay.Remove(overlayVPKPath(install, profile))
		return nil, game.GameInfo{}, err
	}
	info, patchErr := a.patchGameInfo(install, profile)
	return prompt, info, patchErr
}

// openCompatibility opens the dialog and starts a forced check, so its numbers
// are from the files as they are now.
func (a *app) openCompatibility() {
	a.health.compatOpen = true
	a.checkHealth(true)
}

func (a *app) closeCompatibility() {
	a.mu.Lock()
	a.health.compatOpen = false
	a.mu.Unlock()
}

// fixCompatibility opts the mods in and rebuilds the package in one manifest
// write.
func (a *app) fixCompatibility(ids []string) {
	a.applyCompatibility(ids, true)
}

// restoreCompatibility opts a mod out, records that the player chose the
// original files, and rebuilds the package without it.
func (a *app) restoreCompatibility(id string) {
	a.applyCompatibility([]string{id}, false)
}

func (a *app) applyCompatibility(ids []string, enabled bool) {
	a.mu.Lock()
	install, folder := a.install, a.profileFolder
	if install == nil || a.health.busy || len(ids) == 0 {
		a.mu.Unlock()
		return
	}
	a.health.busy = true
	a.health.compatErr = ""
	a.mu.Unlock()
	a.invalidate()

	go func() {
		mods, info, prompt, err := a.writeCompatibility(install, folder, ids, enabled)
		var message string
		if err != nil {
			message = err.Error()
		}
		a.publish(func() {
			a.mu.Lock()
			a.health.busy = false
			a.health.compatErr = message
			if prompt != nil {
				a.health.review = prompt
				a.health.draft = make(map[string]string, len(prompt.conflicts))
			} else if err == nil {
				a.health.review, a.health.draft = nil, nil
			}
			if mods != nil {
				a.mods = mods
			}
			if err == nil {
				a.gameInfo = info
				a.gameInfoErr = ""
			}
			a.healthEpoch++
			a.mu.Unlock()
			a.invalidate()
		})
		if err == nil {
			// The status shown after a fix comes from a fresh read-only check.
			a.checkHealth(true)
		}
	}()
}

// sharedFixConflict refuses to opt a mod in when another installed mod that
// was not part of the package keeps one of its files. The package leaves such
// files alone, so the repair could never take effect; naming the other mod is
// clearer than a status that keeps saying the mod is fixable.
func sharedFixConflict(profile addons.Profile, ids []string) error {
	mods, err := profile.Read()
	if err != nil {
		return err
	}
	requested := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		requested[id] = struct{}{}
	}
	var allFiles []modcompat.ModFiles
	selected := make(map[string]struct{}, len(ids))
	allFiles, err = profileModFiles(profile, mods)
	if err != nil {
		return err
	}
	for _, mod := range mods {
		if !mod.Enabled || len(mod.VpkPaths) == 0 {
			continue
		}
		if _, ok := requested[mod.ID]; ok || mod.CompatibilityEnabled {
			selected[mod.ID] = struct{}{}
		}
	}
	protection, err := modcompat.NewRepairScope(allFiles, selected)
	if err != nil {
		return err
	}
	for _, mod := range mods {
		if !mod.Enabled || len(mod.VpkPaths) == 0 {
			continue
		}
		if _, ok := requested[mod.ID]; !ok {
			continue
		}
		paths, err := protection.ProtectedFor(mod.VpkPaths)
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			continue
		}
		owners := protection.ModIDs(paths[0])
		labels := make([]string, len(owners))
		for i, owner := range owners {
			if path, ok := strings.CutPrefix(owner, "unmanaged:"); ok {
				labels[i] = "手动安装的 VPK（" + filepath.Base(path) + "）"
			} else {
				labels[i] = owner
			}
		}
		return fmt.Errorf("模组 %s 与 %s 共用 %s，无法安全修复；请先停用对方模组，或为它也开启兼容性。",
			mod.ID, strings.Join(labels, "、"), paths[0])
	}
	return nil
}

// writeCompatibility takes the profile lock, changes the choice, rebuilds the
// overlay and writes the game's search paths, in that order. Removing the old
// package first means a crash at any step falls back to the mod files rather
// than mounting a fix for the previous selection.
func (a *app) writeCompatibility(install *game.Install, folder string, ids []string, enabled bool) ([]addons.Mod, game.GameInfo, *pendingReview, error) {
	if a.isGameRunning() {
		return nil, game.GameInfo{}, nil, errors.New("游戏正在运行，先退出游戏再修改兼容性。")
	}
	a.profileWriteMu.Lock()
	defer a.profileWriteMu.Unlock()

	profile := profileFor(install, folder)
	vpkPath := overlayVPKPath(install, profile)
	if enabled {
		if err := sharedFixConflict(profile, ids); err != nil {
			return nil, game.GameInfo{}, nil, err
		}
	}
	// Asked again after taking the lock, which is the last point before files
	// this write replaces are touched: the game can have started while the
	// choice was being prepared.
	if a.isGameRunning() {
		return nil, game.GameInfo{}, nil, errors.New("游戏正在运行，先退出游戏再修改兼容性。")
	}
	if err := overlay.Remove(vpkPath); err != nil {
		return nil, game.GameInfo{}, nil, err
	}
	if err := profile.SetCompatibility(ids, enabled); err != nil {
		return nil, game.GameInfo{}, nil, err
	}
	prompt, rebuildErr := a.rebuildOverlayLocked(install, profile)
	info, patchErr := a.patchGameInfo(install, profile)
	mods, readErr := profile.Read()
	return mods, info, prompt, errors.Join(rebuildErr, patchErr, readErr)
}

// hudSummary describes the advisory HUD comparison for one mod. It never
// changes the health status: a layout may intentionally drop a panel.
func hudSummary(checks []modcompat.HudCheck) string {
	if len(checks) == 0 {
		return ""
	}
	missing := make(map[string]struct{})
	consoleLines := 0
	var unreadable, unsupported, noBaseline bool
	for _, check := range checks {
		consoleLines += len(check.Console.Lines)
		switch check.Status {
		case modcompat.HudCompared:
			for _, panel := range check.MissingPanels {
				missing[panel] = struct{}{}
			}
		case modcompat.HudUnreadable:
			unreadable = true
		case modcompat.HudUnsupported:
			unsupported = true
		case modcompat.HudNoBaseline:
			noBaseline = true
		}
	}
	var line string
	switch {
	case len(missing) > 0:
		panels := slices.Sorted(maps.Keys(missing))
		if len(panels) > 6 {
			panels = append(panels[:6], fmt.Sprintf("等 %d 个", len(missing)))
		}
		line = "HUD 布局：游戏新增的面板未包含：" + joinChinese(panels) + "（仅提示）"
	case unreadable:
		line = "HUD 布局：模组布局文件无法读取（仅提示）"
	case unsupported:
		line = "HUD 布局：布局格式不在检查范围内（仅提示）"
	case noBaseline:
		line = "HUD 布局：当前游戏没有同名文件，无法比较"
	}
	if line != "" && consoleLines > 0 {
		line += fmt.Sprintf("；console.log 有 %d 条相关报错", consoleLines)
	}
	return line
}

func healthSymptoms(symptoms []modhealth.Symptom) string {
	if len(symptoms) == 0 {
		return ""
	}
	parts := make([]string, 0, len(symptoms))
	for _, symptom := range symptoms {
		switch symptom {
		case modhealth.SymptomCameraAim:
			parts = append(parts, "相机/瞄准")
		case modhealth.SymptomAnimations:
			parts = append(parts, "动画")
		case modhealth.SymptomAppearance:
			parts = append(parts, "外观")
		case modhealth.SymptomModel:
			parts = append(parts, "模型")
		case modhealth.SymptomGameData:
			parts = append(parts, "游戏数据")
		case modhealth.SymptomCrashRisk:
			parts = append(parts, "崩溃风险")
		}
	}
	return joinChinese(parts)
}

func joinChinese(parts []string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += "、"
		}
		out += part
	}
	return out
}

// healthLine is a mod's badge text and its tone: accent for something the user
// can fix, danger for a mod that needs its author, success for a fix in place.
// It names the categories that were not checked, so "no problems" is never
// mistaken for a full review.
func healthLine(health modhealth.Health) (string, string) {
	line, tone := healthStatusLine(health)
	if line != "" {
		if missing := modhealth.LimitedSymptoms(health.Checks); len(missing) > 0 {
			line += "（未检查：" + healthSymptoms(missing) + "）"
		}
	}
	if health.ScopeIncomplete {
		line += "（多个模组提供同一文件，无法单独修复）"
	}
	return line, tone
}

func healthStatusLine(health modhealth.Health) (string, string) {
	symptoms := healthSymptoms(health.Symptoms)
	with := func(prefix string) string {
		if symptoms == "" {
			return prefix
		}
		return prefix + "：" + symptoms
	}
	switch health.Status {
	case modhealth.StatusWorks:
		return "兼容性正常", "muted"
	case modhealth.StatusFixable:
		return with("可修复"), "accent"
	case modhealth.StatusPartlyFixable:
		return with("部分可修复"), "accent"
	case modhealth.StatusFixed:
		return with("已修复"), "success"
	case modhealth.StatusOriginalFiles:
		return "使用原文件", "muted"
	case modhealth.StatusNeedsUpdate:
		return with("需要更新"), "danger"
	case modhealth.StatusNotCovered:
		return "未完全检查", "muted"
	}
	return "", "muted"
}

func healthToneColor(t *ui.Theme, tone string) ui.Color {
	switch tone {
	case "accent":
		return t.Accent
	case "danger":
		return t.Danger
	case "success":
		return t.Success
	}
	return t.TextMuted
}

// reviewSection asks the player to choose between contributors that rewrote
// the same localization token or compiled-data field. Nothing is written until
// every choice is applied.
func (a *app) reviewSection(c *ui.Context, review *pendingReview, draft map[string]string,
	names map[string]string, busy, checking bool) {
	t := c.Theme()
	count := len(review.conflicts) + len(review.dataConflicts)
	ui.Column(c).FillWidth().Gap(6).Padding(8).Background(t.Background).Radius(6).Children(func() {
		ui.Text(c, fmt.Sprintf("%d 处冲突需要选择", count)).FontSize(13).Bold()
		ui.Text(c, "多个模组改写了同一词条或数据项。选用其中一个后，兼容包才会写入；选择会被记住，文件不变时不再询问。").
			FontSize(11).TextColor(t.TextMuted)
		height := float32(count*64 + 4)
		if height > 300 {
			height = 300
		}
		ui.Scroll(c).WidthPercent(100).Height(height).Children(func() {
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				for _, conflict := range review.conflicts {
					key := conflict.Key
					choices := localizationChoices(conflict, names)
					labels := make([]string, len(choices))
					for i, choice := range choices {
						labels[i] = choice.label
					}
					chosen := draft[key]
					if !slices.Contains(labels, chosen) {
						chosen = labels[0]
					}
					ui.Column(c).FillWidth().Gap(3).Children(func() {
						ui.Text(c, filepath.Base(conflict.FilePath)+" · "+conflict.Token).
							FontSize(12).Bold().MaxLines(1).Ellipsis("…")
						ui.Select(c, &chosen, labels).OnChange(func() { a.setReviewDraft(key, chosen) })
					})
				}
				for _, conflict := range review.dataConflicts {
					key := conflict.Key
					choices := dataChoices(conflict, names)
					labels := make([]string, len(choices))
					for i, choice := range choices {
						labels[i] = choice.label
					}
					chosen := draft[key]
					if !slices.Contains(labels, chosen) {
						chosen = labels[0]
					}
					label := filepath.Base(conflict.FilePath) + " · " + conflict.RowName
					if conflict.FieldPath != "" {
						label += " · " + conflict.FieldPath
					}
					ui.Column(c).FillWidth().Gap(3).Children(func() {
						ui.Text(c, label).FontSize(12).Bold().MaxLines(1).Ellipsis("…")
						ui.Select(c, &chosen, labels).OnChange(func() { a.setReviewDraft(key, chosen) })
					})
				}
			})
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.PrimaryButton(c, "应用选择").
				Label("按所选模组重建兼容包").
				Disabled(busy || checking || a.gameRunningNow()).
				OnClick(func() { a.submitReview(draftResolutions(review, draft, names)) })
			ui.Text(c, "沿用加载顺序表示不单独指定，与修复前的行为一致。").
				FontSize(11).TextColor(t.TextMuted)
		})
	})
}

// compatibilityDialog lists each mod with something to say about compatibility.
// The buttons act on one mod, and the footer fixes every mod that can be fixed
// in one write.
func (a *app) compatibilityDialog(c *ui.Context) {
	if !a.health.compatOpen {
		return
	}
	t := c.Theme()
	h := a.healthState()
	mods := a.installed()
	infos := a.modInfos()
	names := make(map[string]string, len(mods.list))
	for _, mod := range mods.list {
		names[mod.ID] = modName(mod.ID, infos[mod.ID])
	}

	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(620).Gap(10).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, "兼容性").FontSize(18).Bold()
				ui.Spacer(c)
				if h.checking {
					ui.Spinner(c)
				}
				ui.Button(c, "重新检查").
					Label("重新检查模组兼容性").
					Disabled(h.checking || h.busy).
					OnClick(func() { a.checkHealth(true) })
			})
			ui.Text(c, "检查只读取模组和游戏文件；修复写进独立的兼容包，不改动原模组文件。").
				FontSize(12).TextColor(t.TextMuted)
			if h.review != nil {
				a.reviewSection(c, h.review, h.draft, names, h.busy, h.checking)
			}

			var shown []addons.Mod
			var fixable []string
			extraHeight := 0
			for _, mod := range mods.list {
				health, ok := h.byID[mod.ID]
				if !ok {
					continue
				}
				hud := hudSummary(health.HudChecks)
				if health.Status != modhealth.StatusWorks || health.Repairs > 0 || hud != "" {
					shown = append(shown, mod)
				}
				if hud != "" {
					extraHeight += 15
				}
				if health.Repairs > 0 && health.Status != modhealth.StatusFixed && !health.ScopeIncomplete {
					fixable = append(fixable, mod.ID)
				}
			}
			if h.checkedAt.IsZero() && !h.checking && h.err == "" {
				ui.Text(c, "还没有检查结果。").FontSize(12).TextColor(t.TextMuted)
			}
			if h.checking {
				if h.total > 0 {
					ui.Text(c, fmt.Sprintf("正在检查 %d/%d 个模组…", h.checked, h.total)).
						FontSize(12).TextColor(t.TextMuted)
				} else {
					ui.Text(c, "正在检查…").FontSize(12).TextColor(t.TextMuted)
				}
			}
			if len(shown) == 0 && !h.checking && h.err == "" {
				ui.Text(c, "没有发现兼容性问题。").FontSize(12).TextColor(t.TextMuted)
			}
			if len(shown) > 0 {
				height := float32(len(shown)*56 + extraHeight + 4)
				if height > 380 {
					height = 380
				}
				ui.Scroll(c).WidthPercent(100).Height(height).Children(func() {
					ui.Column(c).FillWidth().Gap(6).Children(func() {
						for _, mod := range shown {
							mod := mod
							health := h.byID[mod.ID]
							line, tone := healthLine(health)
							name := modName(mod.ID, infos[mod.ID])
							ui.Row(c).FillWidth().Padding(8).Gap(10).AlignItems(ui.Center).
								Background(t.Background).Radius(6).Children(func() {
								ui.Column(c).Grow(1).Gap(3).Children(func() {
									ui.Text(c, name).FontSize(14).Bold().MaxLines(1).Ellipsis("…")
									ui.Text(c, line).FontSize(12).TextColor(healthToneColor(t, tone))
									if health.Warnings > 0 {
										ui.Text(c, fmt.Sprintf("%d 项问题需要模组作者更新", health.Warnings)).
											FontSize(11).TextColor(t.TextMuted)
									}
									if hud := hudSummary(health.HudChecks); hud != "" {
										ui.Text(c, hud).FontSize(11).TextColor(t.TextMuted).
											MaxLines(2).Ellipsis("…")
									}
								})
								ui.Row(c).Shrink(0).Gap(6).Children(func() {
									if health.Repairs > 0 && health.Status != modhealth.StatusFixed {
										ui.PrimaryButton(c, "修复").
											Label("修复 " + name).
											Disabled(h.busy || h.checking || h.review != nil || !mod.Enabled || health.ScopeIncomplete || a.gameRunningNow()).
											OnClick(func() { a.fixCompatibility([]string{mod.ID}) })
									}
									if health.Status == modhealth.StatusFixed {
										ui.Button(c, "使用原文件").
											Label("让 " + name + " 使用原文件").
											Disabled(h.busy || h.checking || a.gameRunningNow()).
											OnClick(func() { a.restoreCompatibility(mod.ID) })
									}
								})
							})
						}
					})
				})
			}
			if h.err != "" {
				ui.Text(c, h.err).FontSize(12).TextColor(t.Danger)
			}
			if h.compatErr != "" {
				ui.Text(c, h.compatErr).FontSize(12).TextColor(t.Danger)
			}
			if h.busy {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c)
					ui.Text(c, "正在写入兼容包…").FontSize(12).TextColor(t.TextMuted)
				})
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if len(fixable) > 0 {
					ui.PrimaryButton(c, "全部修复").
						Label("修复所有可修复的模组").
						Disabled(h.busy || h.checking || h.review != nil || a.gameRunningNow()).
						OnClick(func() { a.fixCompatibility(fixable) })
				}
				ui.Button(c, "关闭").OnClick(a.closeCompatibility)
			})
		})
	})
	if !open {
		a.closeCompatibility()
	}
}

// healthSection is the settings page's view of the check: when it last ran,
// what it found and the way to run it again.
func (a *app) healthSection(c *ui.Context, s state) {
	t := c.Theme()
	h := a.healthState()
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "兼容性检查").FontSize(16).Bold()
		ui.Text(c, "检查已安装模组与当前游戏文件的差异。修复写进独立的兼容包，原模组文件不变。").
			FontSize(12).TextColor(t.TextMuted)
		switch {
		case h.checkedAt.IsZero():
			ui.Text(c, "还没有检查过。").FontSize(12).TextColor(t.TextMuted)
		case h.checking:
			ui.Text(c, fmt.Sprintf("正在检查 %d/%d 个模组…", h.checked, h.total)).
				FontSize(12).TextColor(t.TextMuted)
		default:
			ui.Text(c, "上次检查："+h.checkedAt.Local().Format("2006-01-02 15:04")).
				FontSize(12).TextColor(t.TextMuted)
		}
		if h.err != "" {
			ui.Text(c, h.err).FontSize(12).TextColor(t.Danger)
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, "检查兼容性").
				Disabled(s.install == nil || h.checking || h.busy).
				OnClick(func() { a.checkHealth(true) })
			ui.Button(c, "查看详情").
				Disabled(s.install == nil).
				OnClick(a.openCompatibility)
			ui.Text(c, fmt.Sprintf("规则版本 %d", modhealth.RulesRevision)).
				FontSize(11).TextColor(t.TextMuted)
		})
	})
}

// gameRunningNow reads the last poll, which is enough to disable a button; the
// write checks the process list again before it starts.
func (a *app) gameRunningNow() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gameRunning
}
