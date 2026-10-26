package provider

import (
	"context"
	"testing"
)

// T-056: the client says whether it would hand a prompt's images to its
// backend, as call decides: only an ImageInput that accepts them.
func TestTheClientSaysWhetherItTakesImages(t *testing.T) {
	plain := backendFunc(func(context.Context, Request) (Response, error) { return Response{}, nil })
	for name, c := range map[string]struct {
		b    Backend
		want bool
	}{
		"a backend that takes images":   {&imageBackend{accepts: true}, true},
		"a backend that refuses images": {&imageBackend{accepts: false}, false},
		"a text backend":                {plain, false},
		"disabled":                      {Disabled{Code: CodeNotConfigured}, false},
	} {
		if got := client(t, c.b).AcceptsImages(); got != c.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}
