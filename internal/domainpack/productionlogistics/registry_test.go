package productionlogistics

import (
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/domainpack"
)

// zzSecond is a second pack declared in this package: the architecture
// rules cannot tell it from this pack, so registering both is refused.
type zzSecond struct{ Pack }

func (zzSecond) ID() string { return "zz_second" }

func TestRegistration(t *testing.T) {
	r, err := domainpack.NewRegistry(Pack{})
	if err != nil {
		t.Fatal(err)
	}
	if p, err := r.Lookup(ID); err != nil || p.ID() != ID {
		t.Fatalf("lookup: %v %v", p, err)
	}
	if _, err := domainpack.NewRegistry(&Pack{}); err != nil {
		t.Errorf("a pointer to the pack: %v", err)
	}
	if _, err := domainpack.NewRegistry(Pack{}, zzSecond{}); err == nil || !strings.Contains(err.Error(), "one package") {
		t.Errorf("two packs of one package: %v", err)
	}
	if _, ok := domainpack.DomainPack(Pack{}).(domainpack.TemplateProvider); !ok {
		t.Error("the pack offers no templates")
	}
}
