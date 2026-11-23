package cvimport

import (
	"encoding/hex"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/ledongthuc/pdf"
)

const (
	maxCMapEntries = 65536
	maxRangeSpan   = 4096
	maxPDFGlyphs   = 4 * MaxText
)

type cmapRange struct {
	lo, hi string
}

// toUnicodeCMap maps raw font codes to UTF-8 strings from a font's /ToUnicode CMap (ISO 32000-1 §9.10.2).
type toUnicodeCMap struct {
	space   [4][]cmapRange
	mapping [4]map[string]string
}

type fontDecoder struct {
	font     pdf.Font
	hasFont  bool
	baseName string
	toUni    *toUnicodeCMap
	fallback pdf.TextEncoding
	// names are the glyph names of the font's /Differences; they decode codes the ToUnicode CMap lacks, such as
	// the "fi" ligature of LaTeX fonts (/uniFB01).
	names map[int]string
	// icon marks a symbol font (Font Awesome, Marvosym, ...): its glyphs are pictures, not text.
	icon bool
}

// iconFonts are base font names whose glyphs are icons; LaTeX CVs put them before e-mail, phone and profile links.
var iconFonts = []string{"fontawesome", "academicons", "marvosym", "fontello", "materialicons", "octicons", "ifsym"}

func iconFont(base string) bool {
	b := strings.ToLower(base)
	for _, f := range iconFonts {
		if strings.Contains(b, f) {
			return true
		}
	}
	return false
}

// ligatureGlyphs are the glyph names of ligatures that fonts use without a uniXXXX name.
var ligatureGlyphs = map[string]string{
	"fi": "fi", "fl": "fl", "ff": "ff", "ffi": "ffi", "ffl": "ffl", "f_i": "fi", "f_l": "fl", "f_f": "ff",
	"f_f_i": "ffi", "f_f_l": "ffl", "f_t": "ft", "s_t": "st",
}

// glyphText reads the text of a glyph name: uniXXXX (one or more code points), uXXXX[XX] and ligatures.
func glyphText(name string) (string, bool) {
	if s, ok := ligatureGlyphs[name]; ok {
		return s, true
	}
	if hexs, ok := strings.CutPrefix(name, "uni"); ok && len(hexs) >= 4 && len(hexs)%4 == 0 {
		var out []rune
		for i := 0; i < len(hexs); i += 4 {
			b, err := hex.DecodeString(hexs[i : i+4])
			if err != nil {
				return "", false
			}
			out = append(out, rune(b[0])<<8|rune(b[1]))
		}
		return string(out), true
	}
	if hexs, ok := strings.CutPrefix(name, "u"); ok && len(hexs) >= 4 && len(hexs) <= 6 {
		r, err := strconv.ParseUint(hexs, 16, 32)
		if err != nil || strings.ToUpper(hexs) != hexs || r > unicode.MaxRune {
			return "", false
		}
		return string(rune(r)), true
	}
	return "", false
}

// differences reads the glyph names of a font's /Encoding /Differences array (code, then names from that code).
func differences(f pdf.Font) map[int]string {
	enc := f.V.Key("Encoding")
	if enc.Kind() != pdf.Dict {
		return nil
	}
	diff := enc.Key("Differences")
	if diff.Kind() != pdf.Array {
		return nil
	}
	out := map[int]string{}
	code := 0
	for i := 0; i < diff.Len() && i < 1024; i++ {
		v := diff.Index(i)
		switch v.Kind() {
		case pdf.Integer:
			code = int(v.Int64())
		case pdf.Name:
			if code >= 0 && code < 256 {
				out[code] = v.Name()
			}
			code++
		case pdf.Null, pdf.Bool, pdf.Real, pdf.String, pdf.Dict, pdf.Array, pdf.Stream:
		}
	}
	return out
}

func newFontDecoder(p pdf.Page, name string) *fontDecoder {
	f := p.Font(name)
	if f.V.IsNull() {
		return &fontDecoder{}
	}
	base := f.BaseFont()
	if i := strings.IndexByte(base, '+'); i >= 0 {
		base = base[i+1:]
	}
	var tu *toUnicodeCMap
	if s := f.V.Key("ToUnicode"); s.Kind() == pdf.Stream {
		tu = parseToUnicodeCMap(s)
	}
	return &fontDecoder{
		font:     f,
		hasFont:  true,
		baseName: base,
		toUni:    tu,
		fallback: f.Encoder(),
		names:    differences(f),
		icon:     iconFont(base),
	}
}

// named decodes a one-byte code by its glyph name when that name says what text it is.
func (d *fontDecoder) named(code int) (string, bool) {
	if name, ok := d.names[code]; ok {
		return glyphText(name)
	}
	return "", false
}

func (d *fontDecoder) width(code int) float64 {
	if !d.hasFont {
		return 0
	}
	return d.font.Width(code)
}

type glyphPiece struct {
	code int
	str  string
}

func (d *fontDecoder) decode(raw string) []glyphPiece {
	if raw == "" {
		return nil
	}
	if d.icon { // icons separate items like a space; their codes would read as stray letters
		out := bytePieces(raw)
		for i := range out {
			out[i].str = " "
		}
		return out
	}
	if d.toUni == nil {
		if d.fallback == nil {
			return bytePieces(raw)
		}
		decoded := d.fallback.Decode(raw)
		if len(decoded) == len(raw) {
			out := make([]glyphPiece, len(raw))
			n := 0
			for _, r := range decoded {
				out[n] = glyphPiece{code: int(raw[n]), str: string(r)}
				if s, ok := d.named(int(raw[n])); ok {
					out[n].str = s
				}
				n++
			}
			return out
		}
		out := make([]glyphPiece, 0, len(decoded))
		n := 0
		for _, r := range decoded {
			code := 0
			if n < len(raw) {
				code = int(raw[n])
			}
			n++
			out = append(out, glyphPiece{code: code, str: string(r)})
		}
		return out
	}

	var out []glyphPiece
	for len(raw) > 0 {
		matched := false
		for n := 1; n <= 4 && n <= len(raw); n++ {
			seg := raw[:n]
			if !inCMapSpace(d.toUni.space[n-1], seg) {
				continue
			}
			code := int(beUint(seg))
			if s, ok := d.toUni.mapping[n-1][seg]; ok {
				out = append(out, glyphPiece{code: code, str: s})
			} else if s, ok := d.named(code); ok && n == 1 {
				out = append(out, glyphPiece{code: code, str: s})
			} else if d.fallback != nil {
				out = append(out, glyphPiece{code: code, str: d.fallback.Decode(seg)})
			} else {
				out = append(out, glyphPiece{code: code, str: seg})
			}
			raw = raw[n:]
			matched = true
			break
		}
		if !matched {
			b := raw[:1]
			s := b
			if d.fallback != nil {
				s = d.fallback.Decode(b)
			}
			out = append(out, glyphPiece{code: int(b[0]), str: s})
			raw = raw[1:]
		}
	}
	return out
}

func bytePieces(raw string) []glyphPiece {
	out := make([]glyphPiece, len(raw))
	for i := 0; i < len(raw); i++ {
		out[i] = glyphPiece{code: int(raw[i]), str: raw[i : i+1]}
	}
	return out
}

func inCMapSpace(ranges []cmapRange, seg string) bool {
	for _, r := range ranges {
		if r.lo <= seg && seg <= r.hi {
			return true
		}
	}
	return false
}

func parseToUnicodeCMap(strm pdf.Value) *toUnicodeCMap {
	rc := strm.Reader()
	if rc == nil {
		return nil
	}
	defer func() { _ = rc.Close() }()
	raw, err := io.ReadAll(io.LimitReader(rc, 2<<20))
	if err != nil || len(raw) == 0 {
		return nil
	}

	var m toUnicodeCMap
	entries := 0
	toks := tokenizeCMap(raw)
	for i := 0; i < len(toks); i++ {
		switch toks[i].word {
		case "begincodespacerange":
			i++
			var pair [2][]byte
			pidx := 0
			for i < len(toks) && toks[i].word != "endcodespacerange" {
				if toks[i].hex != nil {
					pair[pidx] = toks[i].hex
					pidx++
					if pidx == 2 {
						pidx = 0
						lo, hi := string(pair[0]), string(pair[1])
						if len(lo) >= 1 && len(lo) <= 4 && len(lo) == len(hi) {
							m.space[len(lo)-1] = append(m.space[len(lo)-1], cmapRange{lo: lo, hi: hi})
						}
					}
				}
				i++
			}
		case "beginbfchar":
			i++
			var pair [2][]byte
			pidx := 0
			for i < len(toks) && toks[i].word != "endbfchar" {
				if toks[i].hex != nil && entries < maxCMapEntries {
					pair[pidx] = toks[i].hex
					pidx++
					if pidx == 2 {
						pidx = 0
						orig, repl := string(pair[0]), string(pair[1])
						if k := len(orig); k >= 1 && k <= 4 {
							putCMap(&m, k-1, orig, utf16BEDecode(repl))
							entries++
						}
					}
				}
				i++
			}
		case "beginbfrange":
			i++
			for i < len(toks) && toks[i].word != "endbfrange" {
				if toks[i].hex == nil {
					i++
					continue
				}
				loBytes := toks[i].hex
				i++
				for i < len(toks) && toks[i].word != "endbfrange" && toks[i].hex == nil {
					i++
				}
				if i >= len(toks) || toks[i].word == "endbfrange" {
					break
				}
				hiBytes := toks[i].hex
				i++
				if i >= len(toks) || toks[i].word == "endbfrange" {
					break
				}
				lo, hi := string(loBytes), string(hiBytes)
				k := len(lo)
				span := uint32(0)
				if k >= 1 && k <= 4 && k == len(hi) && lo <= hi {
					span = beUint(hi) - beUint(lo)
					if span > maxRangeSpan {
						span = maxRangeSpan
					}
				}
				if toks[i].word == "[" {
					i++
					var off uint32
					for i < len(toks) && toks[i].word != "]" && toks[i].word != "endbfrange" {
						if toks[i].hex != nil && k >= 1 && k <= 4 && k == len(hi) && lo <= hi && off <= span && entries < maxCMapEntries {
							putCMap(&m, k-1, addCodeOffset(lo, off), utf16BEDecode(string(toks[i].hex)))
							entries++
							off++
						}
						i++
					}
					continue
				}
				if toks[i].hex != nil && k >= 1 && k <= 4 && k == len(hi) && lo <= hi {
					base := string(toks[i].hex)
					for off := uint32(0); off <= span && entries < maxCMapEntries; off++ {
						putCMap(&m, k-1, addCodeOffset(lo, off), utf16BEDecode(addCodeOffset(base, off)))
						entries++
					}
				}
			}
		}
	}
	return &m
}

type cmapTok struct {
	word string
	hex  []byte
}

func tokenizeCMap(b []byte) []cmapTok {
	var out []cmapTok
	n := len(b)
	for i := 0; i < n; {
		c := b[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' {
			i++
			continue
		}
		if c == '%' {
			for i < n && b[i] != '\n' && b[i] != '\r' {
				i++
			}
			continue
		}
		if c == '<' && (i+1 >= n || b[i+1] != '<') {
			i++
			var hb []byte
			for i < n && b[i] != '>' {
				ch := b[i]
				if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') {
					hb = append(hb, ch)
				}
				i++
			}
			if i < n && b[i] == '>' {
				i++
			}
			if len(hb)%2 == 1 {
				hb = append(hb, '0')
			}
			if dec := make([]byte, len(hb)/2); len(dec) > 0 {
				if _, err := hex.Decode(dec, hb); err == nil {
					out = append(out, cmapTok{hex: dec})
					continue
				}
			}
			out = append(out, cmapTok{hex: []byte{}})
			continue
		}
		if c == '[' || c == ']' {
			out = append(out, cmapTok{word: string(c)})
			i++
			continue
		}
		if c == '<' || c == '>' {
			if i+1 < n && b[i+1] == c {
				i += 2
			} else {
				i++
			}
			continue
		}
		if c == '(' {
			i++
			depth := 1
			for i < n && depth > 0 {
				if b[i] == '\\' {
					i += 2
					continue
				}
				switch b[i] {
				case '(':
					depth++
				case ')':
					depth--
				}
				i++
			}
			continue
		}
		start := i
		for i < n {
			ch := b[i]
			if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' || ch == '\f' ||
				ch == '<' || ch == '>' || ch == '[' || ch == ']' || ch == '(' || ch == '%' {
				break
			}
			i++
		}
		if i > start {
			out = append(out, cmapTok{word: string(b[start:i])})
		}
	}
	return out
}

func putCMap(m *toUnicodeCMap, idx int, code, val string) {
	if val == "" {
		return
	}
	if m.mapping[idx] == nil {
		m.mapping[idx] = make(map[string]string)
	}
	m.mapping[idx][code] = val
}

func utf16BEDecode(raw string) string {
	if len(raw) == 0 {
		return ""
	}
	if len(raw) == 1 {
		return string(rune(raw[0]))
	}
	u := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		u = append(u, uint16(raw[i])<<8|uint16(raw[i+1]))
	}
	return string(utf16.Decode(u))
}

func beUint(s string) uint32 {
	var v uint32
	for i := 0; i < len(s) && i < 4; i++ {
		v = v<<8 | uint32(s[i])
	}
	return v
}

func addCodeOffset(s string, off uint32) string {
	b := []byte(s)
	carry := off
	for i := len(b) - 1; i >= 0 && carry > 0; i-- {
		sum := uint32(b[i]) + (carry & 0xff)
		b[i] = byte(sum & 0xff)
		carry = (carry >> 8) + (sum >> 8)
	}
	return string(b)
}

type pdfMatrix [3][3]float64

var pdfIdent = pdfMatrix{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}

func (x pdfMatrix) mul(y pdfMatrix) pdfMatrix {
	var z pdfMatrix
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			for k := 0; k < 3; k++ {
				z[i][j] += x[i][k] * y[k][j]
			}
		}
	}
	return z
}

type pdfGState struct {
	Tc    float64
	Tw    float64
	Th    float64
	Tl    float64
	Tf    *fontDecoder
	Tfs   float64
	Trise float64
	Tm    pdfMatrix
	Tlm   pdfMatrix
	CTM   pdfMatrix
}

func popAllArgs(stk *pdf.Stack) []pdf.Value {
	n := stk.Len()
	args := make([]pdf.Value, n)
	for i := n - 1; i >= 0; i-- {
		args[i] = stk.Pop()
	}
	return args
}

// extractPDFPage reads glyphs from p while giving /ToUnicode CMaps priority over /Encoding dictionaries.
func extractPDFPage(p pdf.Page) []pdf.Text {
	if p.V.IsNull() || p.V.Key("Contents").Kind() == pdf.Null {
		return nil
	}
	strm := p.V.Key("Contents")
	decoders := make(map[string]*fontDecoder)
	g := pdfGState{
		Th:  1,
		Tf:  &fontDecoder{},
		CTM: pdfIdent,
		Tm:  pdfIdent,
		Tlm: pdfIdent,
	}
	var out []pdf.Text
	var gstack []pdfGState

	showText := func(raw string) {
		if len(out) >= maxPDFGlyphs || raw == "" {
			return
		}
		for _, piece := range g.Tf.decode(raw) {
			if len(out) >= maxPDFGlyphs {
				return
			}
			w0 := g.Tf.width(piece.code)
			trm := pdfMatrix{{g.Tfs * g.Th, 0, 0}, {0, g.Tfs, 0}, {0, g.Trise, 1}}.mul(g.Tm).mul(g.CTM)
			wPts := w0 / 1000 * trm[0][0]
			out = append(out, pdf.Text{
				Font:     g.Tf.baseName,
				FontSize: trm[0][0],
				X:        trm[2][0],
				Y:        trm[2][1],
				W:        wPts,
				S:        piece.str,
			})
			tx := (w0/1000*g.Tfs + g.Tc) * g.Th
			g.Tm = pdfMatrix{{1, 0, 0}, {0, 1, 0}, {tx, 0, 1}}.mul(g.Tm)
		}
	}

	pdf.Interpret(strm, func(stk *pdf.Stack, op string) {
		args := popAllArgs(stk)
		switch op {
		case "cm":
			if len(args) != 6 {
				return
			}
			var m pdfMatrix
			for i := 0; i < 6; i++ {
				m[i/2][i%2] = args[i].Float64()
			}
			m[2][2] = 1
			g.CTM = m.mul(g.CTM)
		case "q":
			if len(gstack) < 64 {
				gstack = append(gstack, g)
			}
		case "Q":
			if n := len(gstack) - 1; n >= 0 {
				g = gstack[n]
				gstack = gstack[:n]
			}
		case "BT":
			g.Tm = pdfIdent
			g.Tlm = g.Tm
		case "T*":
			x := pdfMatrix{{1, 0, 0}, {0, 1, 0}, {0, -g.Tl, 1}}
			g.Tlm = x.mul(g.Tlm)
			g.Tm = g.Tlm
		case "Tc":
			if len(args) == 1 {
				g.Tc = args[0].Float64()
			}
		case "Tw":
			if len(args) == 1 {
				g.Tw = args[0].Float64()
			}
		case "Tz":
			if len(args) == 1 {
				g.Th = args[0].Float64() / 100
			}
		case "TL":
			if len(args) == 1 {
				g.Tl = args[0].Float64()
			}
		case "Ts":
			if len(args) == 1 {
				g.Trise = args[0].Float64()
			}
		case "TD":
			if len(args) != 2 {
				return
			}
			g.Tl = -args[1].Float64()
			x := pdfMatrix{{1, 0, 0}, {0, 1, 0}, {args[0].Float64(), args[1].Float64(), 1}}
			g.Tlm = x.mul(g.Tlm)
			g.Tm = g.Tlm
		case "Td":
			if len(args) != 2 {
				return
			}
			x := pdfMatrix{{1, 0, 0}, {0, 1, 0}, {args[0].Float64(), args[1].Float64(), 1}}
			g.Tlm = x.mul(g.Tlm)
			g.Tm = g.Tlm
		case "Tf":
			if len(args) != 2 {
				return
			}
			fname := args[0].Name()
			dec, ok := decoders[fname]
			if !ok {
				dec = newFontDecoder(p, fname)
				decoders[fname] = dec
			}
			g.Tf = dec
			g.Tfs = args[1].Float64()
		case "Tm":
			if len(args) != 6 {
				return
			}
			var m pdfMatrix
			for i := 0; i < 6; i++ {
				m[i/2][i%2] = args[i].Float64()
			}
			m[2][2] = 1
			g.Tm = m
			g.Tlm = m
		case "\"":
			if len(args) != 3 {
				return
			}
			g.Tw = args[0].Float64()
			g.Tc = args[1].Float64()
			x := pdfMatrix{{1, 0, 0}, {0, 1, 0}, {0, -g.Tl, 1}}
			g.Tlm = x.mul(g.Tlm)
			g.Tm = g.Tlm
			showText(args[2].RawString())
		case "'":
			if len(args) != 1 {
				return
			}
			x := pdfMatrix{{1, 0, 0}, {0, 1, 0}, {0, -g.Tl, 1}}
			g.Tlm = x.mul(g.Tlm)
			g.Tm = g.Tlm
			showText(args[0].RawString())
		case "Tj":
			if len(args) == 1 {
				showText(args[0].RawString())
			}
		case "TJ":
			if len(args) != 1 {
				return
			}
			v := args[0]
			for i := 0; i < v.Len(); i++ {
				el := v.Index(i)
				if el.Kind() == pdf.String {
					showText(el.RawString())
				} else {
					tx := -el.Float64() / 1000 * g.Tfs * g.Th
					g.Tm = pdfMatrix{{1, 0, 0}, {0, 1, 0}, {tx, 0, 1}}.mul(g.Tm)
				}
			}
		}
	})
	return out
}
