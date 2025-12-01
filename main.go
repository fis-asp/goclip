//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unsafe"

	// #include <windows.h>
	"C"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"golang.org/x/sys/windows"

	_ "embed"
)

//go:embed assets/logo/app.ico
var embeddedAppIco []byte

// Version is set at build time via ldflags
var Version = "dev"

type windowInfo struct {
	Hwnd  windows.Handle
	Title string
}

// Pool of UTF-16 buffers for GetWindowText
var windowTextBufPool = sync.Pool{
	New: func() any {
		// Most window titles are well under 256 runes, so 512 UTF-16 chars suffices
		buf := make([]uint16, 512)
		return &buf
	},
}

// Pool of UTF-16 buffers for QueryFullProcessImageNameW
var exePathBufPool = sync.Pool{
	New: func() any {
		buf := make([]uint16, 1024) // ~2KB default, enough for most paths
		return &buf
	},
}

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procEnumWindows              = user32.NewProc("EnumWindows")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procSendInput                = user32.NewProc("SendInput")
	procVkKeyScanExW             = user32.NewProc("VkKeyScanExW")
	procMapVirtualKeyExW         = user32.NewProc("MapVirtualKeyExW")
	procLoadKeyboardLayoutW      = user32.NewProc("LoadKeyboardLayoutW")
	procGetKeyboardLayout        = user32.NewProc("GetKeyboardLayout")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")

	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
)

const (
	inputKeyboard     = 1
	keyeventfExtended = 0x0001
	keyeventfKeyUp    = 0x0002
	keyeventfUnicode  = 0x0004
	keyeventfScancode = 0x0008

	vkShift    = 0x10
	vkControl  = 0x11
	vkMenu     = 0x12
	vkRControl = 0xA3
	vkRMenu    = 0xA5
	vkReturn   = 0x0D

	mapvkVKToVSC = 0

	processQueryLimitedInformation = 0x1000
)

// ---------- Ignore lists (lowercased) ----------
var ignoredProcessNamesLower = map[string]struct{}{
	"goclip.exe": {}, // ignore itself
	// add more exe names here if needed, e.g. some.exe
}

var ignoredTitleSubstringsLower = []string{
	"task switch",     // covers “Task Switch”, “Task Switching”
	"program manager", // desktop shell surface
	// add more substrings if needed
}

// ------------------------------------------------

type keyboardInput struct {
	WVK         uint16
	WScan       uint16
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

type input struct {
	Type  uint32
	_pad1 uint32
	Ki    keyboardInput
	_pad2 uint64
}

// ------------------------- ForegroundWatcher.go -------------------------
//
// Foreground window watcher using Windows SetWinEventHook API.
// Replaces polling loop with an event-driven system.
//
// Monitors EVENT_SYSTEM_FOREGROUND and calls the user-provided callback
// whenever the active/focused window changes.
//

var (
	procSetWinEventHook = user32.NewProc("SetWinEventHook")
	procUnhookWinEvent  = user32.NewProc("UnhookWinEvent")

	// handle to the installed hook, needed for cleanup
	foregroundEventHook windows.Handle

	// prevent GC of the callback by holding reference globally
	foregroundCallbackRef uintptr
)

const (
	eventSystemForeground = 0x0003
	winEventOutOfContext  = 0x0000
)

// startForegroundWatcher sets up a WinEventHook for EVENT_SYSTEM_FOREGROUND.
// It accepts the executable name of this process (lower-cased, to skip self),
// and a callback function to notify when the foreground window changes.
func startForegroundWatcher(
	selfExeLower string,
	onChange func(hwnd windows.Handle, title string),
) error {
	// Wrap the callback in a syscall callback
	cb := windows.NewCallback(func(
		hWinEventHook uintptr,
		event uint32,
		hwnd uintptr,
		idObject, idChild, idThread, dwmsEventTime uintptr,
	) uintptr {
		if hwnd == 0 {
			return 0
		}

		h := windows.Handle(hwnd)
		title := strings.TrimSpace(getWindowText(h))

		// Call client callback only if meaningful
		if title != "" && !shouldIgnoreWindow(h, title, selfExeLower) {
			onChange(h, title)
		}
		return 0
	})

	// GC safekeeping
	foregroundCallbackRef = cb

	// Install the Windows hook
	r, _, err := procSetWinEventHook.Call(
		uintptr(eventSystemForeground), // eventMin
		uintptr(eventSystemForeground), // eventMax
		0,                              // hModule (not using DLL injection)
		cb,                             // callback
		0,                              // processId
		0,                              // threadId
		uintptr(winEventOutOfContext),  // flags -> don't inject into processes
	)
	if r == 0 {
		return fmt.Errorf("SetWinEventHook failed: %v", err)
	}
	foregroundEventHook = windows.Handle(r)
	return nil
}

// stopForegroundWatcher removes the foreground watcher hook.
// Should be called at program exit.
func stopForegroundWatcher() {
	if foregroundEventHook != 0 {
		procUnhookWinEvent.Call(uintptr(foregroundEventHook))
		foregroundEventHook = 0
	}
	foregroundCallbackRef = 0
}

func isWindowVisible(hwnd windows.Handle) bool {
	r, _, _ := procIsWindowVisible.Call(uintptr(hwnd))
	return r != 0
}

func getWindowText(hwnd windows.Handle) string {
	l, _, _ := procGetWindowTextLengthW.Call(uintptr(hwnd))
	length := int(l)
	if length == 0 {
		return ""
	}

	// get buffer from pool
	p := windowTextBufPool.Get().(*[]uint16)
	buf := *p

	// if too small, grow (don’t return shrunk buffer to pool)
	if cap(buf) < length+1 {
		buf = make([]uint16, length+1)
	} else {
		buf = buf[:length+1]
	}

	// call GetWindowTextW
	procGetWindowTextW.Call(
		uintptr(hwnd),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(length+1),
	)

	// convert to string
	text := windows.UTF16ToString(buf[:length])

	// put buffer back if it's a reasonable size
	if cap(buf) <= 4096 {
		windowTextBufPool.Put(&buf)
	}

	return text
}

func getWindowProcessExeBase(hwnd windows.Handle) string {
	// Get PID for window
	var pid uint32
	procGetWindowThreadProcessId.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}

	// Open process with minimal rights
	h, err := windows.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	// Get buffer from pool
	p := exePathBufPool.Get().(*[]uint16)
	buf := *p
	size := uint32(len(buf))

	// Query the full process path
	r1, _, _ := procQueryFullProcessImageNameW.Call(
		uintptr(h),
		uintptr(0),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)

	var exe string
	if r1 != 0 && size > 0 {
		exe = strings.ToLower(filepath.Base(windows.UTF16ToString(buf[:size])))
	}

	// Put back if not grown too large
	if cap(buf) <= 8192 { // e.g. ~16KB characters ~32KB memory
		exePathBufPool.Put(&buf)
	}

	return exe
}

func shouldIgnoreWindow(hwnd windows.Handle, title string, selfExeLower string) bool {
	t := strings.ToLower(strings.TrimSpace(title))
	if t == "" {
		return true
	}
	for _, sub := range ignoredTitleSubstringsLower {
		if strings.Contains(t, sub) {
			return true
		}
	}
	exe := getWindowProcessExeBase(hwnd)
	if exe != "" {
		if exe == selfExeLower {
			return true
		}
		if _, ok := ignoredProcessNamesLower[exe]; ok {
			return true
		}
	}
	return false
}

func enumWindows(selfExeLower string) []windowInfo {
	var wins []windowInfo
	cb := windows.NewCallback(func(h uintptr, _ uintptr) uintptr {
		hwnd := windows.Handle(h)
		if !isWindowVisible(hwnd) {
			return 1
		}
		title := strings.TrimSpace(getWindowText(hwnd))
		if shouldIgnoreWindow(hwnd, title, selfExeLower) {
			return 1
		}
		wins = append(wins, windowInfo{Hwnd: hwnd, Title: title})
		return 1
	})
	procEnumWindows.Call(cb, 0)
	sort.Slice(wins, func(i, j int) bool {
		return strings.ToLower(wins[i].Title) < strings.ToLower(wins[j].Title)
	})
	return wins
}

func setForegroundWindow(hwnd windows.Handle) bool {
	r, _, _ := procSetForegroundWindow.Call(uintptr(hwnd))
	return r != 0
}

func sendInputCall(ins []input) (uint32, error) {
	if len(ins) == 0 {
		return 0, nil
	}
	ret, _, err := procSendInput.Call(
		uintptr(len(ins)),
		uintptr(unsafe.Pointer(&ins[0])),
		unsafe.Sizeof(input{}),
	)
	if ret == 0 {
		return 0, err
	}
	return uint32(ret), nil
}

func sendUnicodeUnit(u uint16) error {
	inDown := input{
		Type: inputKeyboard,
		Ki: keyboardInput{
			WScan:   u,
			DwFlags: keyeventfUnicode,
		},
	}
	inUp := input{
		Type: inputKeyboard,
		Ki: keyboardInput{
			WScan:   u,
			DwFlags: keyeventfUnicode | keyeventfKeyUp,
		},
	}
	_, err := sendInputCall([]input{inDown, inUp})
	return err
}

func pressVK(vk uint16, down bool) error {
	flags := uint32(0)
	if !down {
		flags = keyeventfKeyUp
	}
	in := input{
		Type: inputKeyboard,
		Ki: keyboardInput{
			WVK:     vk,
			DwFlags: flags,
		},
	}
	_, err := sendInputCall([]input{in})
	return err
}

func pressVKExtended(vk uint16, down bool) error {
	flags := uint32(keyeventfExtended)
	if !down {
		flags |= keyeventfKeyUp
	}
	in := input{
		Type: inputKeyboard,
		Ki: keyboardInput{
			WVK:     vk,
			DwFlags: flags,
		},
	}
	_, err := sendInputCall([]input{in})
	return err
}

func sendScan(sc uint16, extended bool, down bool) error {
	flags := uint32(keyeventfScancode)
	if !down {
		flags |= keyeventfKeyUp
	}
	if extended {
		flags |= keyeventfExtended
	}
	in := input{
		Type: inputKeyboard,
		Ki: keyboardInput{
			WScan:   sc,
			DwFlags: flags,
		},
	}
	_, err := sendInputCall([]input{in})
	return err
}

func tapScan(sc uint16, extended bool) error {
	if err := sendScan(sc, extended, true); err != nil {
		return err
	}
	if err := sendScan(sc, extended, false); err != nil {
		return err
	}
	return nil
}

func mapVirtualKeyEx(vk uint16, hkl windows.Handle) uint16 {
	r, _, _ := procMapVirtualKeyExW.Call(uintptr(vk), uintptr(mapvkVKToVSC), uintptr(hkl))
	return uint16(r & 0xFFFF)
}

func loadHKLByName(name string) windows.Handle {
	if name == "Auto (Use System)" || name == "" {
		h, _, _ := procGetKeyboardLayout.Call(0)
		return windows.Handle(h)
	}

	klid := ""
	switch name {
	case "English (US)":
		klid = "00000409"
	case "US International":
		klid = "00020409"
	case "English (UK)":
		klid = "00000809"
	case "German (DE)":
		klid = "00000407"
	case "French (FR)":
		klid = "0000040C"
	case "Spanish (ES)":
		klid = "0000040A"
	case "Italian (IT)":
		klid = "00000410"
	case "Dutch (NL)":
		klid = "00000413"
	case "Portuguese (BR - ABNT2)":
		klid = "00010416"
	case "Portuguese (PT)":
		klid = "00000816"
	case "Danish (DA)":
		klid = "00000406"
	case "Swedish (SV)":
		klid = "0000041D"
	case "Finnish (FI)":
		klid = "0000040B"
	case "Norwegian (NO)":
		klid = "00000414"
	case "Swiss German (DE-CH)":
		klid = "00000807"
	case "Swiss French (FR-CH)":
		klid = "0000100C"
	case "Polish (Programmers)":
		klid = "00000415"
	case "Czech (CS)":
		klid = "00000405"
	case "Slovak (SK)":
		klid = "0000041B"
	case "Hungarian (HU)":
		klid = "0000040E"
	case "Turkish (Q)":
		klid = "0000041F"
	case "Russian (RU)":
		klid = "00000419"
	case "Ukrainian (UK)":
		klid = "00000422"
	case "Hebrew (HE)":
		klid = "0000040D"
	case "Arabic (AR)":
		klid = "00000401"
	case "Japanese (JP)":
		klid = "00000411"
	case "Korean (KO)":
		klid = "00000412"
	default:
		h, _, _ := procGetKeyboardLayout.Call(0)
		return windows.Handle(h)
	}

	ptr, _ := windows.UTF16PtrFromString(klid)
	h, _, _ := procLoadKeyboardLayoutW.Call(uintptr(unsafe.Pointer(ptr)), uintptr(0))
	return windows.Handle(h)
}

func vkKeyScanEx(r rune, hkl windows.Handle) (vk uint16, shift byte, ok bool) {
	if r > 0xFFFF {
		return 0, 0, false
	}
	ch := uint16(r)
	res, _, _ := procVkKeyScanExW.Call(uintptr(ch), uintptr(hkl))
	v := int16(res)
	if v == -1 {
		return 0, 0, false
	}
	vk = uint16(byte(v & 0xFF))
	shift = byte((v >> 8) & 0xFF)
	return vk, shift, true
}

func sendEnter(hkl windows.Handle) error {
	sc := mapVirtualKeyEx(vkReturn, hkl)
	if sc == 0 {
		return tapScan(28, false)
	}
	return tapScan(sc, false)
}

func sendCharPhysicalFallback(r rune, perCharDelay time.Duration) error {
	s := string(r)
	utf16, err := windows.UTF16FromString(s)
	if err != nil {
		return err
	}
	for _, u := range utf16 {
		if u == 0 {
			continue
		}
		if err := sendUnicodeUnit(u); err != nil {
			return err
		}
		time.Sleep(perCharDelay)
	}
	return nil
}

func releaseModifiers(shift byte) {
	// Check if AltGr (Ctrl+Alt = 0x06)
	if (shift & 0x06) == 0x06 {
		// Release Right Alt (AltGr) - scan code 0x38 with extended flag
		_ = sendScan(0x38, true, false)
	} else {
		// Release individual modifiers
		if (shift & 0x04) != 0 {
			_ = pressVK(vkMenu, false)
		}
		if (shift & 0x02) != 0 {
			_ = pressVK(vkControl, false)
		}
	}
	if (shift & 0x01) != 0 {
		_ = pressVK(vkShift, false)
	}
}

func isExtendedVK(vk uint16) bool {
	switch vk {
	case 0x25, 0x26, 0x27, 0x28:
		return true
	case 0x21, 0x22, 0x23, 0x24:
		return true
	case 0x2D, 0x2E:
		return true
	default:
		return false
	}
}

func sendCharPhysical(r rune, hkl windows.Handle, perCharDelay time.Duration) error {
	vk, shift, ok := vkKeyScanEx(r, hkl)
	if !ok {
		return sendCharPhysicalFallback(r, perCharDelay)
	}
	sc := mapVirtualKeyEx(vk, hkl)
	if sc == 0 {
		return sendCharPhysicalFallback(r, perCharDelay)
	}
	if (shift & 0x01) != 0 {
		if err := pressVK(vkShift, true); err != nil {
			return err
		}
	}
	// Check if AltGr is needed (Ctrl+Alt = 0x06)
	if (shift & 0x06) == 0x06 {
		// Use Right Alt (AltGr) - scan code 0x38 with extended flag for better web console compatibility
		if err := sendScan(0x38, true, true); err != nil {
			releaseModifiers(shift)
			return err
		}
	} else {
		// Press Ctrl and/or Alt individually if needed
		if (shift & 0x02) != 0 {
			if err := pressVK(vkControl, true); err != nil {
				return err
			}
		}
		if (shift & 0x04) != 0 {
			if err := pressVK(vkMenu, true); err != nil {
				return err
			}
		}
	}
	if err := tapScan(sc, isExtendedVK(vk)); err != nil {
		releaseModifiers(shift)
		return err
	}
	releaseModifiers(shift)
	time.Sleep(perCharDelay)
	return nil
}

func sendText(text string, layout string, perCharDelay time.Duration, shouldStop func() bool) error {
	hkl := loadHKLByName(layout)
	text = strings.ReplaceAll(text, "\r\n", "\n")

	for _, r := range text {
		if shouldStop != nil && shouldStop() {
			// cancelled by user
			return nil
		}

		if r == '\n' {
			if err := sendEnter(hkl); err != nil {
				return err
			}
			time.Sleep(perCharDelay)
			continue
		}

		if err := sendCharPhysical(r, hkl, perCharDelay); err != nil {
			return err
		}
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

func main() {
	myApp := app.New()
	myApp.Settings().SetTheme(theme.DarkTheme())

	// set runtime icon (taskbar/window) from embedded resource
	if res := loadAppIcon(); res != nil {
		myApp.SetIcon(res)
	}

	// our own exe base name (lower) to avoid listing ourselves
	selfPath, _ := os.Executable()
	selfExeLower := strings.ToLower(filepath.Base(selfPath))

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

	layoutSelect := widget.NewSelect([]string{
		"Auto (Use System)",
		"English (US)",
		"US International",
		"English (UK)",
		"German (DE)",
		"French (FR)",
		"Spanish (ES)",
		"Italian (IT)",
		"Dutch (NL)",
		"Portuguese (BR - ABNT2)",
		"Portuguese (PT)",
		"Danish (DA)",
		"Swedish (SV)",
		"Finnish (FI)",
		"Norwegian (NO)",
		"Swiss German (DE-CH)",
		"Swiss French (FR-CH)",
		"Polish (Programmers)",
		"Czech (CS)",
		"Slovak (SK)",
		"Hungarian (HU)",
		"Turkish (Q)",
		"Russian (RU)",
		"Ukrainian (UK)",
		"Hebrew (HE)",
		"Arabic (AR)",
		"Japanese (JP)",
		"Korean (KO)",
	}, nil)
	layoutSelect.Selected = "Auto (Use System)"

	// --- Typing speed controls (dropdown + optional custom ms field) ---
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
	customMsEntry.Hide() // start hidden unless Custom is selected

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

			// For very short snippets, no delay
			if runeCount <= 200 && lines <= 5 {
				return 0
			}

			// Base delay from line count (more lines -> more delay)
			msByLines := lines

			// Additional delay from character count (large blocks with few newlines)
			msByChars := runeCount / 200 // 200 chars per 1 ms

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

	// Display for current delay (only shown for Default (Auto))
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
	winMap := map[string]windows.Handle{}

	var laMu sync.RWMutex
	lastActiveHandle := windows.Handle(0)
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
		wins := enumWindows(selfExeLower)
		winOptions = []string{}
		winMap = map[string]windows.Handle{}
		for _, wi := range wins {
			short := truncateRunes(wi.Title, 30) // limit to 30 chars in list
			label := fmt.Sprintf("%s (0x%X)", short, uintptr(wi.Hwnd))
			winOptions = append(winOptions, label)
			winMap[label] = wi.Hwnd
		}
		windowSelect.Options = winOptions
		windowSelect.Refresh()
		status.SetText(fmt.Sprintf("Found %d windows.", len(wins)))
	}

	refreshBtn := widget.NewButton("Refresh windows", refreshWindows)

	// Start event-driven watcher of foreground windows
	err := startForegroundWatcher(selfExeLower, func(hwnd windows.Handle, title string) {
		t := truncateRunes(title, 30)

		laMu.Lock()
		lastActiveHandle = hwnd
		lastActiveTitle = t
		laMu.Unlock()

		_ = lastActiveText.Set("Last active: " + t)
	})
	if err != nil {
		status.SetText("Warning: foreground watcher failed, falling back: " + err.Error())
	}

	// Ensure cleanup when main exits
	defer stopForegroundWatcher()

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

	// Stop button (shown while typing)
	stopBtn = widget.NewButton("Stop", func() {
		setStopRequested(true)
		status.SetText("Stopping typing...")
	})
	stopBtn.Importance = widget.DangerImportance

	// --- Type Button ---
	typeBtn = widget.NewButton("Type", func() {
		selected := windowSelect.Selected

		laMu.RLock()
		curH := lastActiveHandle
		curTitle := lastActiveTitle
		laMu.RUnlock()

		var hwnd windows.Handle
		if selected == "" {
			hwnd = curH
		} else {
			var ok bool
			hwnd, ok = winMap[selected]
			if !ok || hwnd == 0 {
				status.SetText("Selected window is no longer available.")
				return
			}
		}

		if hwnd == 0 {
			status.SetText("No window focused yet. Click a window then come back.")
			return
		}

		setForegroundWindow(hwnd)
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

		go func(hwnd windows.Handle, curTitle string, txt string, perChar time.Duration) {
			err := sendText(txt, layoutSelect.Selected, perChar, shouldStop)
			canceled := shouldStop()

			title := strings.TrimSpace(getWindowText(hwnd))
			if title == "" {
				title = curTitle
			}
			title = truncateRunes(title, 30)

			fyne.Do(func() {
				if canceled {
					status.SetText("Typing stopped by user.")
				} else if err != nil {
					status.SetText("Error typing: " + err.Error())
				} else {
					status.SetText("Typed to: " + title)
				}
				setTypingUI(false)
				setStopRequested(false)
			})
		}(hwnd, curTitle, txt, perChar)
	})

	// --- Type Clipboard Button ---
	typeClipboardBtn = widget.NewButton("Type Clipboard", func() {
		selected := windowSelect.Selected

		laMu.RLock()
		curH := lastActiveHandle
		curTitle := lastActiveTitle
		laMu.RUnlock()

		var hwnd windows.Handle
		if selected == "" {
			hwnd = curH
		} else {
			var ok bool
			hwnd, ok = winMap[selected]
			if !ok || hwnd == 0 {
				status.SetText("Selected window is no longer available.")
				return
			}
		}

		if hwnd == 0 {
			status.SetText("No window focused yet. Click a window then come back.")
			return
		}

		setForegroundWindow(hwnd)
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

		go func(hwnd windows.Handle, curTitle string, txt string, perChar time.Duration) {
			err := sendText(txt, layoutSelect.Selected, perChar, shouldStop)
			canceled := shouldStop()

			title := strings.TrimSpace(getWindowText(hwnd))
			if title == "" {
				title = curTitle
			}
			title = truncateRunes(title, 30)

			fyne.Do(func() {
				if canceled {
					status.SetText("Typing stopped by user.")
				} else if err != nil {
					status.SetText("Error typing clipboard: " + err.Error())
				} else {
					status.SetText("Typed clipboard to: " + title)
				}
				setTypingUI(false)
				setStopRequested(false)
			})
		}(hwnd, curTitle, txt, perChar)
	})

	// Action container that switches between [Type, Type Clipboard] and [Stop]
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
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Typing Speed", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		speedSelect,
		customMsEntry, // hidden unless Custom is selected
	)

	header := container.NewBorder(nil, nil, left, right, nil)

	body := container.NewVBox(
		widget.NewLabelWithStyle("Text to type", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		inputRow,
		delayLabel,
		actionContainer,
		status,
	)

	// Version label in bottom right
	versionLabel := widget.NewLabel("v" + Version)
	versionLabel.TextStyle = fyne.TextStyle{Italic: true}
	versionLabel.Alignment = fyne.TextAlignTrailing
	footer := container.NewHBox(layout.NewSpacer(), versionLabel)

	content := container.NewBorder(header, footer, nil, nil, body)
	w.SetContent(content)

	updateDelayLabel()
	refreshWindows()
	w.ShowAndRun()
}
