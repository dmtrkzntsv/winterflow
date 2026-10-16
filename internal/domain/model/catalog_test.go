package model

import "testing"

func TestCatalogMetaEqualAndNormalized(t *testing.T) {
	a := CatalogMeta{Summary: "s", Keywords: []string{"k"}}
	n := a.Normalized()
	if n.Tags == nil || n.AlternativeTo == nil || len(n.Keywords) != 1 || a.Tags != nil {
		t.Fatalf("normalized = %+v (from %+v)", n, a)
	}
	if !a.Equal(n) {
		t.Fatal("a nil list differs from an empty one")
	}
	for _, b := range []CatalogMeta{
		{Summary: "t", Keywords: []string{"k"}},
		{Summary: "s", Keywords: []string{"k", "l"}},
		{Summary: "s", Keywords: []string{"k"}, LogoURL: "https://x.test/l.png"},
	} {
		if a.Equal(b) {
			t.Errorf("%+v equals %+v", a, b)
		}
	}
}
