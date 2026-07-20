package tart

import (
	"testing"
	"time"
)

func TestParseBootToken_Wait60s(t *testing.T) {
	d, err := ParseBootToken("<wait60s>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	w, ok := d.(WaitDirective)
	if !ok {
		t.Fatalf("expected WaitDirective, got %T", d)
	}
	if w.Duration != 60*time.Second {
		t.Fatalf("expected 60s, got %v", w.Duration)
	}
}

func TestParseBootToken_Wait5s(t *testing.T) {
	d, err := ParseBootToken("<wait5s>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	w, ok := d.(WaitDirective)
	if !ok {
		t.Fatalf("expected WaitDirective, got %T", d)
	}
	if w.Duration != 5*time.Second {
		t.Fatalf("expected 5s, got %v", w.Duration)
	}
}

func TestParseBootToken_Wait100ms(t *testing.T) {
	d, err := ParseBootToken("<wait100ms>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	w, ok := d.(WaitDirective)
	if !ok {
		t.Fatalf("expected WaitDirective, got %T", d)
	}
	if w.Duration != 100*time.Millisecond {
		t.Fatalf("expected 100ms, got %v", w.Duration)
	}
}

func TestParseBootToken_WaitText(t *testing.T) {
	d, err := ParseBootToken("<wait 'Select Your Country'>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	w, ok := d.(WaitTextDirective)
	if !ok {
		t.Fatalf("expected WaitTextDirective, got %T", d)
	}
	if w.Text != "Select Your Country" {
		t.Fatalf("expected text 'Select Your Country', got %q", w.Text)
	}
	if w.Timeout != 60*time.Second {
		t.Fatalf("expected 60s timeout, got %v", w.Timeout)
	}
}

func TestParseBootToken_ClickText(t *testing.T) {
	d, err := ParseBootToken("<click 'Continue'>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c, ok := d.(ClickTextDirective)
	if !ok {
		t.Fatalf("expected ClickTextDirective, got %T", d)
	}
	if c.Text != "Continue" {
		t.Fatalf("expected text 'Continue', got %q", c.Text)
	}
	if c.Timeout != 60*time.Second {
		t.Fatalf("expected 60s timeout, got %v", c.Timeout)
	}
}

func TestParseBootToken_Spacebar(t *testing.T) {
	d, err := ParseBootToken("<spacebar>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	k, ok := d.(KeyDirective)
	if !ok {
		t.Fatalf("expected KeyDirective, got %T", d)
	}
	if k.Key != KeySpace {
		t.Fatalf("expected KeySpace, got %v", k.Key)
	}
}

func TestParseBootToken_Tab(t *testing.T) {
	d, err := ParseBootToken("<tab>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	k, ok := d.(KeyDirective)
	if !ok {
		t.Fatalf("expected KeyDirective, got %T", d)
	}
	if k.Key != KeyTab {
		t.Fatalf("expected KeyTab, got %v", k.Key)
	}
}

func TestParseBootToken_Enter(t *testing.T) {
	d, err := ParseBootToken("<enter>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	k, ok := d.(KeyDirective)
	if !ok {
		t.Fatalf("expected KeyDirective, got %T", d)
	}
	if k.Key != KeyReturn {
		t.Fatalf("expected KeyReturn, got %v", k.Key)
	}
}

func TestParseBootToken_Esc(t *testing.T) {
	d, err := ParseBootToken("<esc>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	k, ok := d.(KeyDirective)
	if !ok {
		t.Fatalf("expected KeyDirective, got %T", d)
	}
	if k.Key != KeyEscape {
		t.Fatalf("expected KeyEscape, got %v", k.Key)
	}
}

func TestParseBootToken_ArrowKeys(t *testing.T) {
	cases := []struct {
		token string
		key   SpecialKey
	}{
		{"<left>", KeyLeft},
		{"<right>", KeyRight},
		{"<up>", KeyUp},
		{"<down>", KeyDown},
	}
	for _, tc := range cases {
		d, err := ParseBootToken(tc.token)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.token, err)
		}
		k, ok := d.(KeyDirective)
		if !ok {
			t.Fatalf("%s: expected KeyDirective, got %T", tc.token, d)
		}
		if k.Key != tc.key {
			t.Fatalf("%s: expected %d, got %d", tc.token, tc.key, k.Key)
		}
	}
}

func TestParseBootToken_FunctionKeys(t *testing.T) {
	cases := []struct {
		token string
		key   SpecialKey
	}{
		{"<f2>", KeyF2},
		{"<f5>", KeyF5},
	}
	for _, tc := range cases {
		d, err := ParseBootToken(tc.token)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.token, err)
		}
		k, ok := d.(KeyDirective)
		if !ok {
			t.Fatalf("%s: expected KeyDirective, got %T", tc.token, d)
		}
		if k.Key != tc.key {
			t.Fatalf("%s: expected %d, got %d", tc.token, tc.key, k.Key)
		}
	}
}

func TestParseBootToken_ModifierOn(t *testing.T) {
	cases := []struct {
		token string
		key   SpecialKey
	}{
		{"<leftShiftOn>", KeyLeftShift},
		{"<leftAltOn>", KeyLeftAlt},
		{"<leftCtrlOn>", KeyLeftCtrl},
	}
	for _, tc := range cases {
		d, err := ParseBootToken(tc.token)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.token, err)
		}
		kd, ok := d.(KeyDownDirective)
		if !ok {
			t.Fatalf("%s: expected KeyDownDirective, got %T", tc.token, d)
		}
		if kd.Key != tc.key {
			t.Fatalf("%s: expected %d, got %d", tc.token, tc.key, kd.Key)
		}
	}
}

func TestParseBootToken_ModifierOff(t *testing.T) {
	cases := []struct {
		token string
		key   SpecialKey
	}{
		{"<leftShiftOff>", KeyLeftShift},
		{"<leftAltOff>", KeyLeftAlt},
		{"<leftCtrlOff>", KeyLeftCtrl},
	}
	for _, tc := range cases {
		d, err := ParseBootToken(tc.token)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.token, err)
		}
		ku, ok := d.(KeyUpDirective)
		if !ok {
			t.Fatalf("%s: expected KeyUpDirective, got %T", tc.token, d)
		}
		if ku.Key != tc.key {
			t.Fatalf("%s: expected %d, got %d", tc.token, tc.key, ku.Key)
		}
	}
}

func TestParseBootToken_PlainText(t *testing.T) {
	d, err := ParseBootToken("devcell")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tp, ok := d.(TypeDirective)
	if !ok {
		t.Fatalf("expected TypeDirective, got %T", d)
	}
	if tp.Text != "devcell" {
		t.Fatalf("expected 'devcell', got %q", tp.Text)
	}
}

func TestParseBootToken_UnknownSpecial(t *testing.T) {
	_, err := ParseBootToken("<unknown>")
	if err == nil {
		t.Fatal("expected error for unknown special token")
	}
}

func TestVNCKeyEvent_Spacebar(t *testing.T) {
	events := EncodeSpecialKey(KeySpace)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Key != 0x0020 || !events[0].DownFlag {
		t.Fatalf("expected down event with key 0x0020, got %+v", events[0])
	}
	if events[1].Key != 0x0020 || events[1].DownFlag {
		t.Fatalf("expected up event with key 0x0020, got %+v", events[1])
	}
}

func TestVNCKeyEvent_Letter(t *testing.T) {
	events := EncodeCharacter('a')
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Key != 0x61 || !events[0].DownFlag {
		t.Fatalf("expected down event with key 0x61, got %+v", events[0])
	}
	if events[1].Key != 0x61 || events[1].DownFlag {
		t.Fatalf("expected up event with key 0x61, got %+v", events[1])
	}
}

func TestVNCKeyEvent_ShiftLetter(t *testing.T) {
	events := EncodeCharacter('A')
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}
	shift := KeyLeftShift.VNCKeyCode()
	if events[0].Key != shift || !events[0].DownFlag {
		t.Fatalf("expected shift down, got %+v", events[0])
	}
	if events[1].Key != uint32('A') || !events[1].DownFlag {
		t.Fatalf("expected A down, got %+v", events[1])
	}
	if events[2].Key != uint32('A') || events[2].DownFlag {
		t.Fatalf("expected A up, got %+v", events[2])
	}
	if events[3].Key != shift || events[3].DownFlag {
		t.Fatalf("expected shift up, got %+v", events[3])
	}
}

func TestEncodeString_Simple(t *testing.T) {
	events := EncodeString("abc")
	if len(events) != 6 {
		t.Fatalf("expected 6 events, got %d", len(events))
	}
}

func TestEncodeString_Mixed(t *testing.T) {
	events := EncodeString("Ab")
	// 'A' = 4 events (shift), 'b' = 2 events
	if len(events) != 6 {
		t.Fatalf("expected 6 events, got %d", len(events))
	}
}

func TestVNCKeyCodes_NewKeys(t *testing.T) {
	cases := []struct {
		key      SpecialKey
		expected uint32
	}{
		{KeyLeftAlt, 0xFFE9},
		{KeyLeftCtrl, 0xFFE3},
		{KeyLeft, 0xFF51},
		{KeyRight, 0xFF53},
		{KeyUp, 0xFF52},
		{KeyDown, 0xFF54},
		{KeyF2, 0xFFBF},
		{KeyF5, 0xFFC2},
	}
	for _, tc := range cases {
		got := tc.key.VNCKeyCode()
		if got != tc.expected {
			t.Errorf("key %d: expected 0x%04X, got 0x%04X", tc.key, tc.expected, got)
		}
	}
}

func TestGenerateBootCommands_DefaultUser(t *testing.T) {
	cmds := GenerateBootCommands("devcell", "devcell")
	if len(cmds) == 0 {
		t.Fatal("expected non-empty boot commands")
	}

	// First directive should be a WaitText for "Country" (OCR-based wait)
	wt, ok := cmds[0].(WaitTextDirective)
	if !ok {
		t.Fatalf("expected first directive to be WaitTextDirective, got %T", cmds[0])
	}
	if wt.Text != "Country" {
		t.Fatalf("expected wait for 'Country', got %q", wt.Text)
	}

	// Verify username and password appear in TypeDirectives
	foundUser := false
	foundPass := false
	for _, cmd := range cmds {
		if td, ok := cmd.(TypeDirective); ok {
			if td.Text == "devcell" {
				foundUser = true
				foundPass = true
			}
		}
	}
	if !foundUser {
		t.Fatal("expected username 'devcell' in boot commands")
	}
	if !foundPass {
		t.Fatal("expected password 'devcell' in boot commands")
	}
}

func TestGenerateBootCommands_CustomUser(t *testing.T) {
	cmds := GenerateBootCommands("admin", "secret")

	foundAdmin := false
	foundSecret := false
	for _, cmd := range cmds {
		if td, ok := cmd.(TypeDirective); ok {
			if td.Text == "admin" {
				foundAdmin = true
			}
			if td.Text == "secret" {
				foundSecret = true
			}
		}
	}
	if !foundAdmin {
		t.Fatal("expected 'admin' in boot commands")
	}
	if !foundSecret {
		t.Fatal("expected 'secret' in boot commands")
	}
}

func TestGenerateBootCommands_HasWaits(t *testing.T) {
	cmds := GenerateBootCommands("devcell", "devcell")

	waitCount := 0
	for _, cmd := range cmds {
		if _, ok := cmd.(WaitDirective); ok {
			waitCount++
		}
	}
	if waitCount == 0 {
		t.Fatal("expected at least one WaitDirective in boot commands")
	}
	if waitCount < 3 {
		t.Fatalf("expected multiple wait directives, got %d", waitCount)
	}
}

func TestGenerateBootCommands_HasOCRDirectives(t *testing.T) {
	cmds := GenerateBootCommands("devcell", "devcell")

	waitTextCount := 0
	clickTextCount := 0
	for _, cmd := range cmds {
		if _, ok := cmd.(WaitTextDirective); ok {
			waitTextCount++
		}
		if _, ok := cmd.(ClickTextDirective); ok {
			clickTextCount++
		}
	}
	if waitTextCount == 0 {
		t.Fatal("expected at least one WaitTextDirective")
	}
	if clickTextCount == 0 {
		t.Fatal("expected at least one ClickTextDirective")
	}
}

func TestGenerateBootCommands_HasModifierKeys(t *testing.T) {
	cmds := GenerateBootCommands("devcell", "devcell")

	keyDownCount := 0
	keyUpCount := 0
	for _, cmd := range cmds {
		if _, ok := cmd.(KeyDownDirective); ok {
			keyDownCount++
		}
		if _, ok := cmd.(KeyUpDirective); ok {
			keyUpCount++
		}
	}
	if keyDownCount == 0 {
		t.Fatal("expected at least one KeyDownDirective")
	}
	if keyUpCount == 0 {
		t.Fatal("expected at least one KeyUpDirective")
	}
	if keyDownCount != keyUpCount {
		t.Fatalf("mismatched modifier key-down (%d) / key-up (%d) count", keyDownCount, keyUpCount)
	}
}

func TestGenerateBootCommands_EnablesSSH(t *testing.T) {
	cmds := GenerateBootCommands("devcell", "devcell")

	foundSSH := false
	for _, cmd := range cmds {
		if td, ok := cmd.(TypeDirective); ok {
			if td.Text == "sudo systemsetup -setremotelogin on" {
				foundSSH = true
				break
			}
		}
	}
	if !foundSSH {
		t.Fatal("expected boot commands to enable SSH via systemsetup")
	}
}
