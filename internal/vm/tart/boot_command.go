package tart

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// BootDirective represents one step in the boot command sequence.
type BootDirective interface {
	directiveType() string
}

// WaitDirective pauses for a duration before the next step.
type WaitDirective struct {
	Duration time.Duration
}

func (w WaitDirective) directiveType() string { return "wait" }

// WaitTextDirective polls the VNC framebuffer via OCR until the given text
// appears on screen. Times out after Timeout.
type WaitTextDirective struct {
	Text    string
	Timeout time.Duration
}

func (w WaitTextDirective) directiveType() string { return "waitText" }

// ClickTextDirective finds text on screen via OCR and clicks its center.
// Polls until the text appears or Timeout expires.
type ClickTextDirective struct {
	Text    string
	Timeout time.Duration
}

func (c ClickTextDirective) directiveType() string { return "clickText" }

// KeyDirective sends a single special key press (down + up).
type KeyDirective struct {
	Key SpecialKey
}

func (k KeyDirective) directiveType() string { return "key" }

// KeyDownDirective sends a key-down event without a corresponding key-up.
// Used for modifier key holds (Shift+Tab, Alt+Space, etc.).
type KeyDownDirective struct {
	Key SpecialKey
}

func (k KeyDownDirective) directiveType() string { return "keyDown" }

// KeyUpDirective sends a key-up event. Pairs with a prior KeyDownDirective.
type KeyUpDirective struct {
	Key SpecialKey
}

func (k KeyUpDirective) directiveType() string { return "keyUp" }

// TypeDirective types a string of characters.
type TypeDirective struct {
	Text string
}

func (t TypeDirective) directiveType() string { return "type" }

// SpecialKey is a named key for VNC key events.
type SpecialKey int

const (
	KeySpace SpecialKey = iota
	KeyTab
	KeyReturn
	KeyEscape
	KeyLeftShift
	KeyLeftAlt
	KeyLeftCtrl
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyF2
	KeyF5
)

// VNCKeyCode returns the X11 keysym for a SpecialKey.
func (k SpecialKey) VNCKeyCode() uint32 {
	switch k {
	case KeySpace:
		return 0x0020
	case KeyTab:
		return 0xFF09
	case KeyReturn:
		return 0xFF0D
	case KeyEscape:
		return 0xFF1B
	case KeyLeftShift:
		return 0xFFE1
	case KeyLeftAlt:
		return 0xFFE9
	case KeyLeftCtrl:
		return 0xFFE3
	case KeyLeft:
		return 0xFF51
	case KeyRight:
		return 0xFF53
	case KeyUp:
		return 0xFF52
	case KeyDown:
		return 0xFF54
	case KeyF2:
		return 0xFFBF
	case KeyF5:
		return 0xFFC2
	default:
		return 0
	}
}

// VNCKeyEvent represents a single VNC key press or release.
type VNCKeyEvent struct {
	DownFlag bool
	Key      uint32 // X11 keysym
}

// EncodeSpecialKey returns the down+up pair for a special key.
func EncodeSpecialKey(k SpecialKey) []VNCKeyEvent {
	code := k.VNCKeyCode()
	return []VNCKeyEvent{
		{DownFlag: true, Key: code},
		{DownFlag: false, Key: code},
	}
}

// EncodeCharacter returns the key events for typing a single character.
// Uppercase letters include shift key events.
func EncodeCharacter(c rune) []VNCKeyEvent {
	if c >= 'A' && c <= 'Z' {
		shift := KeyLeftShift.VNCKeyCode()
		return []VNCKeyEvent{
			{DownFlag: true, Key: shift},
			{DownFlag: true, Key: uint32(c)},
			{DownFlag: false, Key: uint32(c)},
			{DownFlag: false, Key: shift},
		}
	}
	return []VNCKeyEvent{
		{DownFlag: true, Key: uint32(c)},
		{DownFlag: false, Key: uint32(c)},
	}
}

// EncodeString returns key events for typing a full string.
func EncodeString(s string) []VNCKeyEvent {
	var events []VNCKeyEvent
	for _, c := range s {
		events = append(events, EncodeCharacter(c)...)
	}
	return events
}

// waitRe matches wait tokens like "<wait60s>" or "<wait100ms>".
var waitRe = regexp.MustCompile(`^<wait(\d+)(ms|s)>$`)

// waitTextRe matches OCR wait tokens like "<wait 'Select Your Country'>".
var waitTextRe = regexp.MustCompile(`^<wait '([^']+)'>$`)

// clickTextRe matches OCR click tokens like "<click 'Continue'>".
var clickTextRe = regexp.MustCompile(`^<click '([^']+)'>$`)

// ParseBootToken parses a single boot command token.
func ParseBootToken(token string) (BootDirective, error) {
	if m := waitRe.FindStringSubmatch(token); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("invalid wait duration: %s", token)
		}
		switch m[2] {
		case "s":
			return WaitDirective{Duration: time.Duration(n) * time.Second}, nil
		case "ms":
			return WaitDirective{Duration: time.Duration(n) * time.Millisecond}, nil
		}
	}

	if m := waitTextRe.FindStringSubmatch(token); m != nil {
		return WaitTextDirective{Text: m[1], Timeout: 60 * time.Second}, nil
	}
	if m := clickTextRe.FindStringSubmatch(token); m != nil {
		return ClickTextDirective{Text: m[1], Timeout: 60 * time.Second}, nil
	}

	switch token {
	case "<spacebar>":
		return KeyDirective{Key: KeySpace}, nil
	case "<tab>":
		return KeyDirective{Key: KeyTab}, nil
	case "<enter>":
		return KeyDirective{Key: KeyReturn}, nil
	case "<esc>":
		return KeyDirective{Key: KeyEscape}, nil
	case "<left>":
		return KeyDirective{Key: KeyLeft}, nil
	case "<right>":
		return KeyDirective{Key: KeyRight}, nil
	case "<up>":
		return KeyDirective{Key: KeyUp}, nil
	case "<down>":
		return KeyDirective{Key: KeyDown}, nil
	case "<f2>":
		return KeyDirective{Key: KeyF2}, nil
	case "<f5>":
		return KeyDirective{Key: KeyF5}, nil

	// Modifier key-down/key-up (hold/release)
	case "<leftShiftOn>":
		return KeyDownDirective{Key: KeyLeftShift}, nil
	case "<leftShiftOff>":
		return KeyUpDirective{Key: KeyLeftShift}, nil
	case "<leftAltOn>":
		return KeyDownDirective{Key: KeyLeftAlt}, nil
	case "<leftAltOff>":
		return KeyUpDirective{Key: KeyLeftAlt}, nil
	case "<leftCtrlOn>":
		return KeyDownDirective{Key: KeyLeftCtrl}, nil
	case "<leftCtrlOff>":
		return KeyUpDirective{Key: KeyLeftCtrl}, nil
	}

	// If it looks like a special token but we don't recognize it, error.
	if len(token) > 2 && token[0] == '<' && token[len(token)-1] == '>' {
		return nil, fmt.Errorf("unknown special token: %s", token)
	}

	return TypeDirective{Text: token}, nil
}

// GenerateBootCommands returns the boot command sequence for macOS Setup
// Assistant automation. Uses OCR-based screen detection for timing and the
// Tart-proven keystroke sequence for navigation.
func GenerateBootCommands(username, password string) []BootDirective {
	return []BootDirective{
		// Wait for Setup Assistant to render (OCR detects "Country" on screen).
		WaitTextDirective{Text: "Country", Timeout: 180 * time.Second},

		// Dismiss welcome / language screen
		KeyDirective{Key: KeySpace},
		WaitDirective{Duration: 2 * time.Second},

		// Language selection: italiano trick (scrolls list), then english
		TypeDirective{Text: "italiano"},
		KeyDirective{Key: KeyEscape},
		TypeDirective{Text: "english"},
		KeyDirective{Key: KeyReturn},
		WaitTextDirective{Text: "Country", Timeout: 30 * time.Second},

		// Country: click the header to ensure focus, then type selection
		ClickTextDirective{Text: "Select Your Country or Region", Timeout: 30 * time.Second},
		WaitDirective{Duration: 1 * time.Second},
		TypeDirective{Text: "united states"},
		// Shift+Tab to "Continue", then activate
		KeyDownDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeyTab},
		KeyUpDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeySpace},

		// Written and Spoken Languages → Continue
		WaitDirective{Duration: 5 * time.Second},
		KeyDirective{Key: KeyTab},
		KeyDirective{Key: KeyTab},
		KeyDirective{Key: KeyTab},
		KeyDirective{Key: KeySpace},
		KeyDirective{Key: KeyTab},
		KeyDirective{Key: KeyTab},
		KeyDirective{Key: KeySpace},

		// Accessibility → Not Now
		WaitDirective{Duration: 5 * time.Second},
		KeyDownDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeyTab},
		KeyUpDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeySpace},

		// Data & Privacy → Continue
		WaitDirective{Duration: 5 * time.Second},
		KeyDownDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeyTab},
		KeyUpDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeySpace},

		// Migration Assistant → Not Now
		WaitDirective{Duration: 5 * time.Second},
		KeyDownDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeyTab},
		KeyUpDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeySpace},

		// Sign In with Apple ID → Set Up Later
		WaitDirective{Duration: 5 * time.Second},
		KeyDownDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeyTab},
		KeyUpDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeySpace},

		// Terms and Conditions → Agree
		WaitDirective{Duration: 5 * time.Second},
		KeyDownDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeyTab},
		KeyUpDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeySpace},

		// Create Computer Account
		WaitDirective{Duration: 5 * time.Second},
		TypeDirective{Text: username}, // Full Name
		KeyDirective{Key: KeyTab},
		TypeDirective{Text: username}, // Account Name
		KeyDirective{Key: KeyTab},
		TypeDirective{Text: password}, // Password
		KeyDirective{Key: KeyTab},
		TypeDirective{Text: password}, // Verify
		KeyDirective{Key: KeyTab},     // Skip hint
		KeyDirective{Key: KeyTab},     // Continue button
		KeyDirective{Key: KeySpace},   // Activate Continue
		// Confirmation dialog for blank hint
		KeyDirective{Key: KeyTab},
		KeyDirective{Key: KeyTab},
		KeyDirective{Key: KeySpace},

		// Account creation takes a while — wait for next screen
		WaitDirective{Duration: 120 * time.Second},

		// Enable VoiceOver briefly to enable keyboard navigation
		KeyDownDirective{Key: KeyLeftAlt},
		KeyDirective{Key: KeyF5},
		KeyUpDirective{Key: KeyLeftAlt},
		WaitDirective{Duration: 5 * time.Second},
		// Dismiss VoiceOver welcome
		KeyDownDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeyTab},
		KeyUpDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeySpace},

		// Location Services → Don't Use / Continue
		WaitDirective{Duration: 5 * time.Second},
		KeyDirective{Key: KeyTab},
		KeyDirective{Key: KeySpace},

		// Analytics → Continue
		WaitDirective{Duration: 5 * time.Second},
		KeyDownDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeyTab},
		KeyUpDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeySpace},

		// Screen Time → Set Up Later / Continue
		WaitDirective{Duration: 5 * time.Second},
		KeyDirective{Key: KeyTab},
		KeyDirective{Key: KeySpace},

		// Siri → Continue
		WaitDirective{Duration: 5 * time.Second},
		KeyDownDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeyTab},
		KeyUpDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeySpace},

		// Time Zone → select UTC, continue
		WaitDirective{Duration: 5 * time.Second},
		KeyDirective{Key: KeyTab},
		KeyDirective{Key: KeyTab},
		TypeDirective{Text: "UTC"},
		KeyDirective{Key: KeyReturn},
		KeyDownDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeyTab},
		KeyDirective{Key: KeyTab},
		KeyUpDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeySpace},

		// Appearance → Continue (accept default)
		WaitDirective{Duration: 5 * time.Second},
		KeyDownDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeyTab},
		KeyUpDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeySpace},

		// More screens that may appear — skip through
		WaitDirective{Duration: 5 * time.Second},
		KeyDirective{Key: KeyTab},
		KeyDirective{Key: KeySpace},

		// Final screen / Desktop welcome
		WaitDirective{Duration: 5 * time.Second},
		KeyDirective{Key: KeyTab},
		KeyDirective{Key: KeySpace},
		KeyDownDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeyTab},
		KeyUpDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeySpace},

		// Skip remaining screens
		WaitDirective{Duration: 5 * time.Second},
		KeyDownDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeyTab},
		KeyUpDirective{Key: KeyLeftShift},
		KeyDirective{Key: KeySpace},

		WaitDirective{Duration: 5 * time.Second},
		KeyDirective{Key: KeyTab},
		KeyDirective{Key: KeySpace},

		// We're at the desktop now. Disable VoiceOver if still on.
		WaitDirective{Duration: 3 * time.Second},
		KeyDirective{Key: KeySpace},

		// --- Post-SA: Enable SSH via Terminal ---
		// Disable VoiceOver
		KeyDownDirective{Key: KeyLeftAlt},
		KeyDirective{Key: KeyF5},
		KeyUpDirective{Key: KeyLeftAlt},
		WaitDirective{Duration: 3 * time.Second},

		// Open Spotlight and launch Terminal
		KeyDownDirective{Key: KeyLeftAlt},
		KeyDirective{Key: KeySpace},
		KeyUpDirective{Key: KeyLeftAlt},
		WaitDirective{Duration: 2 * time.Second},
		TypeDirective{Text: "Terminal"},
		KeyDirective{Key: KeyReturn},
		WaitDirective{Duration: 5 * time.Second},

		// Enable full keyboard access
		TypeDirective{Text: "defaults write NSGlobalDomain AppleKeyboardUIMode -int 3"},
		KeyDirective{Key: KeyReturn},
		WaitDirective{Duration: 2 * time.Second},

		// Enable Remote Login (SSH) via systemsetup
		TypeDirective{Text: "sudo systemsetup -setremotelogin on"},
		KeyDirective{Key: KeyReturn},
		WaitDirective{Duration: 3 * time.Second},
		TypeDirective{Text: password}, // sudo password
		KeyDirective{Key: KeyReturn},
		WaitDirective{Duration: 3 * time.Second},

		// Disable Gatekeeper
		TypeDirective{Text: "sudo spctl --global-disable"},
		KeyDirective{Key: KeyReturn},
		WaitDirective{Duration: 2 * time.Second},

		// Quit Terminal
		KeyDownDirective{Key: KeyLeftAlt},
		TypeDirective{Text: "q"},
		KeyUpDirective{Key: KeyLeftAlt},
		WaitDirective{Duration: 2 * time.Second},
	}
}
