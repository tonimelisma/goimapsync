// Package config resolves pimvault's directories and loads config.toml.
//
// Every directory is resolved in the same order: command-line flag, then
// environment variable, then config.toml, then the XDG default. Resolution is
// deterministic; only Load touches the filesystem. The tool never writes
// config.toml; it holds only the user's settings.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const appName = "pimvault"

// Environment variables that override directories.
const (
	EnvArchive = "PIMVAULT_ARCHIVE"
	EnvConfig  = "PIMVAULT_CONFIG"
	EnvState   = "PIMVAULT_STATE"
)

// File is the content of config.toml.
type File struct {
	Archive string `toml:"archive"`
	State   string `toml:"state"`

	// Client IDs override the ones built into pimvault (D136).
	GoogleClientID     string `toml:"google_client_id"`
	GoogleClientSecret string `toml:"google_client_secret"`
	MicrosoftClientID  string `toml:"microsoft_client_id"`
}

// Flags holds directory overrides given on the command line. Empty means unset.
type Flags struct {
	Archive string
	Config  string
	State   string
}

// Env looks up an environment variable, like os.LookupEnv.
type Env func(key string) (string, bool)

// Paths are the resolved locations the tool works with.
type Paths struct {
	// ConfigFile is config.toml; it may not exist.
	ConfigFile string
	// ConfigDir holds config.toml and the account token files.
	ConfigDir string
	// Archive is the root of the archive of .eml, .ics and .vcf files.
	Archive string
	// State holds the tool's own sync state.
	State string
}

// ConfigFile is the location of config.toml and how it was chosen.
type ConfigFile struct {
	Path string
	// Explicit is true when the path came from --config or PIMVAULT_CONFIG.
	// An explicit file must exist; the XDG default may be missing.
	Explicit bool
}

// ConfigFilePath resolves where config.toml is: flag, then PIMVAULT_CONFIG,
// then $XDG_CONFIG_HOME/pimvault/config.toml.
func ConfigFilePath(flags Flags, env Env, home string) (ConfigFile, error) {
	if path, ok := firstSet(flags.Config, lookup(env, EnvConfig)); ok {
		abs, err := absolute(path)
		if err != nil {
			return ConfigFile{}, err
		}

		return ConfigFile{Path: abs, Explicit: true}, nil
	}

	base, err := xdgDir(env, "XDG_CONFIG_HOME", home, ".config")
	if err != nil {
		return ConfigFile{}, err
	}

	return ConfigFile{Path: filepath.Join(base, appName, "config.toml")}, nil
}

// Resolve computes all paths from flags, environment and the loaded config
// file. configFile must come from ConfigFilePath.
func Resolve(configFile ConfigFile, file *File, flags Flags, env Env, home string) (Paths, error) {
	archive, err := resolveDir(flags.Archive, lookup(env, EnvArchive), file.Archive, home, "archive",
		func() (string, error) { return xdgAppDir(env, "XDG_DATA_HOME", home, filepath.Join(".local", "share")) })
	if err != nil {
		return Paths{}, err
	}

	state, err := resolveDir(flags.State, lookup(env, EnvState), file.State, home, "state",
		func() (string, error) {
			return xdgAppDir(env, "XDG_STATE_HOME", home, filepath.Join(".local", "state"))
		})
	if err != nil {
		return Paths{}, err
	}

	return Paths{
		ConfigFile: configFile.Path,
		ConfigDir:  filepath.Dir(configFile.Path),
		Archive:    archive,
		State:      state,
	}, nil
}

// Load reads config.toml. A missing default file is not an error: defaults
// apply. A missing explicit file is, because tokens follow the config
// directory and a typo would silently switch to another account registry.
// Unknown keys are an error, so a typo cannot silently fall back to a default.
func Load(configFile ConfigFile) (File, error) {
	path := configFile.Path

	data, err := os.ReadFile(path) //nolint:gosec // G304: the config path is chosen by the user or XDG.
	if errors.Is(err, fs.ErrNotExist) && !configFile.Explicit {
		return File{}, nil
	}

	if err != nil {
		return File{}, fmt.Errorf("read config %s: %w", path, err)
	}

	var file File

	meta, err := toml.Decode(string(data), &file)
	if err != nil {
		return File{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}

		return File{}, fmt.Errorf("parse config %s: unknown keys: %s", path, strings.Join(keys, ", "))
	}

	return file, nil
}

// resolveDir picks the first set value in precedence order. Flag and
// environment values may be relative to the working directory. Config file
// values must be absolute or start with ~/ (the home directory), because a
// config file has no working directory.
func resolveDir(flag, env, fromFile, home, key string, fallback func() (string, error)) (string, error) {
	if path, ok := firstSet(flag, env); ok {
		return absolute(path)
	}

	if fromFile == "" {
		return fallback()
	}

	if rest, ok := strings.CutPrefix(fromFile, "~/"); ok {
		if home == "" {
			return "", fmt.Errorf("resolve %s directory: config value %q: home directory unknown", key, fromFile)
		}

		return filepath.Join(home, rest), nil
	}

	if !filepath.IsAbs(fromFile) {
		return "", fmt.Errorf("resolve %s directory: config value %q must be absolute or start with ~/", key, fromFile)
	}

	return filepath.Clean(fromFile), nil
}

// xdgAppDir returns <XDG base>/pimvault.
func xdgAppDir(env Env, variable, home, defaultRel string) (string, error) {
	base, err := xdgDir(env, variable, home, defaultRel)
	if err != nil {
		return "", err
	}

	return filepath.Join(base, appName), nil
}

// xdgDir returns an XDG base directory. Per the XDG Base Directory spec, an
// unset, empty or relative value is ignored in favor of the default under home.
func xdgDir(env Env, variable, home, defaultRel string) (string, error) {
	if value := lookup(env, variable); value != "" && filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}

	if home == "" {
		return "", fmt.Errorf("resolve %s: home directory unknown", variable)
	}

	return filepath.Join(home, defaultRel), nil
}

func lookup(env Env, key string) string {
	value, _ := env(key)

	return value
}

func firstSet(values ...string) (string, bool) {
	for _, value := range values {
		if value != "" {
			return value, true
		}
	}

	return "", false
}

func absolute(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}

	return abs, nil
}
