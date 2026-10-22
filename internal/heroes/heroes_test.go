package heroes

import "testing"

// A hero's name, the Tauri app's key for it, and its aliases all name the same
// hero, however the punctuation and case differ.
func TestNormalize(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  string
	}{
		{"Yamato", "Yamato"},
		{"yamato", "Yamato"},
		{"  Mo & Krill ", "Mo & Krill"},
		{"MoKrill", "Mo & Krill"},
		{"mo and krill", "Mo & Krill"},
		{"LadyGeist", "Lady Geist"},
		{"lady geist", "Lady Geist"},
		{"DeadmanDanny", "Deadman Danny"},
		{"GreyTalon", "Grey Talon"},
		{"Vyper", "Vyper"},
		{"viper", "Vyper"},
		{"Bikinidicta", ""},
	} {
		got, ok := Normalize(tt.value)
		if got != tt.want || ok != (tt.want != "") {
			t.Errorf("Normalize(%q) = %q, %v; want %q", tt.value, got, ok, tt.want)
		}
	}
}

// A mod's title is where the catalogue leaves the hero out, so the names and
// aliases in it are how the app finds one.
func TestGuess(t *testing.T) {
	for _, tt := range []struct {
		title string
		want  string
	}{
		{"Bikini Vindicta", "Vindicta"},
		{"Hornet Recolor", "Vindicta"},
		{"Yamato v2 - Cyber", "Yamato"},
		{"Lady Geist retexture", "Lady Geist"},
		{"lady-geist", "Lady Geist"},
		{"Mo & Krill skin", "Mo & Krill"},
		{"MoKrill", "Mo & Krill"},
		{"Frank the Tank", "Victor"},
		{"Punkgoat", "Billy"},
		{"Seven 7", "Seven"},
		{"Nurse Harrow", "Nurse Harrow"},
		{"A cursor pack", ""},
		{"Hazelnut", ""},
	} {
		if got := Guess(tt.title); got != tt.want {
			t.Errorf("Guess(%q) = %q, want %q", tt.title, got, tt.want)
		}
	}
}

// The Skins tree's subcategories are heroes. Model Replacement says a mod is a
// skin without saying whose, and the generic sections are not heroes.
func TestFromSkinCategory(t *testing.T) {
	for _, tt := range []struct {
		category, subcategory string
		want                  string
	}{
		{"Skins", "Yamato", "Yamato"},
		{"Skins", "Mo & Krill", "Mo & Krill"},
		{"Skins", "New Hero", "New Hero"},
		{"Skins", "Other", ""},
		{"Skins", "Skins", ""},
		{"Skins", "", ""},
		{"Model Replacement", "Weapons", ""},
		{"HUD", "Yamato", ""},
	} {
		if got := FromSkinCategory(tt.category, tt.subcategory); got != tt.want {
			t.Errorf("FromSkinCategory(%q, %q) = %q, want %q",
				tt.category, tt.subcategory, got, tt.want)
		}
	}
}

// What a mod belongs to, in the order the original app decides it: a manual
// override, then the category, then the title.
func TestResolve(t *testing.T) {
	for _, tt := range []struct {
		override, category, subcategory, title string
		want                                   string
	}{
		{"", "Skins", "Yamato", "Bikini Vindicta", "Yamato"},
		{"", "Skins", "Yamato", "anything", "Yamato"},
		{"", "HUD", "Cursors", "Yamato cursor", "Yamato"},
		{"", "HUD", "Cursors", "Cursor pack", ""},
		{"Vindicta", "Skins", "Yamato", "anything", "Vindicta"},
		{"vindicta", "", "", "", "Vindicta"},
		{"Some New Hero", "Skins", "Yamato", "anything", "Some New Hero"},
		{Generic, "Skins", "Yamato", "Bikini Vindicta", ""},
	} {
		got := Resolve(tt.override, tt.category, tt.subcategory, tt.title)
		if got != tt.want {
			t.Errorf("Resolve(%q, %q, %q, %q) = %q, want %q",
				tt.override, tt.category, tt.subcategory, tt.title, got, tt.want)
		}
	}
}

func TestIsSkinCategory(t *testing.T) {
	for category, want := range map[string]bool{
		"Skins":             true,
		"Model Replacement": true,
		"HUD":               false,
		"Audio":             false,
		"":                  false,
	} {
		if got := IsSkinCategory(category); got != want {
			t.Errorf("IsSkinCategory(%q) = %v, want %v", category, got, want)
		}
	}
}

// The roster and its names stay in step: every definition is a hero, and the
// names are the ones the window shows.
func TestNamesAreTheDefinitions(t *testing.T) {
	if len(Names) != len(Definitions) {
		t.Fatalf("%d names for %d definitions", len(Names), len(Definitions))
	}
	for i, definition := range Definitions {
		if Names[i] != definition.Name {
			t.Errorf("Names[%d] = %q, want %q", i, Names[i], definition.Name)
		}
		if !IsHero(definition.Name) {
			t.Errorf("%q is not a hero by name", definition.Name)
		}
		if hero, ok := Normalize(definition.Key); !ok || hero != definition.Name {
			t.Errorf("Normalize(%q) = %q, %v; want %q", definition.Key, hero, ok, definition.Name)
		}
	}
}
