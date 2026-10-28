// Package locale is the AI output language (REQ-022, AI_SPEC §3): the
// server validates the requested output_locale, states it in the provider
// instruction with fixed text, and checks that the numbers in analyst prose
// are written as Arabic numerals that match supplied values (AI_SPEC §9), in
// either language. Only prose follows the locale; IDs, JSON Patch paths, JSON
// Pointers, enum and reason codes, metric paths and units are never
// translated.
package locale

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Locale is an output language (common.schema.json#/$defs/Locale).
type Locale string

const (
	English            Locale = "en"
	TraditionalChinese Locale = "zh-Hant"
	// Default is the locale of a request that names none.
	Default = English
)

// All lists the locales, the default first, in the schema's order.
var All = []Locale{English, TraditionalChinese}

// ErrUnsupported: the request names a locale outside All.
var ErrUnsupported = errors.New("locale: unsupported output_locale")

// Error is a rejected output_locale; Path is the JSON Pointer of the
// request member. It never quotes the client's value.
type Error struct{ Path string }

func (e *Error) Error() string {
	return ErrUnsupported.Error() + " at " + e.Path + ` (allowed: "en", "zh-Hant")`
}
func (e *Error) Unwrap() error { return ErrUnsupported }

// Parse validates a request's output_locale: absent (empty) is the
// default, otherwise it must be spelled exactly as the contract spells it.
func Parse(s string) (Locale, error) {
	if s == "" {
		return Default, nil
	}
	for _, l := range All {
		if s == string(l) {
			return l, nil
		}
	}
	return "", &Error{Path: "/output_locale"}
}

const keepUntranslated = " Never translate, transliterate or reformat IDs, JSON Patch paths, JSON Pointers, schema member names, " +
	"enum values, reason or error codes, metric paths or units: copy them exactly as supplied. Write every number with " +
	"Arabic numerals (0-9) and the unit of the supplied value, never as number words (write 47, not forty-seven or 四十七), " +
	"and only numbers that appear in the supplied data."

// Instruction is the fixed provider instruction for l. An unknown value
// gets the default's: the instruction never carries client text.
func (l Locale) Instruction() string {
	switch l {
	case TraditionalChinese:
		return "OUTPUT_LOCALE: zh-Hant. Write every prose field (summaries, assumptions, expected effects, questions, " +
			"statements, hypotheses, evidence, reasons, uncertainty) in Traditional Chinese (繁體中文), not Simplified Chinese." + keepUntranslated
	default:
		return "OUTPUT_LOCALE: en. Write every prose field (summaries, assumptions, expected effects, questions, " +
			"statements, hypotheses, evidence, reasons, uncertainty) in English." + keepUntranslated
	}
}

// Number is an Arabic numeral in prose.
type Number struct {
	Text     string // as written, e.g. "1,234.5" or "12.5%"
	Value    float64
	Percent  bool // followed by %
	Decimals int  // digits after the decimal point, as written
	Offset   int  // byte offset in the text
}

// Reason classifies a Finding.
type Reason string

const (
	// Unverified: an Arabic number that matches no supplied value.
	Unverified Reason = "unverified"
	// NumberWord: a number written in words (四十七, forty-seven), which
	// cannot be checked against supplied values.
	NumberWord Reason = "number_word"
	// NonASCIIDigit: digits other than 0-9 (full-width, Arabic-Indic...).
	NonASCIIDigit Reason = "non_ascii_digit"
)

// Finding is a number in prose that could not be verified.
type Finding struct {
	Text   string
	Offset int
	Reason Reason
}

// number: optional sign, digits with optional thousands groups, optional
// fraction and exponent, optional percent sign.
var number = regexp.MustCompile(`[-+−]?(?:\d{1,3}(?:,\d{3})+|\d+)(?:\.\d+)?(?:[eE][-+]?\d+)?%?`)

// identifierRune reports whether r continues an identifier or path, so a
// digit after it belongs to that token (q1, v2, node_01j, /metrics/x1).
// Only ASCII counts: IDs, paths and codes are ASCII (TWIN_IR_SPEC, D-001),
// while zh-Hant prose writes numbers right after Han characters
// (「降至12分鐘」), which are letters to unicode.IsLetter (T-024 review H1).
func identifierRune(r rune) bool {
	return r == '_' || r == '/' || r == '.' || r == ':' || r == '#' || r == '-' || asciiLetter(r) || asciiDigit(r)
}

func asciiLetter(r rune) bool {
	return r < utf8.RuneSelf && ('a' <= r && r <= 'z' || 'A' <= r && r <= 'Z')
}
func asciiDigit(r rune) bool { return '0' <= r && r <= '9' }

// Numbers returns the Arabic numbers in text and the number writings that
// cannot be verified. Digits inside identifiers and paths are skipped.
func Numbers(text string) ([]Number, []Finding) {
	var nums []Number
	for _, loc := range number.FindAllStringIndex(text, -1) {
		start, end := loc[0], loc[1]
		raw := text[start:end]
		sign := ""
		if strings.HasPrefix(raw, "−") {
			sign, raw = "-", raw[len("−"):]
		} else if raw[0] == '-' || raw[0] == '+' {
			sign, raw = raw[:1], raw[1:]
		}
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		// T-030 review M1: ".97" is the number 0.97 when the dot starts it
		// (after a space or the start), not a part of an identifier or path.
		if start > 0 && before == '.' && sign == "" {
			if prev, _ := utf8.DecodeLastRuneInString(text[:start-1]); start == 1 || !identifierRune(prev) {
				start, raw, before = start-1, "0."+raw, ' '
			}
		}
		switch {
		case start > 0 && sign != "" && asciiDigit(before):
			// A range or difference, "1-5": the second number has no sign.
			start += len(text[start:end]) - len(raw)
			sign = ""
		case start > 0 && identifierRune(before) && (sign == "" || asciiLetter(before) || before == '_'):
			// Part of an identifier or path: q1, node_01j, ict-2.
			continue
		}
		pct := strings.HasSuffix(raw, "%")
		body := strings.ReplaceAll(strings.TrimSuffix(raw, "%"), ",", "")
		v, err := strconv.ParseFloat(body, 64)
		if err != nil || math.IsInf(v, 0) {
			continue
		}
		if sign == "-" {
			v = -v
		}
		dec := 0
		if mant, _, _ := strings.Cut(strings.ToLower(body), "e"); strings.Contains(mant, ".") {
			dec = len(mant) - strings.Index(mant, ".") - 1
		}
		nums = append(nums, Number{Text: text[start:end], Value: v, Percent: pct, Decimals: dec, Offset: start})
	}
	return nums, unverifiable(text)
}

var (
	// English cardinal words that state a quantity. "one" is left out: it
	// is mostly an article or pronoun ("one bottleneck", "no one").
	numberWords = regexp.MustCompile(`(?i)\b(?:zero|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|` +
		`fifteen|sixteen|seventeen|eighteen|nineteen|twenty|thirty|forty|fifty|sixty|seventy|eighty|ninety|hundred|` +
		`thousand|million|billion|trillion|dozen|twice|thrice|half)\b`)
	// Chinese numerals: digits and multipliers.
	zhDigits      = "零〇一二三四五六七八九兩两壹貳贰參叁肆伍陸陆柒捌玖"
	zhMultipliers = "十百千萬万億亿拾佰仟"
	// Units and classifiers that make a single numeral a quantity.
	zhTimeUnits = []string{"分鐘", "分钟", "小時", "小时", "秒", "天", "日", "週", "周", "星期", "倍", "成", "%", "％"}
	zhCounters  = []string{"個", "个", "台", "臺", "部", "名", "位", "人", "件", "次", "片", "站", "條", "条", "批", "組", "组", "班", "項", "项"}
	// Words made of numeral characters that are not numbers.
	zhIdioms = []string{"萬一", "万一", "一一"}
)

func isZhNumeral(r rune) bool {
	return strings.ContainsRune(zhDigits, r) || strings.ContainsRune(zhMultipliers, r)
}

func hasPrefixAny(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// unverifiable finds number words (English and Chinese) and non-ASCII
// digits. The Chinese rules: a run of two or more numeral characters
// (四十七, 兩萬) unless it is an idiom (萬一, 千萬不要); 百分之; and a single
// digit followed by a unit or classifier (兩台, 五分鐘), where 一 counts only
// before a unit (一小時), since 一個／一台 work as an article.
func unverifiable(text string) []Finding {
	var out []Finding
	for _, loc := range numberWords.FindAllStringIndex(text, -1) {
		out = append(out, Finding{Text: text[loc[0]:loc[1]], Offset: loc[0], Reason: NumberWord})
	}
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		switch {
		case r > unicode.MaxASCII && unicode.IsDigit(r):
			j := i
			for j < len(text) {
				d, s := utf8.DecodeRuneInString(text[j:])
				if !unicode.IsDigit(d) {
					break
				}
				j += s
			}
			out = append(out, Finding{Text: text[i:j], Offset: i, Reason: NonASCIIDigit})
			i = j
			continue
		case strings.HasPrefix(text[i:], "百分之"):
			out = append(out, Finding{Text: "百分之", Offset: i, Reason: NumberWord})
			i += len("百分之")
			continue
		case isZhNumeral(r):
			j, n := i, 0
			for j < len(text) {
				d, s := utf8.DecodeRuneInString(text[j:])
				if !isZhNumeral(d) {
					break
				}
				j += s
				n++
			}
			run, rest := text[i:j], text[j:]
			switch {
			case n >= 2:
				idiom := false
				for _, w := range zhIdioms {
					idiom = idiom || run == w
				}
				if (run == "千萬" || run == "千万") && hasPrefixAny(rest, []string{"不", "別", "别", "要"}) {
					idiom = true
				}
				if !idiom {
					out = append(out, Finding{Text: run, Offset: i, Reason: NumberWord})
				}
			case strings.ContainsRune(zhDigits, r):
				if hasPrefixAny(rest, zhTimeUnits) || (r != '一' && hasPrefixAny(rest, zhCounters)) {
					out = append(out, Finding{Text: run, Offset: i, Reason: NumberWord})
				}
			}
			i = j
			continue
		}
		i += size
	}
	return out
}

// matches reports whether the prose number n states v: v rounded to the
// decimals n shows (a sign may be carried by the words around it), or, for
// a percentage, 100·v so rounded. The sign is ignored ("fell by 3.5"
// states -3.5), so whether a delta's direction is right is not checked
// here: T-030's analyst checks compare it with the comparison.
func matches(n Number, v float64) bool {
	tol := 0.5*math.Pow(10, -float64(n.Decimals)) + 1e-9*math.Max(1, math.Abs(v))
	if math.Abs(math.Abs(v)-math.Abs(n.Value)) <= tol {
		return true
	}
	return n.Percent && math.Abs(math.Abs(100*v)-math.Abs(n.Value)) <= tol
}

// CheckProse reports every number in text that cannot be verified against
// supplied: number words, non-ASCII digits, and Arabic numbers that match
// none of the supplied values.
func CheckProse(text string, supplied []float64) []Finding {
	nums, out := Numbers(text)
	for _, n := range nums {
		ok := false
		for _, v := range supplied {
			if matches(n, v) {
				ok = true
				break
			}
		}
		if !ok {
			out = append(out, Finding{Text: n.Text, Offset: n.Offset, Reason: Unverified})
		}
	}
	return out
}
