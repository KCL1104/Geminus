package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
)

const operator = "dt_test_provisioner_login"

func operatorURL(t *testing.T) string {
	t.Helper()
	admin := dbtest.Admin(t)
	if _, err := admin.Exec(context.Background(), `DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '`+operator+`') THEN
			CREATE ROLE `+operator+` LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB IN ROLE twin_provisioner;
		END IF;
		EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
		END $$`); err != nil {
		t.Fatal(err)
	}
	return dbtest.LoginURL(t, operator)
}

func provision(t *testing.T, url string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := map[string]string{"GEMINUS_PROVISION_DATABASE_URL": url}
	code := run(context.Background(), args, func(k string) string { return env[k] }, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// The operator creates a user and an organization; the output names their
// IDs and nothing else.
func TestProvision(t *testing.T) {
	url := operatorURL(t)
	subject := "sub-" + ids.New("x")
	code, out, errs := provision(t, url, "user", "-issuer", "https://test.invalid", "-subject", subject)
	var user struct {
		UserID  string `json:"user_id"`
		Created bool   `json:"created"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &user, json.RejectUnknownMembers(true)) != nil || !strings.HasPrefix(user.UserID, "usr_") || !user.Created {
		t.Fatalf("user: %d %q %q", code, out, errs)
	}
	code, out, errs = provision(t, url, "organization", "-name", "Acme", "-owner-issuer", "https://test.invalid", "-owner-subject", subject)
	var org struct {
		OrganizationID string `json:"organization_id"`
		Owner          string `json:"owner_user_id"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &org, json.RejectUnknownMembers(true)) != nil || !strings.HasPrefix(org.OrganizationID, "org_") || org.Owner != user.UserID {
		t.Errorf("organization: %d %q %q", code, out, errs)
	}
}

// A runtime login or a superuser is refused before anything is written;
// bad usage and input are exit 2.
func TestRefusals(t *testing.T) {
	url := operatorURL(t)
	for name, c := range map[string]struct {
		url  string
		args []string
		code int
	}{
		"API login":       {dbtest.LoginURL(t, dbtest.APILogin), []string{"user", "-issuer", "i", "-subject", "s"}, exitRefused},
		"superuser":       {dbtest.AdminURL(t), []string{"user", "-issuer", "i", "-subject", "s"}, exitRefused},
		"no command":      {url, nil, exitUsage},
		"unknown command": {url, []string{"delete"}, exitUsage},
		"extra argument":  {url, []string{"user", "-issuer", "i", "-subject", "s", "more"}, exitUsage},
		"empty subject":   {url, []string{"user", "-issuer", "i", "-subject", ""}, exitUsage},
		"no name":         {url, []string{"organization", "-owner-issuer", "i", "-owner-subject", "s"}, exitUsage},
		"no database":     {"", []string{"user", "-issuer", "i", "-subject", "s"}, exitUsage},
	} {
		if code, out, _ := provision(t, c.url, c.args...); code != c.code || out != "" {
			t.Errorf("%s: exit %d, output %q", name, code, out)
		}
	}
}

// The demo organization of the hackathon deployment gets the judge's account
// as an editor when it is created (T-057, H-035 (2)): the output names the
// member's ID too; a member's three flags go together, and a member is never
// an owner.
func TestProvisionWithMember(t *testing.T) {
	url := operatorURL(t)
	owner, judge := "sub-"+ids.New("x"), "judge-"+ids.New("x")
	code, out, errs := provision(t, url, "organization", "-name", "Demo line (synthetic)", "-owner-issuer", "https://test.invalid", "-owner-subject", owner,
		"-member-issuer", "https://test.invalid", "-member-subject", judge, "-member-role", "editor")
	var org struct {
		OrganizationID string `json:"organization_id"`
		Owner          string `json:"owner_user_id"`
		Member         string `json:"member_user_id"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &org, json.RejectUnknownMembers(true)) != nil || !strings.HasPrefix(org.OrganizationID, "org_") ||
		!strings.HasPrefix(org.Owner, "usr_") || !strings.HasPrefix(org.Member, "usr_") || org.Owner == org.Member {
		t.Fatalf("organization with a member: %d %q %q", code, out, errs)
	}
	code, out, _ = provision(t, url, "user", "-issuer", "https://test.invalid", "-subject", judge)
	if code != 0 || !strings.Contains(out, `"user_id":"`+org.Member+`"`) || !strings.Contains(out, `"created":false`) {
		t.Errorf("the member as a user: %d %q", code, out)
	}
	base := []string{"organization", "-name", "Refused", "-owner-issuer", "https://test.invalid", "-owner-subject", "sub-" + ids.New("x")}
	for name, extra := range map[string][]string{
		"a member without a role":    {"-member-issuer", "https://test.invalid", "-member-subject", "judge-" + ids.New("x")},
		"a member without a subject": {"-member-issuer", "https://test.invalid", "-member-role", "editor"},
		"a member without an issuer": {"-member-subject", "judge-" + ids.New("x"), "-member-role", "editor"},
		"a role alone":               {"-member-role", "editor"},
		"a member as a second owner": {"-member-issuer", "https://test.invalid", "-member-subject", "judge-" + ids.New("x"), "-member-role", "owner"},
		"an unknown role":            {"-member-issuer", "https://test.invalid", "-member-subject", "judge-" + ids.New("x"), "-member-role", "admin"},
	} {
		if code, out, _ := provision(t, url, append(append([]string{}, base...), extra...)...); code != exitUsage || out != "" {
			t.Errorf("%s: exit %d, output %q", name, code, out)
		}
	}
	if code, out, _ := provision(t, url, "user", "-issuer", "https://test.invalid", "-subject", "s", "-member-role", "editor"); code != exitUsage || out != "" {
		t.Errorf("a member flag on user: exit %d, output %q", code, out)
	}
}
