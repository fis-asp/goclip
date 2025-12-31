//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreGraphics -framework ApplicationServices -framework Carbon -framework AppKit -framework Foundation
#import <CoreGraphics/CoreGraphics.h>
#import <ApplicationServices/ApplicationServices.h>
#import <Carbon/Carbon.h>
#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>
#import <stdlib.h>
#import <stdint.h>

// Get all visible windows
typedef struct {
    int pid;
    int windowNumber;
    char title[256];
    char appName[256];
} WindowInfo;

static int getVisibleWindows(WindowInfo* windows, int maxWindows) {
    @autoreleasepool {
        int count = 0;

        // Get list of all windows
        CFArrayRef windowList = CGWindowListCopyWindowInfo(
            kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements,
            kCGNullWindowID
        );

        if (!windowList) return 0;

        CFIndex numWindows = CFArrayGetCount(windowList);

        for (CFIndex i = 0; i < numWindows && count < maxWindows; i++) {
            CFDictionaryRef window = CFArrayGetValueAtIndex(windowList, i);

            // Get window layer - skip windows at layer 0 (normal) or higher only
            CFNumberRef layerRef = CFDictionaryGetValue(window, kCGWindowLayer);
            int layer = 0;
            if (layerRef) {
                CFNumberGetValue(layerRef, kCFNumberIntType, &layer);
            }

            // Skip non-normal windows (menu bar, dock, etc.)
            if (layer != 0) continue;

            // Get window title
            CFStringRef titleRef = CFDictionaryGetValue(window, kCGWindowName);
            if (!titleRef) continue;

            // Get window owner name
            CFStringRef ownerRef = CFDictionaryGetValue(window, kCGWindowOwnerName);
            if (!ownerRef) continue;

            // Get PID
            CFNumberRef pidRef = CFDictionaryGetValue(window, kCGWindowOwnerPID);
            int pid = 0;
            if (pidRef) {
                CFNumberGetValue(pidRef, kCFNumberIntType, &pid);
            }

            // Get window number
            CFNumberRef windowNumRef = CFDictionaryGetValue(window, kCGWindowNumber);
            int windowNumber = 0;
            if (windowNumRef) {
                CFNumberGetValue(windowNumRef, kCFNumberIntType, &windowNumber);
            }

            // Convert title to C string
            char title[256] = {0};
            CFStringGetCString(titleRef, title, sizeof(title), kCFStringEncodingUTF8);

            // Skip empty titles
            if (strlen(title) == 0) continue;

            // Convert owner name to C string
            char appName[256] = {0};
            CFStringGetCString(ownerRef, appName, sizeof(appName), kCFStringEncodingUTF8);

            // Store window info
            windows[count].pid = pid;
            windows[count].windowNumber = windowNumber;
            strncpy(windows[count].title, title, sizeof(windows[count].title) - 1);
            strncpy(windows[count].appName, appName, sizeof(windows[count].appName) - 1);
            count++;
        }

        CFRelease(windowList);
        return count;
    }
}

// Activate a window by PID
static bool activateWindowByPID(int pid) {
    @autoreleasepool {
        NSRunningApplication *app = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
        if (app) {
            return [app activateWithOptions:NSApplicationActivateAllWindows];
        }
        return false;
    }
}

// Get current frontmost application PID
static int getFrontmostPID() {
    @autoreleasepool {
        NSRunningApplication *app = [[NSWorkspace sharedWorkspace] frontmostApplication];
        if (app) {
            return [app processIdentifier];
        }
        return 0;
    }
}

// Get application name for PID
static void getAppNameForPID(int pid, char* name, int maxLen) {
    @autoreleasepool {
        NSRunningApplication *app = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
        if (app) {
            const char* appName = [[app localizedName] UTF8String];
            if (appName) {
                strncpy(name, appName, maxLen - 1);
                name[maxLen - 1] = 0;
                return;
            }
        }
        name[0] = 0;
    }
}

// Check if accessibility permissions are granted
static bool checkAccessibilityPermissions() {
    NSDictionary *options = @{(__bridge id)kAXTrustedCheckOptionPrompt: @YES};
    return AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)options);
}

// Raise and focus a specific window belonging to pid, matched by exact title.
static bool raiseWindowByPIDAndTitle(int pid, const char* ctitle) {
	if (!ctitle) return false;
	CFStringRef targetTitle = CFStringCreateWithCString(kCFAllocatorDefault, ctitle, kCFStringEncodingUTF8);
	if (!targetTitle) return false;

	AXUIElementRef app = AXUIElementCreateApplication(pid);
	if (!app) { CFRelease(targetTitle); return false; }

	CFArrayRef windows = NULL;
	AXError err = AXUIElementCopyAttributeValue(app, kAXWindowsAttribute, (CFTypeRef *)&windows);
	if (err != kAXErrorSuccess || !windows) {
		CFRelease(app);
		CFRelease(targetTitle);
		return false;
	}

	bool ok = false;
	CFIndex count = CFArrayGetCount(windows);
	for (CFIndex i = 0; i < count; i++) {
		AXUIElementRef win = (AXUIElementRef)CFArrayGetValueAtIndex(windows, i);
		if (!win) continue;
		CFStringRef wt = NULL;
		if (AXUIElementCopyAttributeValue(win, kAXTitleAttribute, (CFTypeRef *)&wt) == kAXErrorSuccess && wt) {
			if (CFStringCompare(wt, targetTitle, 0) == kCFCompareEqualTo) {
				// Try to focus the window and raise it
				AXUIElementSetAttributeValue(app, kAXFocusedWindowAttribute, win);
				AXUIElementPerformAction(win, kAXRaiseAction);
				ok = true;
				CFRelease(wt);
				break;
			}
			CFRelease(wt);
		}
	}

	CFRelease(windows);
	CFRelease(app);
	CFRelease(targetTitle);
	return ok;
}

// Note: Matching by CGWindowNumber is not portable via AX on all macOS versions.
// We rely on title matching above for specific window activation.

// Map a Unicode character to a keycode + basic modifiers (Shift, Option) for the current keyboard layout.
// outMods bit 0 => Shift, bit 1 => Option
static bool mapRuneToKey(UniChar target, uint16_t *outKeyCode, uint32_t *outMods) {
	// Prefer ASCII-capable source, fallback to current
	TISInputSourceRef source = TISCopyCurrentASCIICapableKeyboardLayoutInputSource();
	if (!source) source = TISCopyCurrentKeyboardLayoutInputSource();
	if (!source) return false;

	CFDataRef layoutData = TISGetInputSourceProperty(source, kTISPropertyUnicodeKeyLayoutData);
	if (!layoutData) {
		CFRelease(source);
		return false;
	}
	const UCKeyboardLayout *layout = (const UCKeyboardLayout *)CFDataGetBytePtr(layoutData);
	if (!layout) {
		CFRelease(source);
		return false;
	}

	// Try keycodes 0..127 and modifier combos: none, Shift, Option, Shift+Option
	for (UInt16 keyCode = 0; keyCode < 128; keyCode++) {
		for (int combo = 0; combo < 4; combo++) {
			UInt32 deadKeyState = 0;
			UniChar chars[8] = {0};
			UniCharCount length = 0;

			UInt32 mods = 0;
			if (combo & 1) mods |= (shiftKey >> 8);
			if (combo & 2) mods |= (optionKey >> 8);

			OSStatus s = UCKeyTranslate(
				layout,
				keyCode,
				kUCKeyActionDisplay,
				mods,
				LMGetKbdType(),
				kUCKeyTranslateNoDeadKeysBit,
				&deadKeyState,
				8,
				&length,
				chars
			);
			if (s == noErr && length > 0 && chars[0] == target) {
				if (outKeyCode) *outKeyCode = (uint16_t)keyCode;
				if (outMods) {
					uint32_t mask = 0;
					if (combo & 1) mask |= 1; // Shift
					if (combo & 2) mask |= 2; // Option
					*outMods = mask;
				}
				CFRelease(source);
				return true;
			}
		}
	}

	CFRelease(source);
	return false;
}

// Global hotkey registration for macOS using Carbon
#define kVK_ANSI_G 5
#define HOTKEY_SUCCESS 1
#define HOTKEY_FAILURE 0
#define HOTKEY_ID 1

static EventHotKeyRef gHotKeyRef = NULL;
static EventHandlerUPP gHotKeyHandler = NULL;

// Hotkey event handler
static OSStatus hotKeyEventHandler(EventHandlerCallRef nextHandler, EventRef event, void *userData) {
	EventHotKeyID hkID;
	OSStatus err = GetEventParameter(event, kEventParamDirectObject, typeEventHotKeyID, NULL, sizeof(EventHotKeyID), NULL, &hkID);
	
	if (err == noErr && hkID.id == HOTKEY_ID) {
		// Signal Go that hotkey was pressed
		extern void hotkeyPressed();
		hotkeyPressed();
	}
	
	return noErr;
}

// Register Cmd+G hotkey
static int registerHotkey() {
	if (gHotKeyRef != NULL) {
		return HOTKEY_FAILURE; // Already registered
	}
	
	EventTypeSpec eventType;
	eventType.eventClass = kEventClassKeyboard;
	eventType.eventKind = kEventHotKeyPressed;
	
	gHotKeyHandler = NewEventHandlerUPP(hotKeyEventHandler);
	InstallEventHandler(GetApplicationEventTarget(), gHotKeyHandler, 1, &eventType, NULL, NULL);
	
	EventHotKeyID hkID;
	hkID.signature = 'gclp';
	hkID.id = HOTKEY_ID;
	
	// Register Cmd+G hotkey
	OSStatus status = RegisterEventHotKey(
		kVK_ANSI_G,           // Virtual key code for 'G'
		cmdKey,               // Cmd modifier
		hkID,
		GetApplicationEventTarget(),
		0,
		&gHotKeyRef
	);
	
	return (status == noErr) ? HOTKEY_SUCCESS : HOTKEY_FAILURE;
}

// Unregister hotkey
static void unregisterHotkey() {
	if (gHotKeyRef != NULL) {
		UnregisterEventHotKey(gHotKeyRef);
		gHotKeyRef = NULL;
	}
	if (gHotKeyHandler != NULL) {
		DisposeEventHandlerUPP(gHotKeyHandler);
		gHotKeyHandler = NULL;
	}
}

*/
import "C"

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	_ "embed"
)

//go:embed assets/logo/app.ico
var embeddedAppIco []byte

type windowInfo struct {
	PID          int
	WindowNumber int
	Title        string
	AppName      string
}

// Layout-aware mapping cache (built on UI thread at startup)
type keyMods struct {
	code   uint16
	shift  bool
	option bool
}

var (
	layoutMap   = map[rune]keyMods{}
	layoutMapMu sync.RWMutex
)

// Global hotkey callback for macOS
var (
	macHotkeyCallback   func()
	macHotkeyCallbackMu sync.Mutex
)

const (
	hotkeyRegistrationSuccess = 1
)

// hotkeyPressed is called from C when the hotkey is pressed
//
//export hotkeyPressed
func hotkeyPressed() {
	macHotkeyCallbackMu.Lock()
	cb := macHotkeyCallback
	macHotkeyCallbackMu.Unlock()

	if cb != nil {
		// Execute callback in main thread via fyne.Do
		fyne.Do(cb)
	}
}

// setMacHotkeyCallback sets the function to be called when the hotkey is pressed
func setMacHotkeyCallback(cb func()) {
	macHotkeyCallbackMu.Lock()
	macHotkeyCallback = cb
	macHotkeyCallbackMu.Unlock()
}

var (
	ignoredAppNamesLower = map[string]struct{}{
		"goclip": {}, // ignore itself
	}

	ignoredTitleSubstringsLower = []string{
		// Add any window titles to ignore on macOS
	}
)

// enumWindows returns visible windows on macOS
func enumWindows(selfAppNameLower string) []windowInfo {
	const maxWindows = 512
	var cWindows [maxWindows]C.WindowInfo

	count := int(C.getVisibleWindows(&cWindows[0], maxWindows))

	var wins []windowInfo
	for i := 0; i < count; i++ {
		w := cWindows[i]

		title := C.GoString(&w.title[0])
		appName := C.GoString(&w.appName[0])

		// Skip our own windows
		if strings.ToLower(appName) == selfAppNameLower {
			continue
		}

		// Skip ignored apps
		if _, ok := ignoredAppNamesLower[strings.ToLower(appName)]; ok {
			continue
		}

		// Skip ignored titles
		titleLower := strings.ToLower(title)
		skip := false
		for _, sub := range ignoredTitleSubstringsLower {
			if strings.Contains(titleLower, sub) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		wins = append(wins, windowInfo{
			PID:          int(w.pid),
			WindowNumber: int(w.windowNumber),
			Title:        strings.TrimSpace(title),
			AppName:      strings.TrimSpace(appName),
		})
	}

	// Sort by title
	sort.Slice(wins, func(i, j int) bool {
		return strings.ToLower(wins[i].Title) < strings.ToLower(wins[j].Title)
	})

	return wins
}

// activateWindow brings a window to the foreground
func activateWindow(pid int) bool {
	result := C.activateWindowByPID(C.int(pid))
	return bool(result)
}

// activateWindowToTitle tries to focus a specific window by title for the given PID.
// Falls back to app-level activation if window focus fails.
func activateWindowToTitle(pid int, title string) bool {
	ctitle := C.CString(title)
	defer C.free(unsafe.Pointer(ctitle))
	if pid != 0 && len(title) > 0 {
		if bool(C.raiseWindowByPIDAndTitle(C.int(pid), ctitle)) {
			return true
		}
	}
	return activateWindow(pid)
}

// sendText types the text using Core Graphics events
func sendText(text string, layout string, perCharDelay time.Duration, shouldStop func() bool) error {
	// Normalize line endings
	text = strings.ReplaceAll(text, "\r\n", "\n")

	for _, r := range text {
		if shouldStop != nil && shouldStop() {
			return nil
		}

		if r == '\n' {
			if err := sendKeyPress(0x24); err != nil { // kVK_Return = 0x24
				return err
			}
			time.Sleep(perCharDelay)
			continue
		}

		// Try layout-aware physical mapping first
		layoutMapMu.RLock()
		if km, ok := layoutMap[r]; ok {
			layoutMapMu.RUnlock()
			if err := sendKeyPressWithMods(km.code, km.shift, km.option); err != nil {
				return err
			}
			time.Sleep(perCharDelay)
			continue
		}
		layoutMapMu.RUnlock()

		// Try US ASCII physical mapping next
		if handled, err := sendASCIICharUS(r); err != nil {
			return err
		} else if handled {
			time.Sleep(perCharDelay)
			continue
		}

		if err := sendChar(r); err != nil {
			return err
		}
		time.Sleep(perCharDelay)
	}

	return nil
}

// sendKeyPress sends a key press and release
func sendKeyPress(keyCode uint16) error {
	// Create key down event
	keyDown := C.CGEventCreateKeyboardEvent(C.CGEventSourceRef(0), C.CGKeyCode(keyCode), C.bool(true))
	if keyDown == 0 {
		return fmt.Errorf("failed to create key down event")
	}
	defer C.CFRelease(C.CFTypeRef(keyDown))

	// Create key up event
	keyUp := C.CGEventCreateKeyboardEvent(C.CGEventSourceRef(0), C.CGKeyCode(keyCode), C.bool(false))
	if keyUp == 0 {
		return fmt.Errorf("failed to create key up event")
	}
	defer C.CFRelease(C.CFTypeRef(keyUp))

	// Post events
	C.CGEventPost(C.kCGHIDEventTap, keyDown)
	C.CGEventPost(C.kCGHIDEventTap, keyUp)

	return nil
}

// sendKeyPressWithMods presses modifiers (Shift/Option) as needed, taps key, then releases modifiers.
func sendKeyPressWithMods(keyCode uint16, needShift bool, needOption bool) error {
	const (
		kVK_Shift  uint16 = 0x38
		kVK_Option uint16 = 0x3A
	)

	// Press modifiers down
	if needOption {
		evt := C.CGEventCreateKeyboardEvent(C.CGEventSourceRef(0), C.CGKeyCode(kVK_Option), C.bool(true))
		if evt == 0 {
			return fmt.Errorf("failed to create option down event")
		}
		C.CGEventPost(C.kCGHIDEventTap, evt)
		C.CFRelease(C.CFTypeRef(evt))
	}
	if needShift {
		evt := C.CGEventCreateKeyboardEvent(C.CGEventSourceRef(0), C.CGKeyCode(kVK_Shift), C.bool(true))
		if evt == 0 {
			// try to release option if pressed
			if needOption {
				up := C.CGEventCreateKeyboardEvent(C.CGEventSourceRef(0), C.CGKeyCode(kVK_Option), C.bool(false))
				if up != 0 {
					C.CGEventPost(C.kCGHIDEventTap, up)
					C.CFRelease(C.CFTypeRef(up))
				}
			}
			return fmt.Errorf("failed to create shift down event")
		}
		C.CGEventPost(C.kCGHIDEventTap, evt)
		C.CFRelease(C.CFTypeRef(evt))
	}

	// Tap the key
	if err := sendKeyPress(keyCode); err != nil {
		// Release modifiers on error
		if needShift {
			up := C.CGEventCreateKeyboardEvent(C.CGEventSourceRef(0), C.CGKeyCode(kVK_Shift), C.bool(false))
			if up != 0 {
				C.CGEventPost(C.kCGHIDEventTap, up)
				C.CFRelease(C.CFTypeRef(up))
			}
		}
		if needOption {
			up := C.CGEventCreateKeyboardEvent(C.CGEventSourceRef(0), C.CGKeyCode(kVK_Option), C.bool(false))
			if up != 0 {
				C.CGEventPost(C.kCGHIDEventTap, up)
				C.CFRelease(C.CFTypeRef(up))
			}
		}
		return err
	}

	// Release modifiers
	if needShift {
		up := C.CGEventCreateKeyboardEvent(C.CGEventSourceRef(0), C.CGKeyCode(kVK_Shift), C.bool(false))
		if up == 0 {
			return fmt.Errorf("failed to create shift up event")
		}
		C.CGEventPost(C.kCGHIDEventTap, up)
		C.CFRelease(C.CFTypeRef(up))
	}
	if needOption {
		up := C.CGEventCreateKeyboardEvent(C.CGEventSourceRef(0), C.CGKeyCode(kVK_Option), C.bool(false))
		if up == 0 {
			return fmt.Errorf("failed to create option up event")
		}
		C.CGEventPost(C.kCGHIDEventTap, up)
		C.CFRelease(C.CFTypeRef(up))
	}
	return nil
}

// sendASCIICharUS tries to send a basic US-ANSI ASCII character using physical keycodes.
// Returns (handled=true) if it sent it using keycodes; otherwise false to fall back.
func sendASCIICharUS(r rune) (bool, error) {
	// Mac virtual keycodes for US ANSI keyboard
	// Letters: a=0, s=1, d=2, f=3, h=4, g=5, z=6, x=7, c=8, v=9, b=11,
	// q=12, w=13, e=14, r=15, y=16, t=17, 1=18, 2=19, 3=20, 4=21, 6=22, 5=23,
	// = 24, 9=25, 7=26, - 27, 8=28, 0=29, ] 30, o=31, u=32, [ 33, i=34, p=35,
	// return=36, l=37, j=38, ' 39, k=40, ; 41, \ 42, , 43, / 44, n=45, m=46,
	// . 47, tab=48, space=49, ` 50, delete=51

	type entry struct {
		code  uint16
		shift bool
	}
	var m map[rune]entry = map[rune]entry{
		// Space and basic control
		' ': {49, false},

		// Digits row (no shift)
		'1': {18, false}, '2': {19, false}, '3': {20, false}, '4': {21, false}, '5': {23, false},
		'6': {22, false}, '7': {26, false}, '8': {28, false}, '9': {25, false}, '0': {29, false},

		// Letters (lowercase)
		'a': {0, false}, 's': {1, false}, 'd': {2, false}, 'f': {3, false}, 'h': {4, false}, 'g': {5, false},
		'z': {6, false}, 'x': {7, false}, 'c': {8, false}, 'v': {9, false}, 'b': {11, false},
		'q': {12, false}, 'w': {13, false}, 'e': {14, false}, 'r': {15, false}, 'y': {16, false}, 't': {17, false},
		'o': {31, false}, 'u': {32, false}, 'i': {34, false}, 'p': {35, false}, 'l': {37, false}, 'j': {38, false},
		'k': {40, false}, 'n': {45, false}, 'm': {46, false},

		// Punctuation (no shift)
		'-': {27, false}, '=': {24, false}, '[': {33, false}, ']': {30, false}, '\\': {42, false},
		';': {41, false}, '\'': {39, false}, ',': {43, false}, '.': {47, false}, '/': {44, false}, '`': {50, false},

		// Shifted symbols
		'!': {18, true}, '@': {19, true}, '#': {20, true}, '$': {21, true}, '%': {23, true}, '^': {22, true},
		'&': {26, true}, '*': {28, true}, '(': {25, true}, ')': {29, true}, '_': {27, true}, '+': {24, true},
		'{': {33, true}, '}': {30, true}, '|': {42, true}, ':': {41, true}, '"': {39, true}, '<': {43, true},
		'>': {47, true}, '?': {44, true}, '~': {50, true},
	}

	// Uppercase letters via shift
	if r >= 'A' && r <= 'Z' {
		lower := rune(r - 'A' + 'a')
		if e, ok := m[lower]; ok {
			return true, sendKeyPressWithMods(e.code, true, false)
		}
		return false, nil
	}

	if e, ok := m[r]; ok {
		return true, sendKeyPressWithMods(e.code, e.shift, false)
	}
	return false, nil
}

// sendChar sends a character using Unicode (reverted to stable path)
func sendChar(r rune) error {
	// Convert rune to UTF-16
	utf16 := []uint16{uint16(r)}
	if r > 0xFFFF {
		// Handle surrogate pairs for characters outside BMP
		r -= 0x10000
		utf16 = []uint16{
			uint16((r >> 10) + 0xD800),
			uint16((r & 0x3FF) + 0xDC00),
		}
	}

	for _, code := range utf16 {
		// Create Unicode keyboard event
		keyDown := C.CGEventCreateKeyboardEvent(C.CGEventSourceRef(0), 0, C.bool(true))
		if keyDown == 0 {
			return fmt.Errorf("failed to create unicode key down event")
		}

		// Set Unicode character
		C.CGEventKeyboardSetUnicodeString(keyDown, 1, (*C.UniChar)(unsafe.Pointer(&code)))

		// Create key up event
		keyUp := C.CGEventCreateKeyboardEvent(C.CGEventSourceRef(0), 0, C.bool(false))
		if keyUp == 0 {
			C.CFRelease(C.CFTypeRef(keyDown))
			return fmt.Errorf("failed to create unicode key up event")
		}

		C.CGEventKeyboardSetUnicodeString(keyUp, 1, (*C.UniChar)(unsafe.Pointer(&code)))

		// Post events
		C.CGEventPost(C.kCGHIDEventTap, keyDown)
		C.CGEventPost(C.kCGHIDEventTap, keyUp)

		C.CFRelease(C.CFTypeRef(keyDown))
		C.CFRelease(C.CFTypeRef(keyUp))
	}

	return nil
}

// truncateRunes limits to n runes, appends "..." if truncated.
func truncateRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return s
	}
	if n <= 3 {
		return string(r[:n])
	}
	return string(r[:n]) + "..."
}

// load ICO from embedded bytes, with a dev-time disk fallback
func loadAppIcon() fyne.Resource {
	if len(embeddedAppIco) > 0 {
		return fyne.NewStaticResource("app.ico", embeddedAppIco)
	}
	// fallback for `go run` from source
	data, err := os.ReadFile("assets/logo/app.ico")
	if err == nil {
		return fyne.NewStaticResource("app.ico", data)
	}
	return nil
}

// getFrontmostApp returns the PID and name of the frontmost application
func getFrontmostApp() (int, string) {
	pid := int(C.getFrontmostPID())
	if pid == 0 {
		return 0, "(none)"
	}

	var nameBuf [256]C.char
	C.getAppNameForPID(C.int(pid), &nameBuf[0], 256)
	name := C.GoString(&nameBuf[0])

	return pid, name
}

func main() {
	myApp := app.New()
	myApp.Settings().SetTheme(theme.DarkTheme())

	// set runtime icon
	if res := loadAppIcon(); res != nil {
		myApp.SetIcon(res)
	}

	// Check for accessibility permissions
	hasPermission := bool(C.checkAccessibilityPermissions())
	if !hasPermission {
		// Create a window to show the permission requirement
		permWindow := myApp.NewWindow("goclip - Accessibility Required")
		permWindow.Resize(fyne.NewSize(500, 250))

		message := widget.NewLabel(
			"goclip needs Accessibility permissions to send keyboard events.\n\n" +
				"Please grant access in:\n" +
				"System Settings → Privacy & Security → Accessibility\n\n" +
				"After granting permission, restart goclip.",
		)
		message.Wrapping = fyne.TextWrapWord

		openSettingsBtn := widget.NewButton("Open System Settings", func() {
			// Use os/exec to open system settings
			cmd := exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility")
			cmd.Run()
		})

		content := container.NewVBox(
			widget.NewLabel("⚠️ Permission Required"),
			widget.NewSeparator(),
			message,
			openSettingsBtn,
		)

		permWindow.SetContent(container.NewPadded(content))
		permWindow.CenterOnScreen()
		permWindow.ShowAndRun()
		return
	}

	// Build a small layout-aware mapping on the UI thread for common characters,
	// including German-specific letters and punctuation. This avoids calling TIS APIs from a goroutine.
	buildLayoutMapping := func() {
		candidates := []rune{}
		// Basic ASCII
		for r := rune(32); r <= rune(126); r++ {
			candidates = append(candidates, r)
		}
		// German specifics
		candidates = append(candidates, []rune{'ä', 'ö', 'ü', 'Ä', 'Ö', 'Ü', 'ß', '€', '§', '°'}...)

		layoutMapMu.Lock()
		defer layoutMapMu.Unlock()
		layoutMap = map[rune]keyMods{}
		for _, r := range candidates {
			var cCode C.uint16_t
			var cMods C.uint32_t
			if C.mapRuneToKey(C.UniChar(r), &cCode, &cMods) {
				km := keyMods{code: uint16(cCode)}
				if (uint32(cMods) & 1) != 0 {
					km.shift = true
				}
				if (uint32(cMods) & 2) != 0 {
					km.option = true
				}
				layoutMap[r] = km
			}
		}
	}
	// Build immediately (runs on UI thread here)
	buildLayoutMapping()

	// our own app name (lower) to avoid listing ourselves
	selfPath, _ := os.Executable()
	selfAppNameLower := strings.ToLower(filepath.Base(selfPath))
	if !strings.Contains(selfAppNameLower, ".") {
		// On macOS during development, the name might just be the binary name
		selfAppNameLower = "goclip"
	}

	w := myApp.NewWindow("goclip")
	w.Resize(fyne.NewSize(800, 460))

	// also set it on the window explicitly
	if res := loadAppIcon(); res != nil {
		w.SetIcon(res)
	}

	// --- Input field with Hide/Show (eye) toggle ---
	inputEntry := widget.NewMultiLineEntry()
	inputEntry.SetPlaceHolder("Type here…")
	inputEntry.Wrapping = fyne.TextWrapWord

	masked := false
	var eyeBtn *widget.Button
	eyeBtn = widget.NewButtonWithIcon("", theme.VisibilityIcon(), func() {
		masked = !masked
		inputEntry.Password = masked
		if masked {
			eyeBtn.SetIcon(theme.VisibilityOffIcon())
		} else {
			eyeBtn.SetIcon(theme.VisibilityIcon())
		}
		inputEntry.Refresh()
	})
	eyeBtn.Importance = widget.LowImportance

	inputRow := container.NewBorder(nil, nil, nil, eyeBtn, inputEntry)

	status := widget.NewLabel("Ready.")
	status.Wrapping = fyne.TextWrapWord

	// Note: Layout selection is simplified on macOS
	// macOS handles keyboard layouts differently
	layoutSelect := widget.NewSelect([]string{
		"Auto (Use System)",
	}, nil)
	layoutSelect.Selected = "Auto (Use System)"
	layoutSelect.Disable() // macOS uses system layout automatically

	// --- Typing speed controls ---
	speedSelect := widget.NewSelect([]string{
		"Default (Auto)",
		"Medium (50 ms)",
		"Slow (100 ms)",
		"Super Slow (250 ms)",
		"Custom",
	}, nil)
	speedSelect.Selected = "Default (Auto)"

	customMsEntry := widget.NewEntry()
	customMsEntry.SetPlaceHolder("ms per char")
	customMsEntry.Hide()

	// Dynamic per-character delay selection
	getPerCharDelay := func(text string) time.Duration {
		switch speedSelect.Selected {
		case "Default (Auto)":
			runeCount := 0
			lines := 1
			for _, ch := range text {
				runeCount++
				if ch == '\n' {
					lines++
				}
			}

			if runeCount <= 200 && lines <= 5 {
				return 0
			}

			msByLines := lines
			msByChars := runeCount / 200

			ms := msByLines
			if msByChars > ms {
				ms = msByChars
			}

			if ms < 10 {
				ms = 10
			}
			if ms > 50 {
				ms = 50
			}

			return time.Duration(ms) * time.Millisecond

		case "Medium (50 ms)":
			return 50 * time.Millisecond
		case "Slow (100 ms)":
			return 100 * time.Millisecond
		case "Super Slow (250 ms)":
			return 250 * time.Millisecond
		case "Custom":
			v := strings.TrimSpace(customMsEntry.Text)
			if v == "" {
				return 0
			}
			var acc int64
			for _, ch := range v {
				if ch < '0' || ch > '9' {
					return 0
				}
				acc = acc*10 + int64(ch-'0')
				if acc > 10000 {
					acc = 10000
					break
				}
			}
			return time.Duration(acc) * time.Millisecond
		default:
			return 0
		}
	}

	delayLabel := widget.NewLabel("Per-character delay: 0 ms")

	updateDelayLabel := func() {
		if speedSelect.Selected != "Default (Auto)" {
			delayLabel.Hide()
			return
		}
		delayLabel.Show()
		d := getPerCharDelay(inputEntry.Text)
		ms := d.Milliseconds()
		delayLabel.SetText(fmt.Sprintf("Per-character delay: %d ms", ms))
	}

	speedSelect.OnChanged = func(s string) {
		if s == "Custom" {
			customMsEntry.Show()
		} else {
			customMsEntry.Hide()
		}
		updateDelayLabel()
	}

	customMsEntry.OnChanged = func(s string) {
		updateDelayLabel()
	}

	inputEntry.OnChanged = func(s string) {
		updateDelayLabel()
	}

	winOptions := []string{}
	winMap := map[string]windowInfo{}

	var laMu sync.RWMutex
	lastActivePID := 0
	lastActiveTitle := "(none)"
	lastActiveText := binding.NewString()
	_ = lastActiveText.Set("Last active: (none)")
	lastActiveLabel := widget.NewLabelWithData(lastActiveText)

	windowSelect := widget.NewSelect(winOptions, nil)
	windowSelect.PlaceHolder = "None (use last active)"

	// TODO(macOS): Improve window target selector
	// - The dropdown should list real, stable window targets and reliably focus them when selected.
	// - Current state: works via exact AX title match (fallback to app activation). Good enough for now.
	// - Next steps:
	//   1) Add partial-title matching fallback if exact match fails (e.g., case-insensitive contains).
	//   2) Explore more stable identifiers than titles (AX attributes vary; kAXWindowNumberAttribute is
	//      not guaranteed on all systems). Consider correlating CGWindowList entries with AX windows.
	//   3) Auto-refresh the window list on focus changes to keep the dropdown current.
	//   4) Optionally display PID/window id and app name in the label for easier disambiguation.
	//   5) Consider a user setting to prefer app-wide activation if specific window focusing fails.
	// - Keep Accessibility permission checks in place; AX APIs require it.

	clearBtn := widget.NewButton("Clear", func() {
		windowSelect.Selected = ""
		windowSelect.Refresh()
		status.SetText("Selection cleared → using last active window.")
	})

	refreshWindows := func() {
		wins := enumWindows(selfAppNameLower)
		winOptions = []string{}
		winMap = map[string]windowInfo{}
		for _, wi := range wins {
			short := truncateRunes(wi.Title, 30)
			label := fmt.Sprintf("%s - %s (PID: %d)", short, wi.AppName, wi.PID)
			winOptions = append(winOptions, label)
			winMap[label] = wi
		}
		windowSelect.Options = winOptions
		windowSelect.Refresh()
		status.SetText(fmt.Sprintf("Found %d windows.", len(wins)))
	}

	refreshBtn := widget.NewButton("Refresh windows", refreshWindows)

	// Start polling for frontmost app changes (simpler than event hooks on macOS)
	stopPolling := make(chan bool)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-stopPolling:
				return
			case <-ticker.C:
				pid, name := getFrontmostApp()
				if pid > 0 && strings.ToLower(name) != selfAppNameLower {
					laMu.Lock()
					if pid != lastActivePID {
						lastActivePID = pid
						lastActiveTitle = truncateRunes(name, 30)
						_ = lastActiveText.Set("Last active: " + lastActiveTitle)
					}
					laMu.Unlock()
				}
			}
		}
	}()

	// --- Typing state / stop handling ---
	var typingMu sync.Mutex
	typingStopRequested := false
	isCurrentlyTyping := false

	setStopRequested := func(v bool) {
		typingMu.Lock()
		typingStopRequested = v
		typingMu.Unlock()
	}

	shouldStop := func() bool {
		typingMu.Lock()
		v := typingStopRequested
		typingMu.Unlock()
		return v
	}

	setTypingState := func(typing bool) {
		typingMu.Lock()
		isCurrentlyTyping = typing
		typingMu.Unlock()
	}

	getTypingState := func() bool {
		typingMu.Lock()
		v := isCurrentlyTyping
		typingMu.Unlock()
		return v
	}

	var typeBtn *widget.Button
	var typeClipboardBtn *widget.Button
	var stopBtn *widget.Button
	var actionContainer *fyne.Container

	setTypingUI := func(active bool) {
		if actionContainer == nil {
			return
		}
		if active {
			if stopBtn != nil {
				actionContainer.Objects = []fyne.CanvasObject{stopBtn}
				actionContainer.Refresh()
			}
		} else {
			if typeBtn != nil && typeClipboardBtn != nil {
				actionContainer.Objects = []fyne.CanvasObject{typeBtn, typeClipboardBtn}
				actionContainer.Refresh()
			}
		}
	}

	// Stop button
	stopBtn = widget.NewButton("Stop", func() {
		setStopRequested(true)
		status.SetText("Stopping typing...")
	})
	stopBtn.Importance = widget.DangerImportance

	// --- Type Button ---
	typeBtn = widget.NewButton("Type", func() {
		selected := windowSelect.Selected

		laMu.RLock()
		curPID := lastActivePID
		curTitle := lastActiveTitle
		laMu.RUnlock()

		var targetPID int
		var targetTitle string
		if selected == "" {
			targetPID = curPID
			targetTitle = curTitle
		} else {
			wi, ok := winMap[selected]
			if !ok || wi.PID == 0 {
				status.SetText("Selected window is no longer available.")
				return
			}
			targetPID = wi.PID
			targetTitle = wi.Title
			_ = wi.WindowNumber // reserved for future use
		}

		if targetPID == 0 {
			status.SetText("No window focused yet. Click a window then come back.")
			return
		}

		// Activate selected window by title or fall back to app/last active
		if selected != "" {
			if !activateWindowToTitle(targetPID, targetTitle) {
				status.SetText("Failed to activate target window.")
				return
			}
		} else if !activateWindow(targetPID) {
			status.SetText("Failed to activate target window.")
			return
		}
		time.Sleep(150 * time.Millisecond)

		txt := inputEntry.Text
		if txt == "" {
			status.SetText("Nothing to type.")
			return
		}

		perChar := getPerCharDelay(txt)
		setStopRequested(false)
		setTypingState(true)
		setTypingUI(true)
		status.SetText("Typing...")

		go func(targetPID int, targetTitle string, txt string, perChar time.Duration) {
			err := sendText(txt, layoutSelect.Selected, perChar, shouldStop)
			canceled := shouldStop()

			fyne.Do(func() {
				if canceled {
					status.SetText("Typing stopped by user.")
				} else if err != nil {
					status.SetText("Error typing: " + err.Error())
				} else {
					status.SetText("Typed to: " + targetTitle)
				}
				setTypingState(false)
				setTypingUI(false)
				setStopRequested(false)
			})
		}(targetPID, targetTitle, txt, perChar)
	})

	// --- Type Clipboard Button ---
	typeClipboardBtn = widget.NewButton("Type Clipboard", func() {
		selected := windowSelect.Selected

		laMu.RLock()
		curPID := lastActivePID
		curTitle := lastActiveTitle
		laMu.RUnlock()

		var targetPID int
		var targetTitle string
		if selected == "" {
			targetPID = curPID
			targetTitle = curTitle
		} else {
			wi, ok := winMap[selected]
			if !ok || wi.PID == 0 {
				status.SetText("Selected window is no longer available.")
				return
			}
			targetPID = wi.PID
			targetTitle = wi.Title
			_ = wi.WindowNumber // reserved for future use
		}

		if targetPID == 0 {
			status.SetText("No window focused yet. Click a window then come back.")
			return
		}

		if selected != "" {
			if !activateWindowToTitle(targetPID, targetTitle) {
				status.SetText("Failed to activate target window.")
				return
			}
		} else if !activateWindow(targetPID) {
			status.SetText("Failed to activate target window.")
			return
		}
		time.Sleep(150 * time.Millisecond)

		txt := w.Clipboard().Content()
		if txt == "" {
			status.SetText("Clipboard is empty.")
			return
		}

		perChar := getPerCharDelay(txt)
		setStopRequested(false)
		setTypingState(true)
		setTypingUI(true)
		status.SetText("Typing clipboard...")

		go func(targetPID int, targetTitle string, txt string, perChar time.Duration) {
			err := sendText(txt, layoutSelect.Selected, perChar, shouldStop)
			canceled := shouldStop()

			fyne.Do(func() {
				if canceled {
					status.SetText("Typing stopped by user.")
				} else if err != nil {
					status.SetText("Error typing clipboard: " + err.Error())
				} else {
					status.SetText("Typed clipboard to: " + targetTitle)
				}
				setTypingState(false)
				setTypingUI(false)
				setStopRequested(false)
			})
		}(targetPID, targetTitle, txt, perChar)
	})

	// Action container
	actionContainer = container.NewHBox(typeBtn, typeClipboardBtn)

	// Left side: window selector + buttons
	left := container.NewVBox(
		widget.NewLabelWithStyle("Target Window", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewHBox(windowSelect, clearBtn),
		refreshBtn,
		lastActiveLabel,
	)

	// Right side: layout selector + typing speed controls
	right := container.NewVBox(
		widget.NewLabelWithStyle("Keyboard Layout", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		layoutSelect,
		widget.NewLabel("(macOS uses system layout)"),
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Typing Speed", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		speedSelect,
		customMsEntry,
	)

	header := container.NewBorder(nil, nil, left, right, nil)

	hotkeyInfoLabel := widget.NewLabel("Hotkey: Cmd+G")
	hotkeyInfoLabel.TextStyle = fyne.TextStyle{Italic: true}

	body := container.NewVBox(
		widget.NewLabelWithStyle("Text to type", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		inputRow,
		delayLabel,
		actionContainer,
		status,
		hotkeyInfoLabel,
	)

	content := container.NewBorder(header, nil, nil, nil, body)
	w.SetContent(content)

	updateDelayLabel()
	refreshWindows()

	// Register global hotkey (Cmd+G) for "Type Clipboard"
	if int(C.registerHotkey()) == hotkeyRegistrationSuccess {
		// Set up hotkey callback to trigger typeClipboardBtn
		setMacHotkeyCallback(func() {
			if typeClipboardBtn != nil {
				// Only trigger if not already typing
				if !getTypingState() {
					// Simulate clicking the Type Clipboard button
					typeClipboardBtn.OnTapped()
				}
			}
		})
	}
	
	// Set up cleanup handler for window close
	w.SetCloseIntercept(func() {
		// Cleanup hotkey registration
		C.unregisterHotkey()
		// Stop the polling goroutine
		close(stopPolling)
		// Close the window
		w.Close()
	})

	w.ShowAndRun()
}
