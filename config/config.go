package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// SpeedOption represents the typing speed setting
type SpeedOption string

const (
	SpeedDefault   SpeedOption = "default"
	SpeedMedium    SpeedOption = "medium"
	SpeedSlow      SpeedOption = "slow"
	SpeedSuperSlow SpeedOption = "superSlow"
	SpeedCustom    SpeedOption = "custom"
)

// CompatibilityMode represents the modifier compatibility setting
type CompatibilityMode string

const (
	CompatibilityAuto     CompatibilityMode = "auto"
	CompatibilityForceOn  CompatibilityMode = "forceOn"
	CompatibilityForceOff CompatibilityMode = "forceOff"
)

// HotkeyConfig holds the global hotkey configuration
type HotkeyConfig struct {
	Enabled  bool   `json:"enabled"`
	Modifier string `json:"modifier"` // "ctrl+alt", "ctrl+shift", "alt+shift", "ctrl+alt+shift"
	Key      string `json:"key"`      // single character like "V", "T", etc.
}

// Config holds all persistent application settings
type Config struct {
	// Typing speed settings
	DefaultSpeedOption SpeedOption `json:"defaultSpeedOption"`
	CustomSpeedMs      int         `json:"customSpeedMs"`

	// Keyboard layout setting
	KeyboardLayout string `json:"keyboardLayout"`

	// Compatibility mode setting
	CompatibilityMode CompatibilityMode `json:"compatibilityMode"`

	// Abort on focus change
	AbortOnFocusChange bool `json:"abortOnFocusChange"`

	// Interface language (empty = auto/system)
	Language string `json:"language"`

	// Always on top window setting
	AlwaysOnTop bool `json:"alwaysOnTop"`

	// Global hotkey for "Type Clipboard" action
	HotkeyClipboard HotkeyConfig `json:"hotkeyClipboard"`

	// Global hotkey for "Type Text" action
	HotkeyText HotkeyConfig `json:"hotkeyText"`

	// Legacy field for migration (deprecated)
	Hotkey HotkeyConfig `json:"hotkey,omitempty"`
}

// DefaultConfig returns the default configuration
func DefaultConfig() Config {
	return Config{
		DefaultSpeedOption: SpeedDefault,
		CustomSpeedMs:      0,
		KeyboardLayout:     "Auto (Use System)",
		CompatibilityMode:  CompatibilityAuto,
		AbortOnFocusChange: true,
		Language:           "",
		AlwaysOnTop:        false,
		HotkeyClipboard: HotkeyConfig{
			Enabled:  false,
			Modifier: "ctrl+alt",
			Key:      "V",
		},
		HotkeyText: HotkeyConfig{
			Enabled:  false,
			Modifier: "ctrl+alt",
			Key:      "T",
		},
	}
}

var (
	configPath string
	configMu   sync.RWMutex
	current    Config
)

func init() {
	// Determine config file path
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = "."
	}
	appConfigDir := filepath.Join(configDir, "goclip")
	configPath = filepath.Join(appConfigDir, "config.json")

	// Initialize with defaults
	current = DefaultConfig()
}

// GetConfigPath returns the path to the config file
func GetConfigPath() string {
	return configPath
}

// Load reads the configuration from disk
func Load() error {
	configMu.Lock()
	defer configMu.Unlock()

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No config file yet, use defaults
			current = DefaultConfig()
			return nil
		}
		return err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}

	// Validate and apply defaults for invalid values
	if cfg.DefaultSpeedOption == "" {
		cfg.DefaultSpeedOption = SpeedDefault
	}
	if cfg.CustomSpeedMs < 0 {
		cfg.CustomSpeedMs = 0
	}
	if cfg.CustomSpeedMs > 10000 {
		cfg.CustomSpeedMs = 10000
	}
	if cfg.KeyboardLayout == "" {
		cfg.KeyboardLayout = "Auto (Use System)"
	}
	if cfg.CompatibilityMode == "" {
		cfg.CompatibilityMode = CompatibilityAuto
	}

	// Migrate legacy hotkey config to new format
	if cfg.Hotkey.Enabled && !cfg.HotkeyClipboard.Enabled {
		cfg.HotkeyClipboard = cfg.Hotkey
		cfg.Hotkey = HotkeyConfig{} // Clear legacy field
	}

	// Set default hotkey modifiers if not set
	if cfg.HotkeyClipboard.Modifier == "" {
		cfg.HotkeyClipboard.Modifier = "ctrl+alt"
	}
	if cfg.HotkeyClipboard.Key == "" {
		cfg.HotkeyClipboard.Key = "V"
	}
	if cfg.HotkeyText.Modifier == "" {
		cfg.HotkeyText.Modifier = "ctrl+alt"
	}
	if cfg.HotkeyText.Key == "" {
		cfg.HotkeyText.Key = "T"
	}

	current = cfg
	return nil
}

// Save writes the current configuration to disk
func Save() error {
	configMu.RLock()
	cfg := current
	configMu.RUnlock()

	return SaveConfig(cfg)
}

// SaveConfig writes a specific configuration to disk
func SaveConfig(cfg Config) error {
	configMu.Lock()
	current = cfg
	configMu.Unlock()

	// Ensure directory exists
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
}

// Get returns a copy of the current configuration
func Get() Config {
	configMu.RLock()
	defer configMu.RUnlock()
	return current
}

// Set updates the current configuration in memory
func Set(cfg Config) {
	configMu.Lock()
	current = cfg
	configMu.Unlock()
}

// Update applies a function to modify the current configuration and saves it
func Update(fn func(*Config)) error {
	configMu.Lock()
	fn(&current)
	cfg := current
	configMu.Unlock()

	return SaveConfig(cfg)
}

// GetDefaultSpeedOption returns the configured default speed option
func GetDefaultSpeedOption() SpeedOption {
	configMu.RLock()
	defer configMu.RUnlock()
	return current.DefaultSpeedOption
}

// GetCustomSpeedMs returns the configured custom speed in milliseconds
func GetCustomSpeedMs() int {
	configMu.RLock()
	defer configMu.RUnlock()
	return current.CustomSpeedMs
}

// GetKeyboardLayout returns the configured keyboard layout
func GetKeyboardLayout() string {
	configMu.RLock()
	defer configMu.RUnlock()
	return current.KeyboardLayout
}

// GetCompatibilityMode returns the configured compatibility mode
func GetCompatibilityMode() CompatibilityMode {
	configMu.RLock()
	defer configMu.RUnlock()
	return current.CompatibilityMode
}

// GetAbortOnFocusChange returns the configured abort on focus change setting
func GetAbortOnFocusChange() bool {
	configMu.RLock()
	defer configMu.RUnlock()
	return current.AbortOnFocusChange
}

// GetLanguage returns the configured interface language
func GetLanguage() string {
	configMu.RLock()
	defer configMu.RUnlock()
	return current.Language
}

// GetAlwaysOnTop returns the configured always on top setting
func GetAlwaysOnTop() bool {
	configMu.RLock()
	defer configMu.RUnlock()
	return current.AlwaysOnTop
}

// GetHotkeyClipboard returns the configured clipboard hotkey settings
func GetHotkeyClipboard() HotkeyConfig {
	configMu.RLock()
	defer configMu.RUnlock()
	return current.HotkeyClipboard
}

// GetHotkeyText returns the configured text hotkey settings
func GetHotkeyText() HotkeyConfig {
	configMu.RLock()
	defer configMu.RUnlock()
	return current.HotkeyText
}
