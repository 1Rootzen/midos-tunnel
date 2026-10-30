package main

import (
	"testing"
	"time"
)

// imageURL must only ever return an https URL that the server actually put in
// the current promo, so the image proxy can't be pointed at an internal host.
func TestPromoImageURLGuard(t *testing.T) {
	p := &promoCache{ok: true, doc: promoDoc{Slides: []promoSlide{
		{ID: "a", Image: "https://cdn.example.com/a.png"},
		{ID: "b", Image: "http://cdn.example.com/b.png"}, // not https
		{ID: "c"}, // no image
		{ID: "d", Image: "file:///etc/passwd"},
	}}}
	// so current() uses the preset doc instead of fetching
	p.fetched = time.Now()

	cases := map[string]string{
		"a": "https://cdn.example.com/a.png",
		"b": "", "c": "", "d": "",
		"missing": "",
	}
	for id, want := range cases {
		if got := p.imageURL(id); got != want {
			t.Errorf("imageURL(%q) = %q, want %q", id, got, want)
		}
	}
}

// With no good copy yet and the server unreachable, the banner falls back to the
// built-in slides rather than going blank.
func TestPromoBuiltinFallback(t *testing.T) {
	if len(builtinPromo.Slides) == 0 {
		t.Fatal("builtinPromo must keep at least one slide so the banner is never empty")
	}
	p := &promoCache{} // never fetched, not ok; current() will try and fail fast
	// point the fetch at an address that won't answer, then confirm the fallback
	old := promoProbe
	promoProbe = func() (promoDoc, bool) { return promoDoc{}, false }
	defer func() { promoProbe = old }()
	if got := p.current(); len(got.Slides) == 0 {
		t.Fatal("current() returned no slides on a failed fetch")
	}
}
