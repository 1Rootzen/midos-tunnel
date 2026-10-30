package main

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// captcha
// ---------------------------------------------------------------------------

// A small SVG captcha. The answer stays on the server with a short lifetime
// and is used once, so it really gates scripted guessing; the image only has
// to be awkward for a machine, not a work of art.

type captchaEntry struct {
	answer string
	exp    time.Time
}

const captchaAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func rnd(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		panic(err)
	}
	return int(v.Int64())
}

func (s *panelServer) handleCaptcha(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteByte(captchaAlphabet[rnd(len(captchaAlphabet))])
	}
	answer := b.String()
	id := randomToken(16)
	now := time.Now()
	s.mu.Lock()
	for k, v := range s.captchas {
		if now.After(v.exp) {
			delete(s.captchas, k)
		}
	}
	// a flood of captcha requests must not grow memory without bound
	if len(s.captchas) > 5000 {
		s.captchas = map[string]captchaEntry{}
	}
	s.captchas[id] = captchaEntry{answer: answer, exp: now.Add(5 * time.Minute)}
	s.mu.Unlock()
	jsonOK(w, map[string]string{"id": id, "svg": captchaSVG(answer)})
}

func (s *panelServer) checkCaptcha(id, answer string) bool {
	s.mu.Lock()
	e, ok := s.captchas[id]
	delete(s.captchas, id) // one attempt per image
	s.mu.Unlock()
	if !ok || time.Now().After(e.exp) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.ToUpper(strings.TrimSpace(answer))), []byte(e.answer)) == 1
}

func captchaSVG(answer string) string {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="180" height="56" viewBox="0 0 180 56">`)
	b.WriteString(`<rect width="180" height="56" rx="10" fill="#2a2724"/>`)
	for i := 0; i < 6; i++ {
		fmt.Fprintf(&b, `<path d="M%d %d Q %d %d %d %d" stroke="#3d45aa" stroke-opacity="0.35" stroke-width="%d" fill="none"/>`,
			rnd(40), rnd(56), 40+rnd(100), rnd(56), 140+rnd(40), rnd(56), 1+rnd(2))
	}
	for i, ch := range answer {
		x := 18 + i*31 + rnd(6)
		y := 36 + rnd(10)
		fmt.Fprintf(&b, `<text x="%d" y="%d" transform="rotate(%d %d %d)" font-family="monospace" font-size="%d" font-weight="700" fill="#f4ede6">%c</text>`,
			x, y, rnd(40)-20, x, y, 26+rnd(6), ch)
	}
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, `<circle cx="%d" cy="%d" r="1.2" fill="#3d45aa" fill-opacity="0.5"/>`, rnd(180), rnd(56))
	}
	b.WriteString(`</svg>`)
	return b.String()
}
