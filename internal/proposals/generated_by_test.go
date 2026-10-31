package proposals

import (
	"encoding/json/v2"
	"reflect"
	"strings"
	"testing"
)

// H-043: the label of which provider and model wrote a result shows those
// and, for a photo, the segmenter and its object count; nothing else of the
// stored metadata leaves the server.
func TestGeneratedByShowsOnlyTheProviderModelAndSegmenter(t *testing.T) {
	photo := `{"provider":"meta","model":"muse-spark-1.3","credential_label":"meta-a","role":"proposal","prompt_template_version":"photo-0.2.0",
		"schema":"x","output_locale":"en","input_hash":"sha256:aa","usage":{},"latency_ms":9,"provider_request_id":"req_1","cost_nano_usd":120,
		"image":{"digest":"sha256:bb","media_type":"image/png","width":4,"height":4},
		"segmenter":{"provider":"meta","model":"sam-3.1","credential_label":"meta-a","role":"segment","image_digest":"sha256:bb","concepts":8,
		"images_billed":8,"cost_nano_usd":20000000,"latency_ms":40,"provider_request_ids":["r1"],"segments":12}}`
	got := FromMetadata([]byte(photo))
	want := GeneratedBy{Provider: "meta", Model: "muse-spark-1.3", Segmenter: &GeneratedSegmenter{Provider: "meta", Model: "sam-3.1", Objects: 12}}
	if got == nil || got.Provider != want.Provider || got.Model != want.Model || got.Segmenter == nil || !reflect.DeepEqual(*got.Segmenter, *want.Segmenter) {
		t.Fatalf("got %+v", got)
	}
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"credential", "meta-a", "cost", "req_1", "sha256", "latency", "image", "prompt"} {
		if strings.Contains(string(out), leak) {
			t.Errorf("%s leaks %q", out, leak)
		}
	}

	for name, c := range map[string]struct {
		metadata string
		want     *GeneratedBy
	}{
		"a generating row's {}":        {`{}`, nil},
		"nothing stored":               {``, nil},
		"not JSON":                     {`{`, nil},
		"a text proposal":              {`{"provider":"anthropic","model":"text-fallback-1"}`, &GeneratedBy{Provider: "anthropic", Model: "text-fallback-1"}},
		"a provider outside the shape": {`{"provider":"Meta AI","model":"m"}`, nil},
		"a model outside the shape":    {`{"provider":"meta","model":"muse spark <b>"}`, &GeneratedBy{Provider: "meta"}},
		"too many objects":             {`{"provider":"meta","segmenter":{"provider":"meta","model":"sam-3.1","segments":65}}`, &GeneratedBy{Provider: "meta"}},
		"negative objects":             {`{"provider":"meta","segmenter":{"provider":"meta","segments":-1}}`, &GeneratedBy{Provider: "meta"}},
	} {
		t.Run(name, func(t *testing.T) {
			got := FromMetadata([]byte(c.metadata))
			if (got == nil) != (c.want == nil) || (got != nil && (got.Provider != c.want.Provider || got.Model != c.want.Model || got.Segmenter != nil)) {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}
