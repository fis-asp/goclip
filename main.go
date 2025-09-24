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
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"golang.org/x/sys/windows"

	_ "embed"
)

//go:embed assets/logo/app.ico
var embeddedAppIco []byte

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
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
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

	vkShift   = 0x10
	vkControl = 0x11
	vkMenu    = 0x12
	vkReturn  = 0x0D

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

func getForeground() windows.Handle {
	h, _, _ := procGetForegroundWindow.Call()
	return windows.Handle(h)
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
	if (shift & 0x04) != 0 {
		_ = pressVK(vkMenu, false)
	}
	if (shift & 0x02) != 0 {
		_ = pressVK(vkControl, false)
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
	if err := tapScan(sc, isExtendedVK(vk)); err != nil {
		releaseModifiers(shift)
		return err
	}
	releaseModifiers(shift)
	time.Sleep(perCharDelay)
	return nil
}

func sendText(text string, layout string, perCharDelay time.Duration) error {
	hkl := loadHKLByName(layout)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	for _, r := range text {
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
			short := truncateRunes(wi.Title, 30) // ← limit to 30 chars in list
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
		status.SetText("⚠️ Foreground watcher failed → falling back: " + err.Error())
	}

	// Ensure cleanup when main exits
	defer stopForegroundWatcher()

	// --- Type Button ---
	typeBtn := widget.NewButton("Type", func() {
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

		txt := inputEntry.Text
		if txt == "" {
			status.SetText("Nothing to type.")
			return
		}

		// Run typing asynchronously in goroutine
		go func(hwnd windows.Handle, txt string, curTitle string) {
			setForegroundWindow(hwnd)
			time.Sleep(150 * time.Millisecond)

			err := sendText(txt, layoutSelect.Selected, 7*time.Millisecond)

		w.Canvas().Invoke(func() {
			if err != nil {
				status.SetText("Error typing: " + err.Error())
				return
			}
			title := strings.TrimSpace(getWindowText(hwnd))
			if title == "" {
				title = curTitle
			}
			title = truncateRunes(title, 30)
			status.SetText("Typed to: " + title)
		})
		}(hwnd, txt, curTitle)
	})

	// --- Type Clipboard Button ---
	typeClipboardBtn := widget.NewButton("Type Clipboard", func() {
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

		txt := w.Clipboard().Content()
		if txt == "" {
			status.SetText("Clipboard is empty.")
			return
		}

		// Run typing asynchronously in goroutine
		go func(hwnd windows.Handle, txt string, curTitle string) {
			setForegroundWindow(hwnd)
			time.Sleep(150 * time.Millisecond)

			err := sendText(txt, layoutSelect.Selected, 7*time.Millisecond)

		w.Canvas().Invoke(func() {
			if err != nil {
				status.SetText("Error typing: " + err.Error())
				return
			}
			title := strings.TrimSpace(getWindowText(hwnd))
			if title == "" {
				title = curTitle
			}
			title = truncateRunes(title, 30)
			status.SetText("Typed to: " + title)
		})
		}(hwnd, txt, curTitle)
	})

	// Left side: window selector + buttons
	left := container.NewVBox(
		widget.NewLabelWithStyle("Target Window", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewHBox(windowSelect, clearBtn),
		refreshBtn,
		lastActiveLabel,
	)

	// Right side: layout selector
	right := container.NewVBox(
		widget.NewLabelWithStyle("Keyboard Layout", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		layoutSelect,
	)

	header := container.NewBorder(nil, nil, left, right, nil)

	body := container.NewVBox(
		widget.NewLabelWithStyle("Text to type", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		inputRow,
		container.NewHBox(typeBtn, typeClipboardBtn),
		status,
	)

	content := container.NewBorder(header, nil, nil, nil, body)
	w.SetContent(content)

	refreshWindows()
	w.ShowAndRun()
}
