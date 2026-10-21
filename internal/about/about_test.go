package about

import (
	"runtime/debug"
	"testing"
)

func TestShortCommit(t *testing.T) {
	const revision = "0123456789abcdef0123456789abcdef01234567"
	tests := []struct {
		name     string
		settings []debug.BuildSetting
		want     string
	}{
		{
			name:     "full revision",
			settings: []debug.BuildSetting{{Key: "vcs.revision", Value: revision}},
			want:     "0123456",
		},
		{
			name:     "short revision",
			settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc1234"}},
			want:     "abc1234",
		},
		{
			name: "modified tree",
			settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: revision},
				{Key: "vcs.modified", Value: "true"},
			},
			want: "0123456-dirty",
		},
		{
			name: "clean tree",
			settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: revision},
				{Key: "vcs.modified", Value: "false"},
			},
			want: "0123456",
		},
		{
			name:     "no version control information",
			settings: nil,
			want:     "unknown",
		},
		{
			name:     "modified without a revision",
			settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}},
			want:     "unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shortCommit(tt.settings); got != tt.want {
				t.Errorf("shortCommit(%v) = %q, want %q", tt.settings, got, tt.want)
			}
		})
	}
}

// The user agent names the same version the settings page shows.
func TestUserAgentNamesTheVersion(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = "0.2.0"

	want := "MyLock/0.2.0 (+https://github.com/BabyLy233/mylock)"
	if got := UserAgent(); got != want {
		t.Errorf("UserAgent() = %q, want %q", got, want)
	}
}
