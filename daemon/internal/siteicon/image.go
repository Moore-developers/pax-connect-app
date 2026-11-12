// Package siteicon owns bounded website icon decoding and private cache lifetime.
// It accepts anonymous bytes from an injected network boundary, never routes or proto.
package siteicon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"golang.org/x/net/idna"
	"image"
	_ "image/jpeg"
	"image/png"
	"net/netip"
	"strings"
)

var ErrTarget = errors.New("icon_target_disallowed")
var ErrImage = errors.New("icon_invalid_image")

// Host accepts only public DNS-shaped hostnames, never URLs or literal addresses.
func Host(raw string) (string, error) {
	host := strings.ToLower(strings.TrimSuffix(raw, "."))
	if len(host) == 0 || len(host) > 253 || strings.ContainsAny(host, "/:@?#\\\x00 \t\r\n") {
		return "", ErrTarget
	}
	host, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", ErrTarget
	}
	if _, e := netip.ParseAddr(host); e == nil {
		return "", ErrTarget
	}
	if !strings.Contains(host, ".") {
		return "", ErrTarget
	}
	for _, suffix := range []string{"localhost", "local", "internal", "home", "lan", "test", "invalid", "onion"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return "", ErrTarget
		}
	}
	for _, part := range strings.Split(host, ".") {
		if len(part) == 0 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return "", ErrTarget
		}
		for _, c := range part {
			if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' {
				continue
			}
			return "", ErrTarget
		}
	}
	return host, nil
}

// PNG validates dimensions before decoding and strips metadata into a 64px PNG.
func PNG(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return nil, ErrImage
	}
	if len(raw) >= 6 && binary.LittleEndian.Uint16(raw[:2]) == 0 && binary.LittleEndian.Uint16(raw[2:4]) == 1 {
		count := int(binary.LittleEndian.Uint16(raw[4:6]))
		if count == 0 || count > 256 || 6+count*16 > len(raw) {
			return nil, ErrImage
		}
		best := []byte(nil)
		area := 0
		for i := 0; i < count; i++ {
			entry := raw[6+i*16:]
			w, h := int(entry[0]), int(entry[1])
			if w == 0 {
				w = 256
			}
			if h == 0 {
				h = 256
			}
			n, o := uint64(binary.LittleEndian.Uint32(entry[8:12])), uint64(binary.LittleEndian.Uint32(entry[12:16]))
			if o+n > uint64(len(raw)) || n == 0 {
				continue
			}
			if w*h > area {
				best = raw[o : o+n]
				area = w * h
			}
		}
		if best == nil {
			return nil, ErrImage
		}
		if bytes.HasPrefix(best, []byte("\x89PNG\r\n\x1a\n")) {
			raw = best
		} else {
			// ICO DIB: header height includes the AND mask. Decode a bounded BMP copy.
			if len(best) < 40 || binary.LittleEndian.Uint32(best[:4]) != 40 {
				return nil, ErrImage
			}
			w, h := int32(binary.LittleEndian.Uint32(best[4:8])), int32(binary.LittleEndian.Uint32(best[8:12]))/2
			bits := binary.LittleEndian.Uint16(best[14:16])
			if w <= 0 || h <= 0 || w > 1024 || h > 1024 || bits != 32 {
				return nil, ErrImage
			}
			dib := append([]byte(nil), best...)
			binary.LittleEndian.PutUint32(dib[8:12], uint32(h))
			file := make([]byte, 14)
			copy(file, "BM")
			binary.LittleEndian.PutUint32(file[2:6], uint32(len(dib)+14))
			binary.LittleEndian.PutUint32(file[10:14], 54)
			file = append(file, dib...)
			img, err := bmp.Decode(bytes.NewReader(file))
			if err != nil {
				return nil, ErrImage
			}
			return encode(img)
		}
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 1024 || cfg.Height > 1024 {
		return nil, ErrImage
	}
	if format != "png" && format != "jpeg" && format != "webp" {
		return nil, ErrImage
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrImage
	}
	return encode(img)
}
func encode(img image.Image) ([]byte, error) {
	dst := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	bounds := img.Bounds()
	w, h := 64, 64
	if bounds.Dx() > bounds.Dy() {
		h = max(1, 64*bounds.Dy()/bounds.Dx())
	} else {
		w = max(1, 64*bounds.Dx()/bounds.Dy())
	}
	draw.CatmullRom.Scale(dst, image.Rect((64-w)/2, (64-h)/2, (64+w)/2, (64+h)/2), img, bounds, draw.Src, nil)
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, dst); err != nil {
		return nil, ErrImage
	}
	return buffer.Bytes(), nil
}
