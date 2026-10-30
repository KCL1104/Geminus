package proposals

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"mime/multipart"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/schemas"
)

// H-049: the person's choice of model for a proposal. These are the
// refusals and the job's payload without a database; the DB tests of
// internal/api run the requests end to end.

// ProposalModels is the contract's closed list (common.schema.json
// ProposalModel), in its order.
func TestProposalModelsAreTheContracts(t *testing.T) {
	data, err := schemas.FS.ReadFile("common.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs map[string]struct {
			Enum []string `json:"enum"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if got := doc.Defs["ProposalModel"].Enum; !slices.Equal(got, ProposalModels) || !slices.Equal(got, []string{"muse-spark-1.3", "muse-wonder-1.0"}) {
		t.Errorf("the contract lists %q, the service %q", got, ProposalModels)
	}
}

// A model this deployment's approval does not list is 503
// AI_POLICY_NOT_APPROVED with AI_MODEL_NOT_APPROVED at /model (for a request
// in words and for a photo), the body of openapi.yaml's createProposal 503
// example; the job's code is the same, shown as AI_POLICY_NOT_APPROVED.
func TestModelRefusal(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("..", "..", "openapi", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, photo := range []bool{false, true} {
		var he *httpapi.Error
		ue := &UnavailableError{Availability: PolicyNotApproved, Model: true, Photo: photo}
		if !errors.As(Refused(ue), &he) || he.Code != httpapi.AIPolicyNotApproved || len(he.Details) != 1 || he.Details[0].Code != CodeModelNotApproved ||
			he.Details[0].Path != "/model" {
			t.Errorf("photo %v: %+v", photo, Refused(ue))
			continue
		}
		if !strings.Contains(ue.Error(), CodeModelNotApproved) {
			t.Errorf("photo %v: %q", photo, ue.Error())
		}
		for _, line := range []string{"code: " + string(he.Code), "message: " + he.Message, "- path: " + he.Details[0].Path,
			"reason: " + he.Details[0].Reason, "code: " + he.Details[0].Code} {
			if !strings.Contains(string(spec), "\n                      "+line+"\n") && !strings.Contains(string(spec), "\n                        "+line+"\n") {
				t.Errorf("openapi.yaml's createProposal 503 example has no %q", line)
			}
		}
	}
	if CodeModelNotApproved != "AI_"+strings.ToUpper(string(provider.CodeModelNotApproved)) {
		t.Error("the API's code is not the job's")
	}
	if errorCodes[CodeModelNotApproved] != "AI_POLICY_NOT_APPROVED" {
		t.Error("a job refused for its model is not shown as AI_POLICY_NOT_APPROVED")
	}
	s := &Service{Models: []string{"muse-spark-1.3", "muse-wonder-1.0"}, PhotoModels: []string{"muse-spark-1.3"}}
	for _, c := range []struct {
		model string
		photo bool
		want  bool
	}{
		{"", false, true}, {"", true, true}, {"muse-wonder-1.0", false, true}, {"muse-spark-1.3", true, true},
		{"muse-wonder-1.0", true, false}, {"muse-spark-1.3-contributor", false, false},
	} {
		err := s.checkModel(c.model, c.photo)
		var ue *UnavailableError
		if (err == nil) != c.want || (err != nil && (!errors.As(err, &ue) || !ue.Model || ue.Availability != PolicyNotApproved)) {
			t.Errorf("%q (photo %v): %v", c.model, c.photo, err)
		}
	}
	if err := (&Service{}).checkModel("muse-spark-1.3", false); err == nil {
		t.Error("a service without a model choice takes one")
	}
}

// The job carries the model the person chose (absent: the deployment's),
// beside research; its attempts are the research rule's alone.
func TestTheJobCarriesTheModel(t *testing.T) {
	const org, id, req = "org_01M4YDQK00Y5H2929M9WWZWTVH", "aip_01M4YDQK00ADN261CME3WV5HZ6", "req_01M4YDQK00ADN261CME3WV5HZ6"
	j := proposalJob(org, id, req, false, "muse-wonder-1.0")
	if j.Payload["model"] != "muse-wonder-1.0" || j.MaxAttempts != 0 {
		t.Errorf("job %+v", j)
	}
	j = proposalJob(org, id, req, true, "muse-wonder-1.0")
	if j.Payload["model"] != "muse-wonder-1.0" || j.Payload["research"] != true || j.MaxAttempts != ResearchJobAttempts {
		t.Errorf("research job %+v", j)
	}
	if j := proposalJob(org, id, req, false, ""); len(j.Payload) != 2 {
		t.Errorf("a job without a model: %+v", j.Payload)
	}
}

// A photo's model is a field before the file: one of the contract's models
// or absent; anything else is 422 at /model. The fingerprint names it only
// when it is sent, so a request without it has the fingerprint it had.
func TestThePhotoModelField(t *testing.T) {
	read := func(fields map[string]string, order ...string) (ImageFields, error) {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		for _, k := range order {
			_ = w.WriteField(k, fields[k])
		}
		fw, _ := w.CreateFormFile("file", "line.png")
		_, _ = fw.Write([]byte("not read here"))
		_ = w.Close()
		f, _, err := imageParts(multipart.NewReader(&body, w.Boundary()))
		return f, err
	}
	f, err := read(map[string]string{"base_version_id": "mv_01M4YDQK00ADN261CME3WV5HZ6", "model": "muse-wonder-1.0"}, "base_version_id", "model")
	if err != nil || f.Model != "muse-wonder-1.0" {
		t.Fatalf("%+v %v", f, err)
	}
	if _, err := f.check(); err != nil {
		t.Errorf("check: %v", err)
	}
	bad := ImageFields{BaseVersionID: "mv_01M4YDQK00ADN261CME3WV5HZ6", Model: "muse-spark-1.3-contributor"}
	var re *RequestError
	if _, err := bad.check(); !errors.As(err, &re) || len(re.Issues) != 1 || re.Issues[0].Path != "/model" {
		t.Errorf("an unknown model: %v", err)
	}
	if _, err := read(map[string]string{"base_version_id": "mv_01M4YDQK00ADN261CME3WV5HZ6", "model": "a"}, "base_version_id", "model", "model"); err == nil {
		t.Error("the model sent twice")
	}
	without := ImageFields{BaseVersionID: "mv_01M4YDQK00ADN261CME3WV5HZ6"}
	with := without
	with.Model = "muse-spark-1.3"
	a, _ := imageFingerprint("mdl_01M4YDQK00ADN261CME3WV5HZ6", without, locale.English, []byte("x"))
	b, _ := imageFingerprint("mdl_01M4YDQK00ADN261CME3WV5HZ6", with, locale.English, []byte("x"))
	// The fingerprint of a request without a model, as it was before H-049.
	before, err := hashing.Of([]byte(`{"base_version_id":"mv_01M4YDQK00ADN261CME3WV5HZ6",` +
		`"file_sha256":"sha256:2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881","file_size":1,` +
		`"model_id":"mdl_01M4YDQK00ADN261CME3WV5HZ6","output_locale":"en"}`))
	if err != nil {
		t.Fatal(err)
	}
	if a == b || a != before {
		t.Errorf("fingerprints %s %s, before %s", a, b, before)
	}
}
