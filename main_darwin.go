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

// Get all visible windows
typedef struct {
    int pid;
    int windowNumber;
    char title[256];
    char appName[256];
} WindowInfo;

int getVisibleWindows(WindowInfo* windows, int maxWindows) {
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
bool activateWindowByPID(int pid) {
    @autoreleasepool {
        NSRunningApplication *app = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
        if (app) {
            return [app activateWithOptions:NSApplicationActivateIgnoringOtherApps];
        }
        return false;
    }
}

// Get current frontmost application PID
int getFrontmostPID() {
    @autoreleasepool {
        NSRunningApplication *app = [[NSWorkspace sharedWorkspace] frontmostApplication];
        if (app) {
            return [app processIdentifier];
        }
        return 0;
    }
}

// Get application name for PID
void getAppNameForPID(int pid, char* name, int maxLen) {
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
*/
import "C"

import (
	"fmt"
	"os"
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
	keyDown := C.CGEventCreateKeyboardEvent(nil, C.CGKeyCode(keyCode), C.bool(true))
	if keyDown == nil {
		return fmt.Errorf("failed to create key down event")
	}
	defer C.CFRelease(C.CFTypeRef(keyDown))

	// Create key up event
	keyUp := C.CGEventCreateKeyboardEvent(nil, C.CGKeyCode(keyCode), C.bool(false))
	if keyUp == nil {
		return fmt.Errorf("failed to create key up event")
	}
	defer C.CFRelease(C.CFTypeRef(keyUp))

	// Post events
	C.CGEventPost(C.kCGHIDEventTap, keyDown)
	C.CGEventPost(C.kCGHIDEventTap, keyUp)

	return nil
}

// sendChar sends a character using Unicode
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
		keyDown := C.CGEventCreateKeyboardEvent(nil, 0, C.bool(true))
		if keyDown == nil {
			return fmt.Errorf("failed to create unicode key down event")
		}

		// Set Unicode character
		C.CGEventKeyboardSetUnicodeString(keyDown, 1, (*C.UniChar)(unsafe.Pointer(&code)))

		// Create key up event
		keyUp := C.CGEventCreateKeyboardEvent(nil, 0, C.bool(false))
		if keyUp == nil {
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
		}

		if targetPID == 0 {
			status.SetText("No window focused yet. Click a window then come back.")
			return
		}

		// Activate the target window
		if !activateWindow(targetPID) {
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
		}

		if targetPID == 0 {
			status.SetText("No window focused yet. Click a window then come back.")
			return
		}

		// Activate the target window
		if !activateWindow(targetPID) {
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

	body := container.NewVBox(
		widget.NewLabelWithStyle("Text to type", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		inputRow,
		delayLabel,
		actionContainer,
		status,
	)

	content := container.NewBorder(header, nil, nil, nil, body)
	w.SetContent(content)

	updateDelayLabel()
	refreshWindows()

	w.ShowAndRun()

	// Cleanup
	close(stopPolling)
}
