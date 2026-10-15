package model_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/model"
)

// NewIssue output always fits common.schema.json ValidationIssue.
func TestNewIssueFitsTheSchema(t *testing.T) {
	long := strings.Repeat("é", model.MaxIssueMessage+5)
	i := model.NewIssue("X", "/a", long, model.SeverityError)
	if n := utf8.RuneCountInString(i.Message); n != model.MaxIssueMessage || !strings.HasSuffix(i.Message, "…") {
		t.Errorf("message has %d characters", n)
	}
	i = model.NewIssue("X", "/a/\xff", "bad \xff byte", model.SeverityError)
	if !utf8.ValidString(i.Message) || !utf8.ValidString(i.Path) || i.Path != "/a/�" {
		t.Errorf("invalid UTF-8 kept: %q %q", i.Path, i.Message)
	}
	path := "/metadata/" + strings.Repeat("k", 1500) + "/" + strings.Repeat("k", 1500)
	i = model.NewIssue("X", path, "m", model.SeverityError)
	if i.Path != "/metadata/"+strings.Repeat("k", 1500) {
		t.Errorf("long path not shortened to its ancestor: %d characters", utf8.RuneCountInString(i.Path))
	}
	if ok := model.NewIssue("X", "/nodes/0", "m", model.SeverityWarning); ok != (model.ValidationIssue{Code: "X", Path: "/nodes/0", Message: "m", Severity: model.SeverityWarning}) {
		t.Errorf("short issue changed: %+v", ok)
	}
}
