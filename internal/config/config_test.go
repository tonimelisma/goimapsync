package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const home = "/home/tester"

func envOf(vars map[string]string) Env {
	return func(key string) (string, bool) {
		value, ok := vars[key]

		return value, ok
	}
}

func TestConfigFilePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		flags Flags
		env   map[string]string
		want  string
	}{
		{name: "xdg default", want: "/home/tester/.config/pimvault/config.toml"},
		{name: "xdg config home", env: map[string]string{"XDG_CONFIG_HOME": "/xdg"}, want: "/xdg/pimvault/config.toml"},
		{name: "relative xdg ignored", env: map[string]string{"XDG_CONFIG_HOME": "rel"}, want: "/home/tester/.config/pimvault/config.toml"},
		{name: "env beats xdg", env: map[string]string{EnvConfig: "/env/c.toml", "XDG_CONFIG_HOME": "/xdg"}, want: "/env/c.toml"},
		{name: "flag beats env", flags: Flags{Config: "/flag/c.toml"}, env: map[string]string{EnvConfig: "/env/c.toml"}, want: "/flag/c.toml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ConfigFilePath(tt.flags, envOf(tt.env), home)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.Path)
			assert.Equal(t, tt.flags.Config != "" || tt.env[EnvConfig] != "", got.Explicit)
		})
	}
}

func TestResolve_Precedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		flags       Flags
		env         map[string]string
		file        File
		wantArchive string
		wantState   string
	}{
		{
			name:        "xdg defaults",
			wantArchive: "/home/tester/.local/share/pimvault",
			wantState:   "/home/tester/.local/state/pimvault",
		},
		{
			name:        "xdg variables",
			env:         map[string]string{"XDG_DATA_HOME": "/data", "XDG_STATE_HOME": "/state"},
			wantArchive: "/data/pimvault",
			wantState:   "/state/pimvault",
		},
		{
			name:        "config file beats xdg",
			env:         map[string]string{"XDG_DATA_HOME": "/data"},
			file:        File{Archive: "/file/archive", State: "/file/state"},
			wantArchive: "/file/archive",
			wantState:   "/file/state",
		},
		{
			name:        "env beats config file",
			env:         map[string]string{EnvArchive: "/env/archive", EnvState: "/env/state"},
			file:        File{Archive: "/file/archive", State: "/file/state"},
			wantArchive: "/env/archive",
			wantState:   "/env/state",
		},
		{
			name:        "flag beats env",
			flags:       Flags{Archive: "/flag/archive", State: "/flag/state"},
			env:         map[string]string{EnvArchive: "/env/archive", EnvState: "/env/state"},
			wantArchive: "/flag/archive",
			wantState:   "/flag/state",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			paths, err := Resolve(ConfigFile{Path: "/cfg/pimvault/config.toml"}, &tt.file, tt.flags, envOf(tt.env), home)
			require.NoError(t, err)
			assert.Equal(t, tt.wantArchive, paths.Archive)
			assert.Equal(t, tt.wantState, paths.State)
			assert.Equal(t, "/cfg/pimvault", paths.ConfigDir)
		})
	}
}

func TestResolve_RelativeFlagIsMadeAbsolute(t *testing.T) {
	t.Parallel()

	paths, err := Resolve(ConfigFile{Path: "/cfg/config.toml"}, &File{}, Flags{Archive: "archive"}, envOf(nil), home)
	require.NoError(t, err)

	wd, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(wd, "archive"), paths.Archive)
}

func TestResolve_RelativeConfigValueRejected(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"relative/archive", "~archive", "~user/archive"} {
		_, err := Resolve(ConfigFile{Path: "/cfg/config.toml"}, &File{Archive: value}, Flags{}, envOf(nil), home)
		require.Error(t, err, value)
		assert.Contains(t, err.Error(), "must be absolute or start with ~/")
	}
}

func TestResolve_TildeConfigValueUsesHome(t *testing.T) {
	t.Parallel()

	paths, err := Resolve(ConfigFile{Path: "/cfg/config.toml"}, &File{Archive: "~/mail-backup", State: "~/.cache/../state"},
		Flags{}, envOf(nil), home)
	require.NoError(t, err)
	assert.Equal(t, "/home/tester/mail-backup", paths.Archive)
	assert.Equal(t, "/home/tester/state", paths.State)

	_, err = Resolve(ConfigFile{Path: "/cfg/config.toml"}, &File{Archive: "~/mail-backup"}, Flags{}, envOf(nil), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "home directory unknown")
}

func TestResolve_NoHomeAndNoOverrides(t *testing.T) {
	t.Parallel()

	_, err := Resolve(ConfigFile{Path: "/cfg/config.toml"}, &File{}, Flags{}, envOf(nil), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "home directory unknown")
}

func explicit(path string) ConfigFile {
	return ConfigFile{Path: path, Explicit: true}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

func TestLoad_MissingFileUsesDefaults(t *testing.T) {
	t.Parallel()

	file, err := Load(ConfigFile{Path: filepath.Join(t.TempDir(), "config.toml")})
	require.NoError(t, err)
	assert.Equal(t, File{}, file)
}

func TestLoad_MissingExplicitFileIsAnError(t *testing.T) {
	t.Parallel()

	_, err := Load(ConfigFile{Path: filepath.Join(t.TempDir(), "confg.toml"), Explicit: true})
	require.Error(t, err)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestLoad_ValidFile(t *testing.T) {
	t.Parallel()

	file, err := Load(explicit(writeConfig(t, "# my settings\narchive = \"/srv/pimvault\"\nstate = \"/var/lib/pimvault\"\n")))
	require.NoError(t, err)
	assert.Equal(t, File{Archive: "/srv/pimvault", State: "/var/lib/pimvault"}, file)
}

func TestLoad_UnknownKeyIsAnError(t *testing.T) {
	t.Parallel()

	_, err := Load(explicit(writeConfig(t, "archive_dir = \"/srv/pimvault\"\n[extra]\nkey = 1\n")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown keys: archive_dir, extra, extra.key")
}

func TestLoad_SyntaxErrorReportsPosition(t *testing.T) {
	t.Parallel()

	_, err := Load(explicit(writeConfig(t, "archive = \"/srv\"\nstate = \n")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 2")
}

func TestLoad_UnreadableFile(t *testing.T) {
	t.Parallel()

	// A directory where the file should be cannot be read as a file.
	dir := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.Mkdir(dir, 0o700))

	_, err := Load(ConfigFile{Path: dir})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read config")
}
