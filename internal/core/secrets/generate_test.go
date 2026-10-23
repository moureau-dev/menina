package secrets

import "testing"

func TestGeneratePassword(t *testing.T) {
	pw, err := GeneratePassword(32)
	if err != nil {
		t.Fatal(err)
	}
	if len(pw) != 32 {
		t.Fatalf("length = %d, want 32", len(pw))
	}
	for _, r := range pw {
		if !isAllowed(r) {
			t.Fatalf("unexpected character %q in generated password", r)
		}
	}
	other, _ := GeneratePassword(32)
	if pw == other {
		t.Fatal("two generated passwords should differ")
	}
	def, _ := GeneratePassword(0)
	if len(def) != 32 {
		t.Fatalf("default length = %d, want 32", len(def))
	}
}

func isAllowed(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return false
}
