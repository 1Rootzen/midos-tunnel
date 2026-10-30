package main

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// The panel's page, scripts, styles, fonts and images are compiled into the
// binary. An Iran server often cannot reach a CDN, so nothing is loaded from
// outside. panelweb is the built single-page app (see web/, built with Vite);
// "all:" keeps chunk files whose names start with an underscore.
//
//go:embed all:panelweb
var panelFiles embed.FS

// staticFile is one embedded file, ready to send: its bytes, a gzip copy when
// that is worth having, and how long a browser may keep it.
type staticFile struct {
	data      []byte
	gz        []byte
	ctype     string
	immutable bool
}

// compressible lists the types worth gzipping; images and woff2 are already
// compressed.
var compressible = map[string]bool{".js": true, ".css": true, ".html": true, ".svg": true, ".json": true, ".txt": true}

func init() {
	// not every system's mime table knows these
	_ = mime.AddExtensionType(".woff2", "font/woff2")
	_ = mime.AddExtensionType(".webp", "image/webp")
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
	_ = mime.AddExtensionType(".js", "text/javascript; charset=utf-8")
}

// loadStatic reads every embedded file once, at start-up, and gzips the text
// ones, so serving costs nothing per request.
func loadStatic() map[string]*staticFile {
	sub, err := fs.Sub(panelFiles, "panelweb")
	if err != nil {
		panic(err)
	}
	files := map[string]*staticFile{}
	_ = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		ext := path.Ext(p)
		f := &staticFile{data: data, ctype: mime.TypeByExtension(ext)}
		if f.ctype == "" {
			f.ctype = http.DetectContentType(data)
		}
		// Vite names everything under assets/ by its content hash, so a file
		// there never changes under the same name and can be cached for good
		f.immutable = strings.HasPrefix(p, "assets/")
		if compressible[ext] && len(data) > 512 {
			var b bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
			_, _ = zw.Write(data)
			_ = zw.Close()
			if b.Len() < len(data)*9/10 {
				f.gz = b.Bytes()
			}
		}
		files["/"+p] = f
		return nil
	})
	return files
}

func panelStatic() http.Handler {
	files := loadStatic()
	// index.html and the few unhashed files (favicon) are revalidated on every
	// load against the running version, so an upgrade shows at once
	etag := `"` + version + `"`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "" || p == "/" {
			p = "/index.html"
		}
		f, ok := files[p]
		if !ok {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", f.ctype)
		if f.immutable {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
			h.Set("ETag", etag)
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		body := f.data
		if f.gz != nil {
			h.Add("Vary", "Accept-Encoding")
			if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				h.Set("Content-Encoding", "gzip")
				body = f.gz
			}
		}
		h.Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(body)
	})
}
