package secret

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const value = "sk-ant-test-0123456789abcdef"

// A Secret never prints, marshals or logs its value, alone or inside a
// struct, with any verb.
func TestSecretNeverPrints(t *testing.T) {
	type holder struct {
		Label string
		Key   Secret
	}
	h := holder{Label: "org-a", Key: Secret(value)}
	var b strings.Builder
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%T"} {
		fmt.Fprintf(&b, verb+"\n", h)
		fmt.Fprintf(&b, verb+"\n", h.Key)
		fmt.Fprintf(&b, verb+"\n", []Secret{h.Key})
	}
	b.WriteString(fmt.Sprint(h.Key) + fmt.Sprintln(&h))
	j, err := json.Marshal(map[string]any{"h": h, "k": h.Key})
	if err != nil {
		t.Fatal(err)
	}
	b.Write(j)
	slog.New(slog.NewJSONHandler(&b, nil)).Info("x", "holder", h, "key", h.Key)
	slog.New(slog.NewTextHandler(&b, nil)).Info("x", "holder", h, "key", h.Key)
	out := b.String()
	for _, leak := range []string{value, "0123456789abcdef", "736b2d616e74", "736B2D616E74"} {
		if strings.Contains(out, leak) {
			t.Fatalf("the value is printed (%q):\n%s", leak, out)
		}
	}
	if !strings.Contains(out, redacted) || !strings.Contains(out, "org-a") {
		t.Errorf("the marker or the label is missing:\n%s", out)
	}
	if h.Key.Reveal() != value {
		t.Error("Reveal")
	}
}
