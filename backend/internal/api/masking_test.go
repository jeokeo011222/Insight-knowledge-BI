package api

import "testing"

func TestMaskStringBuiltins(t *testing.T) {
	cases := []struct {
		name   string
		value  string
		kind   string
		rule   string
		expect string
	}{
		{"phone", "13812341234", MaskPhone, "", "138****1234"},
		{"phone short", "123", MaskPhone, "", "***"},
		{"idcard", "110101199003071234", MaskIDCard, "", "1101**********1234"},
		{"idcard short", "1234", MaskIDCard, "", "****"},
		{"email", "alice@example.com", MaskEmail, "", "a***@example.com"},
		{"email invalid", "not-an-email", MaskEmail, "", "************"},
		{"name", "张三丰", MaskName, "", "张**"},
		{"name single", "张", MaskName, "", "*"},
		{"full", "anything", MaskFull, "", "***"},
		{"off", "keep me", "", "", "keep me"},
	}
	for _, tc := range cases {
		if got := maskString(tc.value, tc.kind, tc.rule); got != tc.expect {
			t.Fatalf("%s: maskString(%q) = %q, want %q", tc.name, tc.value, got, tc.expect)
		}
	}
}

func TestMaskStringCustomRegex(t *testing.T) {
	got := maskString("ORD-123456-XY", MaskCustom, `\d{6}`)
	if got != "ORD-******-XY" {
		t.Fatalf("custom mask = %q", got)
	}
	// An invalid pattern must fall back to a full mask instead of leaking.
	if got := maskString("secret", MaskCustom, "([unclosed"); got != "***" {
		t.Fatalf("invalid pattern should fully mask, got %q", got)
	}
}

func TestMaskValueScalars(t *testing.T) {
	if got := maskValue(int64(13812341234), MaskPhone, ""); got != "138****1234" {
		t.Fatalf("numeric phone not masked: %v", got)
	}
	if got := maskValue(nil, MaskPhone, ""); got != nil {
		t.Fatalf("nil should stay nil, got %v", got)
	}
	type custom struct{ A int }
	value := custom{A: 1}
	if got := maskValue(value, MaskPhone, ""); got != value {
		t.Fatal("structured values must be passed through untouched")
	}
}

func TestMaskTypesIncludesOff(t *testing.T) {
	found := false
	for _, kind := range MaskTypes() {
		if kind == "" {
			found = true
		}
	}
	if !found {
		t.Fatal("MaskTypes must include the empty (disabled) option")
	}
}
