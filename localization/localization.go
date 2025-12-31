package localization

import (
	"strings"

	locale "github.com/jeandeaual/go-locale"
)

type LabelSet struct {
	AppTitle                         string
	InputPlaceholder                 string
	StatusReady                      string
	TargetWindowHeading              string
	ClearButton                      string
	RefreshWindowsButton             string
	WindowPlaceholder                string
	LastActiveFormat                 string
	LastActiveNone                   string
	FoundWindowsFormat               string
	KeyboardLayoutHeading            string
	TypingSpeedHeading               string
	SpeedDefault                     string
	SpeedMedium                      string
	SpeedSlow                        string
	SpeedSuperSlow                   string
	SpeedCustom                      string
	CustomMsPlaceholder              string
	DelayLabelFormat                 string
	TextToTypeHeading                string
	TypeButton                       string
	TypeClipboardButton              string
	StopButton                       string
	StatusNoWindow                   string
	StatusWindowUnavailable          string
	StatusNothingToType              string
	StatusTyping                     string
	StatusStopping                   string
	StatusTypingStopped              string
	StatusTypingErrorFormat          string
	StatusTypedToFormat              string
	StatusClipboardEmpty             string
	StatusTypingClipboard            string
	StatusTypingClipboardErrorFormat string
	StatusTypedClipboardFormat       string
	StatusSelectionCleared           string
	StatusWatcherWarningFormat       string
	LanguageHeading                  string
	LanguageAutoOption               string
	CompatibilityModeHeading         string
	CompatibilityModeAuto            string
	CompatibilityModeOn              string
	CompatibilityModeOff             string
	CompatibilityStatusFormat        string
	CompatibilityStatusActive        string
	CompatibilityStatusInactive      string
	CompatibilityStatusUnknown       string
	CompatibilityHelpTitle           string
	CompatibilityHelpMessage         string
	AbortOnFocusChange               string
	HotkeyInfo                       string
}

type LanguageMetadata struct {
	Code       string
	NativeName string
}

type languageDefinition struct {
	metadata LanguageMetadata
	labels   LabelSet
}

var (
	defaultCode = "en"
	languages   = []languageDefinition{
		{
			metadata: LanguageMetadata{Code: "en", NativeName: "English"},
			labels: LabelSet{
				AppTitle:                         "goclip",
				InputPlaceholder:                 "Type here…",
				StatusReady:                      "Ready.",
				TargetWindowHeading:              "Target Window",
				ClearButton:                      "Clear",
				RefreshWindowsButton:             "Refresh windows",
				WindowPlaceholder:                "None (use last active)",
				LastActiveFormat:                 "Last active: %s",
				LastActiveNone:                   "(none)",
				FoundWindowsFormat:               "Found %d windows.",
				KeyboardLayoutHeading:            "Keyboard Layout",
				TypingSpeedHeading:               "Typing Speed",
				SpeedDefault:                     "Default (Auto)",
				SpeedMedium:                      "Medium (50 ms)",
				SpeedSlow:                        "Slow (100 ms)",
				SpeedSuperSlow:                   "Super Slow (250 ms)",
				SpeedCustom:                      "Custom",
				CustomMsPlaceholder:              "ms per char",
				DelayLabelFormat:                 "Per-character delay: %d ms",
				TextToTypeHeading:                "Text to type",
				TypeButton:                       "Type",
				TypeClipboardButton:              "Type Clipboard",
				StopButton:                       "Stop",
				StatusNoWindow:                   "No window focused yet. Click a window then come back.",
				StatusWindowUnavailable:          "Selected window is no longer available.",
				StatusNothingToType:              "Nothing to type.",
				StatusTyping:                     "Typing...",
				StatusStopping:                   "Stopping typing...",
				StatusTypingStopped:              "Typing stopped by user.",
				StatusTypingErrorFormat:          "Error typing: %s",
				StatusTypedToFormat:              "Typed to: %s",
				StatusClipboardEmpty:             "Clipboard is empty.",
				StatusTypingClipboard:            "Typing clipboard...",
				StatusTypingClipboardErrorFormat: "Error typing clipboard: %s",
				StatusTypedClipboardFormat:       "Typed clipboard to: %s",
				StatusSelectionCleared:           "Selection cleared → using last active window.",
				StatusWatcherWarningFormat:       "Warning: foreground watcher failed, falling back: %s",
				LanguageHeading:                  "Interface Language",
				LanguageAutoOption:               "Auto (System)",
				CompatibilityModeHeading:         "Modifier Compatibility",
				CompatibilityModeAuto:            "Auto (Known apps)",
				CompatibilityModeOn:              "Force On",
				CompatibilityModeOff:             "Force Off",
				CompatibilityStatusFormat:        "Modifier compatibility: %s",
				CompatibilityStatusActive:        "Active",
				CompatibilityStatusInactive:      "Inactive",
				CompatibilityStatusUnknown:       "Unknown (no target)",
				CompatibilityHelpTitle:           "Modifier compatibility",
				CompatibilityHelpMessage:         "Some apps may not detect Alt, Shift, or AltGr correctly. Auto: Applies a fix for known apps like Citrix Workspace or HPE iLO. Always on: Always apply the fix. Off: Never apply the fix.",
				AbortOnFocusChange:               "Abort on focus change",
				HotkeyInfo:                       "Hotkey: Ctrl+G",
			},
		},
		{
			metadata: LanguageMetadata{Code: "de", NativeName: "Deutsch"},
			labels: LabelSet{
				AppTitle:                         "goclip",
				InputPlaceholder:                 "Hier tippen…",
				StatusReady:                      "Bereit.",
				TargetWindowHeading:              "Zielfenster",
				ClearButton:                      "Auswahl aufheben",
				RefreshWindowsButton:             "Fensterliste aktualisieren",
				WindowPlaceholder:                "Keine (zuletzt aktiv)",
				LastActiveFormat:                 "Zuletzt aktiv: %s",
				LastActiveNone:                   "(keins)",
				FoundWindowsFormat:               "%d Fenster gefunden.",
				KeyboardLayoutHeading:            "Tastaturlayout",
				TypingSpeedHeading:               "Schreibgeschwindigkeit",
				SpeedDefault:                     "Standard (Auto)",
				SpeedMedium:                      "Mittel (50 ms)",
				SpeedSlow:                        "Langsam (100 ms)",
				SpeedSuperSlow:                   "Sehr langsam (250 ms)",
				SpeedCustom:                      "Benutzerdefiniert",
				CustomMsPlaceholder:              "ms pro Zeichen",
				DelayLabelFormat:                 "Verzögerung pro Zeichen: %d ms",
				TextToTypeHeading:                "Einzugebender Text",
				TypeButton:                       "Tippen",
				TypeClipboardButton:              "Zwischenablage tippen",
				StopButton:                       "Stopp",
				StatusNoWindow:                   "Kein Fenster fokussiert. Bitte Fenster auswählen und zurückkehren.",
				StatusWindowUnavailable:          "Ausgewähltes Fenster ist nicht mehr verfügbar.",
				StatusNothingToType:              "Kein Text zum Tippen.",
				StatusTyping:                     "Tippe...",
				StatusStopping:                   "Tippen wird gestoppt...",
				StatusTypingStopped:              "Tippen vom Benutzer gestoppt.",
				StatusTypingErrorFormat:          "Fehler beim Tippen: %s",
				StatusTypedToFormat:              "Getippt nach: %s",
				StatusClipboardEmpty:             "Zwischenablage ist leer.",
				StatusTypingClipboard:            "Zwischenablage wird getippt...",
				StatusTypingClipboardErrorFormat: "Fehler beim Tippen aus der Zwischenablage: %s",
				StatusTypedClipboardFormat:       "Zwischenablage getippt nach: %s",
				StatusSelectionCleared:           "Auswahl entfernt → zuletzt aktives Fenster wird verwendet.",
				StatusWatcherWarningFormat:       "Warnung: Vordergrundüberwachung fehlgeschlagen, Fallback: %s",
				LanguageHeading:                  "Anzeigesprache",
				LanguageAutoOption:               "Automatisch (System)",
				CompatibilityModeHeading:         "Modifikatorkompatibilität",
				CompatibilityModeAuto:            "Auto (bekannte Apps)",
				CompatibilityModeOn:              "Immer aktiv",
				CompatibilityModeOff:             "Deaktiviert",
				CompatibilityStatusFormat:        "Modifikatorkompatibilität: %s",
				CompatibilityStatusActive:        "Aktiv",
				CompatibilityStatusInactive:      "Inaktiv",
				CompatibilityStatusUnknown:       "Unbekannt (kein Ziel)",
				CompatibilityHelpTitle:           "Modifikatorkompatibilität",
				CompatibilityHelpMessage:         "Manche Apps erkennen Alt, Shift oder AltGr nicht richtig. Auto: Wendet eine Korrektur für bekannte Apps wie Citrix Workspace oder HPE iLO an. Immer an: Korrektur immer verwenden. Aus: Korrektur nie verwenden.",
				AbortOnFocusChange:               "Bei Fokuswechsel abbrechen",
				HotkeyInfo:                       "Tastenkombination: Strg+G",
			},
		},
	}
	languageMap = func() map[string]languageDefinition {
		m := make(map[string]languageDefinition, len(languages))
		for _, lang := range languages {
			m[lang.metadata.Code] = lang
		}
		return m
	}()
)

func SupportedLanguages() []LanguageMetadata {
	result := make([]LanguageMetadata, 0, len(languages))
	for _, lang := range languages {
		result = append(result, lang.metadata)
	}
	return result
}

func Labels(code string) LabelSet {
	if lang, ok := languageMap[code]; ok {
		return lang.labels
	}
	return languageMap[defaultCode].labels
}

func DefaultCode() string {
	return defaultCode
}

func DetectSystemLanguage() string {
	locales, err := locale.GetLocales()
	if err == nil {
		for _, loc := range locales {
			if code := normalizeCode(loc); code != "" {
				if _, ok := languageMap[code]; ok {
					return code
				}
			}
		}
	}
	return defaultCode
}

func NormalizeCode(code string) string {
	return normalizeCode(code)
}

func normalizeCode(code string) string {
	code = strings.TrimSpace(strings.ToLower(code))
	if code == "" {
		return ""
	}
	if idx := strings.Index(code, "-"); idx > 0 {
		code = code[:idx]
	}
	if len(code) > 2 {
		code = code[:2]
	}
	return code
}

func ResolveCode(code string) string {
	if normalized := normalizeCode(code); normalized != "" {
		if _, ok := languageMap[normalized]; ok {
			return normalized
		}
	}
	return defaultCode
}

func IsSupported(code string) bool {
	_, ok := languageMap[normalizeCode(code)]
	return ok
}
