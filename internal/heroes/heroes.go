// Package heroes works out which Deadlock hero a mod is for, and which names
// are the same hero spelled differently.
//
// The catalogue's category tree is what a person browses by, so this package
// does not decide which heroes exist; it only answers whether a name is a hero
// the app already knows, and guesses one from a mod's title when neither the
// category nor the user said.
//
// The names, aliases and word patterns are the Tauri app's (HERO_DEFINITIONS),
// carried over so that the two agree about a mod. A hero added to the game
// after them still resolves from its skin category, which is what the API
// carries and what the store filters by.
package heroes

import (
	"regexp"
	"strings"
)

// SkinsCategory is the root category whose subcategories are heroes.
const SkinsCategory = "Skins"

// ModelReplacementCategory is the other root a skin can be filed under. Its
// subcategories are the parts of the game replaced rather than heroes, so it
// says a mod is a skin but not whose.
const ModelReplacementCategory = "Model Replacement"

// Generic is what the library stores for a mod the user assigned to
// 「通用/其他」. An empty override means automatic; this one is a decision, so
// it is a value of its own rather than an empty string.
const Generic = "generic"

// genericCategories are the Skins subcategories that name a kind of skin rather
// than a hero, which is why they are not heroes themselves.
var genericCategories = map[string]bool{"Skins": true, "Other": true}

// Definition is one hero: its canonical name, the key the Tauri app knows it
// by, and the words its mods are titled with.
type Definition struct {
	// Name is what the app shows and the API's Skins tree calls it.
	Name string
	// Key is the Tauri app's enum key, which a mod title or a hand-edited file
	// may spell instead of the name.
	Key string
	// Aliases are other names the hero is called by.
	Aliases []string
	// FuzzyTokens match inside a title as whole words.
	FuzzyTokens []string
	// Phrases match several words in order.
	Phrases [][]string
	// RegexSnippets match with punctuation between the words, which a title
	// often uses where a name has a space.
	RegexSnippets []string
}

// Definitions are the heroes this app knows how to guess, in the order the
// Tauri app lists them. The order decides which of two matching heroes wins,
// so it is kept as it is.
var Definitions = []Definition{
	{Name: "Abrams", Key: "Abrams", Aliases: []string{"atlas", "atlas detective", "atlas detective v2"}, FuzzyTokens: []string{"brams"}},
	{Name: "Apollo", Key: "Apollo", Aliases: []string{"fencer"}},
	{Name: "Baba", Key: "Baba"},
	{Name: "Bebop", Key: "Bebop"},
	{Name: "Billy", Key: "Billy", Aliases: []string{"punkgoat"}},
	{Name: "Calico", Key: "Calico", Aliases: []string{"cadence", "nano"}},
	{Name: "Celeste", Key: "Celeste", Aliases: []string{"unicorn"}},
	{Name: "Deadman Danny", Key: "DeadmanDanny", Aliases: []string{"deadmandanny", "deadpack"}, Phrases: [][]string{{"deadman", "danny"}}},
	{Name: "Doorman", Key: "Doorman", Aliases: []string{"doorman v2", "the doorman"}},
	{Name: "Drifter", Key: "Drifter"},
	{Name: "Dynamo", Key: "Dynamo", Aliases: []string{"prof dynamo"}},
	{Name: "Graves", Key: "Graves", Aliases: []string{"necro"}},
	{
		Name: "Grey Talon", Key: "GreyTalon",
		Aliases:     []string{"archer", "archer v2", "greytalon"},
		FuzzyTokens: []string{"talon"},
		Phrases:     [][]string{{"grey", "talon"}},
	},
	{Name: "Haze", Key: "Haze", Aliases: []string{"haze v2"}},
	{Name: "Holliday", Key: "Holliday", Aliases: []string{"astro"}},
	{Name: "Infernus", Key: "Infernus", Aliases: []string{"inferno", "inferno v4"}, FuzzyTokens: []string{"fernus"}},
	{Name: "Ivy", Key: "Ivy", Aliases: []string{"tengu"}},
	{Name: "Kelvin", Key: "Kelvin", Aliases: []string{"kelvin explorer", "kelvin v2"}},
	{
		Name: "Lady Geist", Key: "LadyGeist",
		Aliases:       []string{"geist", "ghost", "ladygeist"},
		FuzzyTokens:   []string{"geist", "gaist"},
		Phrases:       [][]string{{"lady", "geist"}},
		RegexSnippets: []string{`lady[^a-z0-9]*geist`},
	},
	{Name: "Lash", Key: "Lash", Aliases: []string{"lash v2"}},
	{Name: "McGinnis", Key: "McGinnis", Aliases: []string{"engineer"}},
	{Name: "Mina", Key: "Mina", Aliases: []string{"vampirebat"}},
	{Name: "Mirage", Key: "Mirage", Aliases: []string{"mirage v2"}},
	{
		Name: "Mo & Krill", Key: "MoKrill",
		Aliases:       []string{"digger", "mo krill", "mokrill"},
		FuzzyTokens:   []string{"krill"},
		Phrases:       [][]string{{"mo", "krill"}, {"mo", "and", "krill"}},
		RegexSnippets: []string{`mo[^a-z0-9]*krill`},
	},
	// "nurse" (Nurse Harrow) and "artist" (Violet) are common words in mod
	// titles, so neither is an alias on its own.
	{Name: "Nurse Harrow", Key: "NurseHarrow", Aliases: []string{"nurseharrow"}, Phrases: [][]string{{"nurse", "harrow"}}},
	{Name: "Paige", Key: "Paige", Aliases: []string{"bookworm"}},
	{Name: "Paradox", Key: "Paradox", Aliases: []string{"chrono"}, FuzzyTokens: []string{"dox"}},
	{Name: "Pocket", Key: "Pocket", Aliases: []string{"synth"}},
	{Name: "Rat King", Key: "RatKing", Aliases: []string{"ratking"}, Phrases: [][]string{{"rat", "king"}}},
	{Name: "Rem", Key: "Rem", Aliases: []string{"familiar"}},
	{Name: "Seven", Key: "Seven", Aliases: []string{"7", "gigawatt", "gigawatt prisoner"}},
	{Name: "Shiv", Key: "Shiv", Aliases: []string{"shiv ult"}},
	{Name: "Silver", Key: "Silver", Aliases: []string{"werewolf"}},
	{Name: "Sinclair", Key: "Sinclair", Aliases: []string{"magician", "magician v2"}},
	{Name: "Solomon", Key: "Solomon", Aliases: []string{"chessmaster"}},
	{Name: "Venator", Key: "Venator", Aliases: []string{"priest"}},
	{Name: "Victor", Key: "Victor", Aliases: []string{"frank", "frank v2", "viktor"}},
	{Name: "Vindicta", Key: "Vindicta", Aliases: []string{"hornet", "hornet v3"}},
	{Name: "Violet", Key: "Violet"},
	{Name: "Viscous", Key: "Viscous"},
	{Name: "Vyper", Key: "Vyper", Aliases: []string{"viper"}},
	{Name: "Warden", Key: "Warden"},
	{Name: "Wraith", Key: "Wraith", Aliases: []string{"wraith gen man", "wraith magician", "wraith puppeteer"}},
	{Name: "Wrecker", Key: "Wrecker", Aliases: []string{"butcher"}},
	{Name: "Yamato", Key: "Yamato", Aliases: []string{"yamato v2"}},
}

// Names are the heroes' display names, in the order Definitions lists them. It
// is a fallback for a catalogue that could not be read, not the list the
// window browses by.
var Names = func() []string {
	names := make([]string, len(Definitions))
	for i, definition := range Definitions {
		names[i] = definition.Name
	}
	return names
}()

// nameSet reports whether name is one of the heroes this package knows, which
// a category tree can add to.
var nameSet = func() map[string]bool {
	set := make(map[string]bool, len(Definitions))
	for _, definition := range Definitions {
		set[definition.Name] = true
	}
	return set
}()

// Normalize returns the canonical name of a hero spelled value, and whether it
// is one. The spelling is compared the way the Tauri app compares it: case
// folded, punctuation dropped, and "&" read as "and".
func Normalize(value string) (string, bool) {
	hero, ok := aliases[normalizeKey(value)]
	return hero, ok
}

// IsHero reports whether name is a hero this package knows by name.
func IsHero(name string) bool {
	return nameSet[name]
}

// Guess returns the hero a mod title names, empty when it names none.
func Guess(title string) string {
	if hero, ok := Normalize(title); ok {
		return hero
	}
	lowered := strings.ToLower(title)
	for _, pattern := range patterns {
		if pattern.re.MatchString(lowered) {
			return pattern.hero
		}
	}
	return ""
}

// FromSkinCategory returns the hero a skin's categories name. Only the Skins
// tree lists heroes; a subcategory that names a kind of skin rather than a hero
// is not one. A name the hardcoded roster does not know is returned as it is:
// the catalogue is what says which heroes exist.
func FromSkinCategory(category, subcategory string) string {
	if category != SkinsCategory || subcategory == "" || genericCategories[subcategory] {
		return ""
	}
	if hero, ok := Normalize(subcategory); ok {
		return hero
	}
	return subcategory
}

// Resolve is the hero a mod belongs to: the user's override, then the skin
// category it is filed under, then its title. An override of Generic puts it in
// 「通用/其他」, which is the empty result, and an override the roster does not
// know is kept as it was written.
func Resolve(override, category, subcategory, title string) string {
	if override != "" {
		if override == Generic {
			return ""
		}
		if hero, ok := Normalize(override); ok {
			return hero
		}
		return override
	}
	if hero := FromSkinCategory(category, subcategory); hero != "" {
		return hero
	}
	return Guess(title)
}

// IsSkinCategory reports whether a root category holds skins, which is what the
// skins page shows.
func IsSkinCategory(category string) bool {
	return category == SkinsCategory || category == ModelReplacementCategory
}

// normalizeKey is the spelling two hero names are compared by.
func normalizeKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("'", "", "`", "", "&", " and ").Replace(value)
	var b strings.Builder
	space := false
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			space = false
			continue
		}
		if !space && b.Len() > 0 {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSuffix(b.String(), " ")
}

// tokenPattern is a pattern that matches pattern between word boundaries, which
// is how a title is searched for a name.
func tokenPattern(pattern string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^a-z0-9])(?:` + pattern + `)(?:$|[^a-z0-9])`)
}

// word is a token pattern for a literal word.
func word(value string) *regexp.Regexp {
	return tokenPattern(regexp.QuoteMeta(value))
}

// phrase is a token pattern for several words in order, however they are
// separated in the title.
func phrase(words ...string) *regexp.Regexp {
	escaped := make([]string, len(words))
	for i, w := range words {
		escaped[i] = regexp.QuoteMeta(w)
	}
	return tokenPattern(strings.Join(escaped, `[^a-z0-9]+`))
}

// matching is a title pattern and the hero it stands for.
type matching struct {
	hero string
	re   *regexp.Regexp
}

// aliases and patterns are built once from Definitions.
var aliases, patterns = build()

func build() (map[string]string, []matching) {
	aliases := make(map[string]string)
	var patterns []matching
	seen := make(map[string]map[string]bool)

	for _, definition := range Definitions {
		known := []string{definition.Name, definition.Key}
		known = append(known, definition.Aliases...)
		for _, alias := range known {
			key := normalizeKey(alias)
			if key == "" {
				continue
			}
			// The first hero a spelling names keeps it. Two heroes cannot both
			// be right, and the roster's order is the Tauri app's.
			if _, taken := aliases[key]; !taken {
				aliases[key] = definition.Name
			}
		}

		if seen[definition.Name] == nil {
			seen[definition.Name] = make(map[string]bool)
		}
		add := func(re *regexp.Regexp, key string) {
			if seen[definition.Name][key] {
				return
			}
			seen[definition.Name][key] = true
			patterns = append(patterns, matching{hero: definition.Name, re: re})
		}

		// A one-word name is matched as a word of a title, so that "Haze"
		// matches "Haze skin" and not "hazelnut".
		normalized := normalizeKey(definition.Name)
		if !strings.Contains(normalized, " ") {
			add(word(normalized), normalized)
		}
		for _, token := range definition.FuzzyTokens {
			if key := normalizeKey(token); key != "" {
				add(word(key), key)
			}
		}
		for _, alias := range definition.Aliases {
			parts := strings.Fields(normalizeKey(alias))
			switch len(parts) {
			case 0:
				continue
			case 1:
				add(word(parts[0]), parts[0])
			default:
				add(phrase(parts...), strings.Join(parts, " "))
			}
		}
		for _, words := range definition.Phrases {
			add(phrase(words...), strings.Join(words, " "))
		}
		for _, snippet := range definition.RegexSnippets {
			add(tokenPattern(snippet), snippet)
		}
	}
	return aliases, patterns
}
