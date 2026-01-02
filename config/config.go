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
