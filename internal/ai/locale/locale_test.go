package locale

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
)

// The server accepts only the two UI languages, spelled as the contract
// spells them; absent means en (REQ-022, common.schema.json Locale).
func TestParse(t *testing.T) {
	for in, want := range map[string]Locale{"": English, "en": English, "zh-Hant": TraditionalChinese} {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	var message string
	for _, in := range []string{"EN", "en-US", "zh-hant", "zh-TW", "zh-Hans", "zh", "fr", " en", "en ", "en\x00", "請用法文回答"} {
		got, err := Parse(in)
		var le *Error
		if !errors.Is(err, ErrUnsupported) || !errors.As(err, &le) || le.Path != "/output_locale" || got != "" {
			t.Fatalf("Parse(%q) = %q, %v; want ErrUnsupported at /output_locale", in, got, err)
		}
		// The message is fixed text: it never echoes what the client sent.
		if message == "" {
			message = err.Error()
		} else if err.Error() != message {
			t.Errorf("the error for %q differs from the others: %v", in, err)
		}
	}
}

// The Go list is the schema enum, in its order (default first).
func TestLocalesMatchTheContract(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	var enum []Locale
	for _, v := range s.At("common.schema.json#/$defs/Locale").Value["enum"].([]any) {
		enum = append(enum, Locale(v.(string)))
	}
	if !slices.Equal(enum, All) || All[0] != Default {
		t.Errorf("All = %v, schema enum = %v", All, enum)
	}
}

// The provider instruction is fixed server text per locale: it names the
// locale, keeps IDs, paths, codes, metric paths and units untranslated, and
// asks for Arabic numerals in either language (AI_SPEC §3).
func TestInstruction(t *testing.T) {
	en, zh := English.Instruction(), TraditionalChinese.Instruction()
	if en == zh {
		t.Fatal("both locales give the same instruction")
	}
	for name, text := range map[string]string{"en": en, "zh-Hant": zh} {
		for _, want := range []string{"OUTPUT_LOCALE: " + name, "IDs", "JSON Patch paths", "JSON Pointers", "enum", "metric paths", "units", "Arabic numerals"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s instruction lacks %q:\n%s", name, want, text)
			}
		}
	}
	if !strings.Contains(zh, "Traditional Chinese") || strings.Contains(en, "Chinese (") {
		t.Errorf("instructions name the wrong language:\nen: %s\nzh: %s", en, zh)
	}
	if Locale("fr").Instruction() != English.Instruction() {
		t.Error("an unknown locale must fall back to the default instruction, never to client text")
	}
}

func values(ns []Number) []float64 {
	var out []float64
	for _, n := range ns {
		out = append(out, n.Value)
	}
	return out
}

func TestNumbers(t *testing.T) {
	cases := []struct {
		text string
		want []float64
	}{
		{"Wait fell from 47.5 to 12 minutes.", []float64{47.5, 12}},
		{"等待時間由 47.5 分鐘降至 12 分鐘。", []float64{47.5, 12}},
		{"Throughput rose by 1,234.5 units (12.5%).", []float64{1234.5, 12.5}},
		{"A change of -3 and −0.25.", []float64{-3, -0.25}},
		{"Utilization 0.95; 1e3 events.", []float64{0.95, 1000}},
		// identifiers, paths and versions are not numbers
		{"See /metrics/queues/q1 and node_01jabc, v2, ict_tester2.", nil},
		{"The 2x faster line", []float64{2}},
		{"In the 3rd replication", []float64{3}},
		{"list 1, 2, 3", []float64{1, 2, 3}},
		// a range: the second bound is checked too, without a sign
		{"between 1-5 testers", []float64{1, 5}},
		{"the ict-2 station", nil},
		// zh-Hant writes numbers next to the characters, without spaces
		// (review H1): a Han character never makes a digit part of an
		// identifier.
		{"等待時間由47.5分鐘降至12分鐘。", []float64{47.5, 12}},
		{"吞吐量增加了999件", []float64{999}},
		{"利用率為88%", []float64{88}},
		{"第2站的利用率為0.95，等待-3分鐘", []float64{2, 0.95, -3}},
		{"由1至5台", []float64{1, 5}},
		// an ASCII identifier inside Chinese text is still an identifier
		{"測試機ict_tester2與節點node_01j的佇列q1", nil},
	}
	for _, c := range cases {
		got, _ := Numbers(c.text)
		if !slices.Equal(values(got), c.want) {
			t.Errorf("Numbers(%q) = %v, want %v", c.text, values(got), c.want)
		}
	}
	got, _ := Numbers("12.5% and 3.25")
	if len(got) != 2 || !got[0].Percent || got[0].Decimals != 1 || got[1].Percent || got[1].Decimals != 2 {
		t.Errorf("percent and decimals: %+v", got)
	}
}

// Numbers written in words or with non-ASCII digits cannot be checked, so
// they are reported (AI_SPEC §3: 「四十七」 is an unverified number), in
// either locale. Ordinary words that contain a numeral character are not.
func TestUnverifiableWritings(t *testing.T) {
	flagged := []string{
		"四十七", "等待約四十七分鐘", "二十", "一百", "三千", "兩萬", "百分之五十", "增加兩台", "三個瓶頸", "減少五分鐘",
		"提升三成", "一小時", "第二站", "壹佰", "forty-seven minutes", "Two testers", "a dozen boards", "zero failures",
		"twice as long", "half the queue", "one hundred units", "４７ minutes", "٤٧ minutes",
	}
	for _, text := range flagged {
		_, findings := Numbers(text)
		if len(findings) == 0 {
			t.Errorf("%q: no finding", text)
		}
		for _, f := range findings {
			if f.Reason != NumberWord && f.Reason != NonASCIIDigit {
				t.Errorf("%q: finding %+v", text, f)
			}
		}
	}
	clean := []string{
		"進一步模擬", "結果一致", "唯一的瓶頸", "一個瓶頸", "一台測試機", "一般而言", "十分明顯", "兩者都", "萬一失敗",
		"千萬不要", "逐一檢查", "one bottleneck", "no one", "someone", "network", "often", "tone", "Seventeenth-century",
	}
	for _, text := range clean {
		if _, findings := Numbers(text); len(findings) != 0 {
			t.Errorf("%q: unexpected findings %+v", text, findings)
		}
	}
}

// Every Arabic number in prose must be one of the supplied values, as
// written (rounded to the decimals shown) or as a percentage of a ratio.
func TestCheckProse(t *testing.T) {
	supplied := []float64{47.5123, 12, 0.125, -3.5, 1234.5}
	ok := []string{
		"Wait fell from 47.5 to 12 minutes.",
		"等待時間由 47.51 分鐘降至 12 分鐘，減少 3.5。",
		"等待時間由47.51分鐘降至12分鐘，減少3.5，相對變化12.5%。",
		"Relative change 12.5%.",
		"Throughput 1,234.5 units; about 48 minutes before.",
		"No numbers here.",
	}
	for _, text := range ok {
		if f := CheckProse(text, supplied); len(f) != 0 {
			t.Errorf("%q: %+v", text, f)
		}
	}
	bad := map[string]Reason{
		"Wait fell to 11 minutes.":    Unverified,
		"Wait was 47.6 minutes.":      Unverified,
		"An improvement of 30%.":      Unverified,
		"等待約四十七分鐘。":                   NumberWord,
		"Forty-seven minutes of wait": NumberWord,
		"等待 ４７ 分鐘":                    NonASCIIDigit,
		"Wait 47.5123456 minutes.":    Unverified, // more digits than supplied is not the supplied value
		// review H1: unspaced zh-Hant numbers are checked too
		"等待時間由99分鐘降至3分鐘。": Unverified,
		"吞吐量增加了999件":      Unverified,
		"利用率為88%":         Unverified,
	}
	for text, want := range bad {
		f := CheckProse(text, supplied)
		if len(f) == 0 || !slices.ContainsFunc(f, func(x Finding) bool { return x.Reason == want }) {
			t.Errorf("%q: findings %+v, want one %s", text, f, want)
		}
	}
	if f := CheckProse("12 minutes", nil); len(f) != 1 || f[0].Text != "12" || f[0].Offset != 0 {
		t.Errorf("nothing supplied: %+v", f)
	}
}

// T-030 review M1: a number that starts with its decimal point is a number.
func TestLeadingDotNumbers(t *testing.T) {
	nums, _ := Numbers("yield fell to .97, then .5% of v1.97 and node.97")
	if len(nums) != 2 || nums[0].Value != 0.97 || nums[0].Decimals != 2 || nums[1].Value != 0.5 || !nums[1].Percent {
		t.Fatalf("%+v", nums)
	}
	if got := CheckProse("yield fell to .91", []float64{0.97}); len(got) != 1 {
		t.Errorf("an unsupplied .91 passed: %+v", got)
	}
	if got := CheckProse("yield fell to .97", []float64{0.97}); len(got) != 0 {
		t.Errorf("a supplied .97 was refused: %+v", got)
	}
}
