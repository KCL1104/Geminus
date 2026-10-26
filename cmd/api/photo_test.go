package main

import (
	"bytes"
	"encoding/json/v2"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/api"
	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/auth/authtest"
	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/tenant"
)

// The T-056 follow-up (D-124), through the handler run serves
// (handlerOptions over config.LoadAPIAI): a photo is refused up front, 503
// with details[0].code AI_IMAGE_UNSUPPORTED, exactly when the photo path is
// off, and a proposal in words is decided on its own. Each request names a
// model that does not exist, so an accepted path answers 404 after its AI
// check and nothing is stored either way. On the disposable database as the
// API login (skipped without it: NOT RUN).
func TestTheServedHandlerDecidesPhotosOnTheirOwn(t *testing.T) {
	admin := dbtest.Admin(t)
	p := authtest.New(t)
	v, err := auth.NewOIDCVerifier(p.Config())
	if err != nil {
		t.Fatal(err)
	}
	pool := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 4, true)
	org := dbtest.NewOrg(t, admin, p.Issuer, tenant.Editor)
	token := p.Token(t, p.RSA, nil, p.Claims(org.Members[tenant.Editor].Subject, time.Now()))
	fs, err := objects.NewFS(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	test := []string{"APP_ENV", "test"}
	for name, c := range map[string]struct {
		env           []string
		photo, detail string // the photo's error code and details[0].code
		words         string // the proposal in words' error code
	}{
		"the fake, its photo path disabled": {append(slices.Clone(test), "AI_PROVIDER", "fake", "AI_VISION_PROVIDER", "disabled"),
			"AI_PROVIDER_NOT_CONFIGURED", "AI_IMAGE_UNSUPPORTED", "NOT_FOUND"},
		"the fake for both": {append(slices.Clone(test), "AI_PROVIDER", "fake"), "NOT_FOUND", "", "NOT_FOUND"},
		"disabled, the fake photo path": {append(slices.Clone(test), "AI_VISION_PROVIDER", "fake"), "NOT_FOUND", "",
			"AI_PROVIDER_NOT_CONFIGURED"},
		"anthropic live: no image approved": {append(slices.Clone(test), apiClaude...), "AI_POLICY_NOT_APPROVED", "AI_IMAGE_UNSUPPORTED", "NOT_FOUND"},
		"anthropic live, meta for photos":   {append(slices.Clone(test), apiClaudeMetaPhotos...), "NOT_FOUND", "", "NOT_FOUND"},
		"nothing set":                       {test, "AI_PROVIDER_NOT_CONFIGURED", "AI_IMAGE_UNSUPPORTED", "AI_PROVIDER_NOT_CONFIGURED"},
	} {
		ai, err := config.LoadAPIAI(with(c.env...))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		h, err := api.Handler(slog.New(slog.NewTextHandler(io.Discard, nil)), v, tenant.New(pool), handlerOptions(ai, fs)...)
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(h)
		model, version := ids.New("mdl"), ids.New("mv")
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		_ = w.WriteField("base_version_id", version)
		fw, _ := w.CreateFormFile("file", "line.png")
		_, _ = fw.Write([]byte("\x89PNG\r\n\x1a\nnot decoded before the base is checked"))
		_ = w.Close()
		photo := send(t, srv, "/v1/models/"+model+"/ai-proposals/image", w.FormDataContentType(), body.String(), token, org.ID, ids.New("key"))
		words := send(t, srv, "/v1/models/"+model+"/ai-proposals", "application/json",
			`{"base_version_id":"`+version+`","mode":"edit","intent":"raise a capacity"}`, token, org.ID, ids.New("key"))
		srv.Close()
		if photo.code != c.photo || (c.detail == "") != (len(photo.details) == 0) || c.detail != "" && photo.details[0] != c.detail ||
			(photo.status == 503) != strings.HasPrefix(c.photo, "AI_") {
			t.Errorf("%s: the photo %d %s %v, want %s %q", name, photo.status, photo.code, photo.details, c.photo, c.detail)
		}
		if words.code != c.words || len(words.details) != 0 {
			t.Errorf("%s: in words %d %s %v, want %s", name, words.status, words.code, words.details, c.words)
		}
	}
	var n int
	if err := admin.QueryRow(t.Context(), `SELECT count(*) FROM twin.ai_proposals WHERE organization_id=$1`, org.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d proposals stored: %v", n, err)
	}
}

type answer struct {
	status  int
	code    string
	details []string
}

// send POSTs body as the token's subject in org and decodes the error.
func send(t *testing.T, srv *httptest.Server, path, contentType, body, token, org, key string) answer {
	t.Helper()
	req, err := http.NewRequest("POST", srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Organization-ID", org)
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("Content-Type", contentType)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Details []struct {
				Code string `json:"code"`
			} `json:"details"`
		} `json:"error"`
	}
	data, _ := io.ReadAll(res.Body)
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("%s: %d %s", path, res.StatusCode, data)
	}
	a := answer{status: res.StatusCode, code: e.Error.Code}
	for _, d := range e.Error.Details {
		a.details = append(a.details, d.Code)
	}
	return a
}
