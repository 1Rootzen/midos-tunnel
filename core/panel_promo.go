package main

// The sponsor banner shown across the top of the panel. Its slides are fetched
// from the project's promo server so the ads can be changed without shipping a
// new release; the panel caches them for an hour and, whenever the server can't
// be reached, falls back to the slides built in here so the banner is never
// empty.
//
// Midos Tunnel is free and open source. This banner is how the project is
// funded: the LICENSE asks anyone who redistributes the panel to keep it. It is
// rendered by the shell (web/src/components/PromoBanner.vue) as part of every
// page, not as something a logged-in operator can switch off.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// promoBase is the promo server the ads come from. Override at runtime with
// MIDOS_PROMO_BASE. Change the default to your own host/domain before a release.
const defaultPromoBase = "https://lic.unishare.net"

func promoBase() string {
	if v := os.Getenv("MIDOS_PROMO_BASE"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultPromoBase
}

// The promo server uses a self-signed certificate and serves only public ads, so
// this fetch does not verify it. Nothing sensitive is sent.
var promoClient = &http.Client{
	Timeout:   promoFetchWait,
	Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
}

// promoProbe fetches the current ads from the promo server. It is a variable so
// tests can stand in for the network.
var promoProbe = func() (promoDoc, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), promoFetchWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, promoBase()+"/promo.json", nil)
	if err != nil {
		return promoDoc{}, false
	}
	req.Header.Set("User-Agent", "midos-tunnel/"+version)
	resp, err := promoClient.Do(req)
	if err != nil {
		return promoDoc{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return promoDoc{}, false
	}
	var doc promoDoc
	if json.NewDecoder(resp.Body).Decode(&doc) != nil {
		return promoDoc{}, false
	}
	return doc, true
}

const (
	promoTTL       = time.Hour
	promoMaxImage  = 1 << 20 // an ad image larger than this is ignored (matches the promo server)
	promoFetchWait = 8 * time.Second
)

type promoSlide struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	CTA    string `json:"cta,omitempty"`    // the button label
	URL    string `json:"url,omitempty"`    // where the slide links to
	Image  string `json:"image,omitempty"`  // optional https image, proxied below
	BG     string `json:"bg,omitempty"`     // background color
	FG     string `json:"fg,omitempty"`     // text color
	Accent string `json:"accent,omitempty"` // headline color
}

type promoDoc struct {
	Slides   []promoSlide `json:"slides"`
	Interval int          `json:"interval"` // seconds each slide is shown
}

// builtinPromo is shown while the download server is unreachable. It keeps the
// banner populated on day one and on Iran servers that reach the server only
// intermittently. Replace the server-side promo.json to change what operators
// actually see.
var builtinPromo = promoDoc{
	Interval: 9,
	Slides: []promoSlide{{
		ID:   "midos",
		Text: "Midos Tunnel — تانل پرسرعت و پایدار ایران و خارج",
		BG:   "#1b2150",
		FG:   "#e6e8ff",
	}},
}

type promoCache struct {
	mu      sync.Mutex
	doc     promoDoc
	fetched time.Time
	ok      bool
}

var promo promoCache

// current returns the freshest promo, refreshing from the server at most once
// per TTL. A failed refresh keeps the last good copy, or the built-in slides.
func (p *promoCache) current() promoDoc {
	p.mu.Lock()
	fresh := p.ok && time.Since(p.fetched) < promoTTL
	cur := p.doc
	p.mu.Unlock()
	if fresh {
		return cur
	}

	doc, ok := promoProbe()
	if !ok || len(doc.Slides) == 0 {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.ok {
			return p.doc // a server hiccup keeps the last good ads
		}
		return builtinPromo
	}
	if doc.Interval < 4 {
		doc.Interval = 9
	}
	p.mu.Lock()
	p.doc, p.fetched, p.ok = doc, time.Now(), true
	p.mu.Unlock()
	return doc
}

// imageURL returns the image of the slide with this id, but only if it is one
// the server actually published, so the proxy below can't be aimed elsewhere.
func (p *promoCache) imageURL(id string) string {
	doc := p.current()
	for _, s := range doc.Slides {
		if s.ID == id && strings.HasPrefix(s.Image, "https://") {
			return s.Image
		}
	}
	return ""
}

func (s *panelServer) handlePromo(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, promo.current())
}

// handlePromoImage streams a slide's image through the panel, so the strict
// img-src 'self' content-security-policy still covers it. Only images named in
// the current promo are fetched.
func (s *panelServer) handlePromoImage(w http.ResponseWriter, r *http.Request) {
	url := promo.imageURL(r.PathValue("id"))
	if url == "" {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), promoFetchWait)
	defer cancel()
	body, err := httpGetBytes(ctx, url)
	if err != nil || len(body) == 0 || len(body) > promoMaxImage {
		http.NotFound(w, r)
		return
	}
	ct := http.DetectContentType(body)
	if !strings.HasPrefix(ct, "image/") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "max-age=3600")
	_, _ = w.Write(body)
}
