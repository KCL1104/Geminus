// Package coldstart holds the one test that must run in a process where no
// schema has been compiled yet, so it lives in its own test binary.
package coldstart

import (
	"os"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// The first requests a server sees arrive together; compiling schemas for
// them must not race (review of T-004: concurrent map writes in the compiler).
func TestColdStartConcurrent(t *testing.T) {
	doc, err := os.ReadFile("../../../../testdata/contracts/valid-minimal-ir.json")
	if err != nil {
		t.Fatal(err)
	}
	bad, err := os.ReadFile("../../../../testdata/contracts/semantic-probability-sum.json")
	if err != nil {
		t.Fatal(err)
	}
	p := []byte(`[{"op":"test","path":"/resources/0/id","value":"worker"},{"op":"replace","path":"/resources/0/capacity","value":3}]`)
	badPatch := []byte(`[{"op":"replace","path":"/resources/0/capacity","value":-1}]`)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 16 {
		wg.Go(func() {
			<-start
			switch i % 4 {
			case 0:
				if r, err := validate.IR(doc); err != nil || !r.Valid() {
					t.Errorf("IR: %v %+v", err, r)
				}
			case 1:
				if r, err := patch.Apply(doc, p, patch.Scenario); err != nil || !r.Valid() {
					t.Errorf("Apply: %v %+v", err, r)
				}
			case 2: // the error path renders messages concurrently too
				if r, err := validate.IR(bad); err != nil || r.Valid() {
					t.Errorf("invalid IR: %v %+v", err, r)
				}
			case 3:
				if r, err := patch.Apply(doc, badPatch, patch.Scenario); err != nil || r.Valid() {
					t.Errorf("invalid Apply: %v %+v", err, r)
				}
			}
		})
	}
	close(start)
	wg.Wait()
}
