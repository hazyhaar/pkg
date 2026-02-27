package injection

import "testing"

func TestNormalize_Clean(t *testing.T) {
	got := Normalize("Hello World")
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestNormalize_Cyrillic(t *testing.T) {
	// "р" is Cyrillic Er (U+0440), "а" is Cyrillic A (U+0430)
	got := Normalize("\u0440\u0430ssword")
	if got != "password" {
		t.Errorf("got %q, want %q", got, "password")
	}
}

func TestNormalize_Leet(t *testing.T) {
	got := Normalize("1gn0r3 pr3v10us")
	if got != "ignore previous" {
		t.Errorf("got %q, want %q", got, "ignore previous")
	}
}

func TestNormalize_Fullwidth(t *testing.T) {
	// Ａ=U+FF21, etc — NFKD folds to ASCII
	got := Normalize("\uff29\uff27\uff2e\uff2f\uff32\uff25")
	if got != "ignore" {
		t.Errorf("got %q, want %q", got, "ignore")
	}
}

func TestNormalize_ZeroWidth(t *testing.T) {
	got := Normalize("ig\u200Bnore")
	if got != "ignore" {
		t.Errorf("got %q, want %q", got, "ignore")
	}
}

func TestNormalize_Accents(t *testing.T) {
	got := Normalize("ignorér précédentes")
	if got != "ignorer precedentes" {
		t.Errorf("got %q, want %q", got, "ignorer precedentes")
	}
}

func TestNormalize_Combined(t *testing.T) {
	// Leet + zero-width + Cyrillic р (U+0440)
	got := Normalize("1gn\u200B0r3 \u0440r3v10us")
	if got != "ignore previous" {
		t.Errorf("got %q, want %q", got, "ignore previous")
	}
}

func TestNormalize_MarkdownBold(t *testing.T) {
	got := Normalize("**ignore** previous")
	if got != "ignore previous" {
		t.Errorf("got %q, want %q", got, "ignore previous")
	}
}

func TestNormalize_MarkdownCode(t *testing.T) {
	got := Normalize("ignore `previous` instructions")
	if got != "ignore previous instructions" {
		t.Errorf("got %q, want %q", got, "ignore previous instructions")
	}
}

func TestNormalize_HTMLTags(t *testing.T) {
	got := Normalize("ignore <b>previous</b> instructions")
	if got != "ignore previous instructions" {
		t.Errorf("got %q, want %q", got, "ignore previous instructions")
	}
}

func TestNormalize_MarkdownLink(t *testing.T) {
	got := Normalize("[ignore](http://evil.com) previous")
	if got != "ignore previous" {
		t.Errorf("got %q, want %q", got, "ignore previous")
	}
}

func TestNormalize_LaTeX(t *testing.T) {
	got := Normalize("\\textbf{ignore} previous")
	if got != "ignore previous" {
		t.Errorf("got %q, want %q", got, "ignore previous")
	}
}

func TestStripInvisible_ZeroWidth(t *testing.T) {
	got := StripInvisible("abc\u200B\u200B\u200Bdef")
	if got != "abcdef" {
		t.Errorf("got %q, want %q", got, "abcdef")
	}
}

func TestStripInvisible_Bidi(t *testing.T) {
	got := StripInvisible("abc\u200Edef\u202Aghi")
	if got != "abcdefghi" {
		t.Errorf("got %q, want %q", got, "abcdefghi")
	}
}

func TestStripInvisible_SoftHyphen(t *testing.T) {
	got := StripInvisible("ig\u00ADnore")
	if got != "ignore" {
		t.Errorf("got %q, want %q", got, "ignore")
	}
}

func TestStripInvisible_Interlinear(t *testing.T) {
	got := StripInvisible("abc\uFFF9def")
	if got != "abcdef" {
		t.Errorf("got %q, want %q", got, "abcdef")
	}
}

func TestStripInvisible_PreservesNewline(t *testing.T) {
	got := StripInvisible("abc\ndef")
	if got != "abc\ndef" {
		t.Errorf("got %q, want %q", got, "abc\ndef")
	}
}

func TestStripInvisible_EveryChar(t *testing.T) {
	got := StripInvisible("i\u200Bg\u2060n\u200Co\uFEFFr\u00ADe")
	if got != "ignore" {
		t.Errorf("got %q, want %q", got, "ignore")
	}
}

func TestFoldConfusables_Table(t *testing.T) {
	// Verify each confusables.json entry maps correctly
	tests := map[rune]rune{
		'\u0430': 'a', // Cyrillic а
		'\u0441': 'c', // Cyrillic с
		'\u0435': 'e', // Cyrillic е
		'\u043E': 'o', // Cyrillic о
		'\u0440': 'p', // Cyrillic р
		'\u0445': 'x', // Cyrillic х
		'\u0443': 'y', // Cyrillic у
		'\u0456': 'i', // Cyrillic і
		'\u0410': 'A', // Cyrillic А
		'\u0421': 'C', // Cyrillic С
		'\u0415': 'E', // Cyrillic Е
		'\u041E': 'O', // Cyrillic О
		'\u0420': 'P', // Cyrillic Р
		'\u0425': 'X', // Cyrillic Х
		'\u0423': 'Y', // Cyrillic У
		'\u0406': 'I', // Cyrillic І
		'\u0251': 'a', // IPA ɑ
		'\u03B5': 'e', // Greek ε
		'\u03BF': 'o', // Greek ο
		'\u03BD': 'v', // Greek ν
		'\u0261': 'g', // IPA ɡ
		'\u212F': 'e', // ℯ
		'\u2139': 'i', // ℹ
		'\u2134': 'o', // ℴ
	}
	for in, want := range tests {
		got := FoldConfusables(string(in))
		if got != string(want) {
			t.Errorf("FoldConfusables(%q U+%04X) = %q, want %q", string(in), in, got, string(want))
		}
	}
}

func TestFoldLeet_Table(t *testing.T) {
	tests := map[rune]rune{
		'0': 'o', '1': 'i', '3': 'e', '4': 'a', '5': 's',
		'7': 't', '8': 'b', '@': 'a', '$': 's', '!': 'i',
		'\u00A1': 'i', // ¡
		'\u20AC': 'e', // €
	}
	for in, want := range tests {
		got := FoldLeet(string(in))
		if got != string(want) {
			t.Errorf("FoldLeet(%q) = %q, want %q", string(in), got, string(want))
		}
	}
}
