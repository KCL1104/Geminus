package migrations

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// The repository's migrations/ load as one gap-free sequence from 0001,
// each framed as one transaction or none (T-039): what the deployment's
// migration step applies is exactly these files, in this order.
func TestLoadTheRepositorysMigrations(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "migrations")
	ms, err := Load(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	ups, _ := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if len(ms) != len(ups) || len(ms) < 16 {
		t.Fatalf("%d migrations of %d up files", len(ms), len(ups))
	}
	for i, m := range ms {
		b, err := os.ReadFile(filepath.Join(dir, m.Name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		if m.Version != i+1 || m.SQL != string(b) || m.SHA256 != hex.EncodeToString(sum[:]) {
			t.Errorf("%d: %+v", i, m.Name)
		}
		// The record is written in the migration's own transaction, before
		// its COMMIT, and the file is otherwise run as it is.
		run := m.script()
		rec := m.record()
		if !strings.Contains(run, rec) || strings.Count(run, "COMMIT;") != 1 || !strings.HasSuffix(run, "COMMIT;\n") ||
			strings.Index(run, rec) > strings.LastIndex(run, "COMMIT;") {
			t.Errorf("%s: the record is not in the migration's transaction:\n...%s", m.Name, run[max(0, len(run)-300):])
		}
		if m.framed && !strings.HasPrefix(run, m.SQL[:m.commit]) || !m.framed && !strings.HasPrefix(run, "BEGIN;\n"+m.SQL) {
			t.Errorf("%s: the file is not run as it is", m.Name)
		}
	}
	if ms[2].Name != "0003_draft_issues.up.sql" || ms[2].framed {
		t.Errorf("0003 has no BEGIN/COMMIT of its own and is run in one: %+v", ms[2].framed)
	}
	if !ms[0].framed {
		t.Error("0001 is framed")
	}
}

// Load refuses what it cannot apply in order and in one transaction each:
// a gap, a duplicate number, a file that does not start at 0001, a
// transaction statement anywhere but the frame, and a framed file with
// something after its COMMIT; other files (down migrations, notes) are not
// migrations.
func TestLoadRefuses(t *testing.T) {
	framed := "-- c\nBEGIN;\nCREATE TABLE t(x int);\nCOMMIT;\n"
	ok := fstest.MapFS{
		"0001_a.up.sql":   {Data: []byte(framed)},
		"0001_a.down.sql": {Data: []byte("DROP TABLE t;")},
		"0002_b.up.sql":   {Data: []byte("ALTER TABLE t ADD y int;\n")},
		"README.md":       {Data: []byte("notes")},
	}
	ms, err := Load(ok)
	if err != nil || len(ms) != 2 || !ms[0].framed || ms[1].framed {
		t.Fatalf("%+v %v", ms, err)
	}
	if got := ms[1].script(); got != "BEGIN;\nALTER TABLE t ADD y int;\n\n"+ms[1].record()+"\nCOMMIT;\n" {
		t.Errorf("unframed script %q", got)
	}
	if got := ms[0].script(); got != "-- c\nBEGIN;\nCREATE TABLE t(x int);\n"+ms[0].record()+"\nCOMMIT;\n" {
		t.Errorf("framed script %q", got)
	}
	for name, files := range map[string]fstest.MapFS{
		"none":                     {},
		"a gap":                    {"0001_a.up.sql": ok["0001_a.up.sql"], "0003_c.up.sql": ok["0002_b.up.sql"]},
		"a duplicate number":       {"0001_a.up.sql": ok["0001_a.up.sql"], "0001_b.up.sql": ok["0002_b.up.sql"]},
		"not from 0001":            {"0002_b.up.sql": ok["0002_b.up.sql"]},
		"an odd up name":           {"0001_a.up.sql": ok["0001_a.up.sql"], "0002_B-x.up.sql": ok["0002_b.up.sql"]},
		"a COMMIT inside":          {"0001_a.up.sql": {Data: []byte("BEGIN;\nCREATE TABLE t(x int);\nCOMMIT;\nCREATE TABLE u(x int);\nCOMMIT;\n")}},
		"a ROLLBACK":               {"0001_a.up.sql": {Data: []byte("CREATE TABLE t(x int);\nrollback ;\n")}},
		"a START TRANSACTION":      {"0001_a.up.sql": {Data: []byte("START TRANSACTION;\nCREATE TABLE t(x int);\nCOMMIT;\n")}},
		"a BEGIN without COMMIT":   {"0001_a.up.sql": {Data: []byte("BEGIN;\nCREATE TABLE t(x int);\n")}},
		"a COMMIT without BEGIN":   {"0001_a.up.sql": {Data: []byte("CREATE TABLE t(x int);\nCOMMIT;\n")}},
		"a statement after COMMIT": {"0001_a.up.sql": {Data: []byte("BEGIN;\nCREATE TABLE t(x int);\nCOMMIT;\nSELECT 1;\n")}},
		"an empty file":            {"0001_a.up.sql": {Data: []byte("-- nothing\n")}},
		"not UTF-8":                {"0001_a.up.sql": {Data: []byte("BEGIN;\n\xff\nCOMMIT;\n")}},
	} {
		if ms, err := Load(files); !errors.Is(err, ErrInvalidMigration) {
			t.Errorf("%s: %v %v", name, ms, err)
		}
	}
	// PL/pgSQL's BEGIN and END inside a function body are not transaction
	// statements.
	body := fstest.MapFS{"0001_a.up.sql": {Data: []byte("BEGIN;\nCREATE FUNCTION f() RETURNS void LANGUAGE plpgsql AS $$\nBEGIN\n  RETURN;\nEND;\n$$;\nCOMMIT;\n")}}
	if _, err := Load(body); err != nil {
		t.Errorf("a function body: %v", err)
	}
}
