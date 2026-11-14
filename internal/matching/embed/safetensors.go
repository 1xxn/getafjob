package embed

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

// table is the one 2-D tensor of a static model file, as float16.
type table struct {
	rows, cols int
	data       []uint16
}

// maxHeader bounds the JSON header of a safetensors file.
const maxHeader = 1 << 20

// readTable reads the single 2-D tensor of a safetensors file (F32 or F16) as float16, keeping the first dims
// columns when dims is between 0 and the width.
func readTable(r io.Reader, dims int) (table, error) {
	r = bufio.NewReaderSize(r, 1<<20)
	var n uint64
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return table{}, err
	}
	if n == 0 || n > maxHeader {
		return table{}, errors.New("safetensors: bad header size")
	}
	head := make([]byte, n)
	if _, err := io.ReadFull(r, head); err != nil {
		return table{}, err
	}
	var tensors map[string]json.RawMessage
	if err := json.Unmarshal(head, &tensors); err != nil {
		return table{}, err
	}
	var info struct {
		Dtype   string   `json:"dtype"`
		Shape   []int    `json:"shape"`
		Offsets [2]int64 `json:"data_offsets"`
	}
	found := 0
	for name, raw := range tensors {
		if name == "__metadata__" {
			continue
		}
		found++
		if err := json.Unmarshal(raw, &info); err != nil {
			return table{}, err
		}
	}
	if found != 1 || len(info.Shape) != 2 || info.Offsets[0] != 0 {
		return table{}, errors.New("safetensors: want one 2-D tensor")
	}
	rows, cols := info.Shape[0], info.Shape[1]
	size := map[string]int{"F32": 4, "F16": 2}[info.Dtype]
	if size == 0 {
		return table{}, fmt.Errorf("safetensors: dtype %s", info.Dtype)
	}
	if rows <= 0 || cols <= 0 || int64(rows)*int64(cols)*int64(size) != info.Offsets[1] {
		return table{}, errors.New("safetensors: shape and size disagree")
	}
	keep := cols
	if dims > 0 && dims < cols {
		keep = dims
	}
	data := make([]uint16, rows*keep)
	row := make([]byte, cols*size)
	for i := range rows {
		if _, err := io.ReadFull(r, row); err != nil {
			return table{}, err
		}
		for j := range keep {
			if size == 4 {
				data[i*keep+j] = floatToHalf(math.Float32frombits(binary.LittleEndian.Uint32(row[j*4:])))
			} else {
				data[i*keep+j] = binary.LittleEndian.Uint16(row[j*2:])
			}
		}
	}
	return table{rows: rows, cols: keep, data: data}, nil
}

// writeTable writes t as the single F16 tensor "embedding.weight" of a safetensors file.
func writeTable(w io.Writer, t table) error {
	head, err := json.Marshal(map[string]any{"embedding.weight": map[string]any{
		"dtype": "F16", "shape": []int{t.rows, t.cols}, "data_offsets": []int{0, len(t.data) * 2}}})
	if err != nil {
		return err
	}
	for len(head)%8 != 0 { // the data starts 8-byte aligned
		head = append(head, ' ')
	}
	bw := bufio.NewWriterSize(w, 1<<20)
	if err := binary.Write(bw, binary.LittleEndian, uint64(len(head))); err != nil {
		return err
	}
	if _, err := bw.Write(head); err != nil {
		return err
	}
	if err := binary.Write(bw, binary.LittleEndian, t.data); err != nil {
		return err
	}
	return bw.Flush()
}

// Pack writes the model in from (tokenizer.json, model.safetensors) to to, cut to dims columns and stored as
// float16: the form the image ships.
func Pack(from, to string, dims int) error {
	src, err := os.OpenRoot(from)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	tok, err := src.ReadFile("tokenizer.json")
	if err != nil {
		return err
	}
	f, err := src.Open("model.safetensors")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	t, err := readTable(f, dims)
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	if err := os.MkdirAll(to, 0o755); err != nil { //nolint:gosec // build tool output, read by everyone in the image
		return err
	}
	dst, err := os.OpenRoot(filepath.Clean(to))
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	if err := dst.WriteFile("tokenizer.json", tok, 0o644); err != nil {
		return err
	}
	out, err := dst.Create("model.safetensors")
	if err != nil {
		return err
	}
	if err := writeTable(out, t); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// halfToFloat maps every float16 bit pattern to its float32 value (256 KB, built once).
var halfToFloat = func() (t [1 << 16]float32) {
	for h := range t {
		t[h] = half(uint16(h))
	}
	return t
}()

// half converts an IEEE 754 half-precision number.
func half(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := uint32(h>>10) & 0x1f
	frac := uint32(h) & 0x3ff
	switch exp {
	case 0:
		f := float32(frac) * (1.0 / (1 << 24)) // subnormal: frac * 2^-24
		if sign != 0 {
			f = -f
		}
		return f
	case 0x1f:
		return math.Float32frombits(sign | 0x7f800000 | frac<<13)
	}
	return math.Float32frombits(sign | (exp+112)<<23 | frac<<13)
}

// floatToHalf rounds f to the nearest float16 (ties to even); out of range becomes infinity, tiny values zero or
// subnormal.
func floatToHalf(f float32) uint16 {
	b := math.Float32bits(f)
	sign := uint16(b>>16) & 0x8000
	exp := int32(b>>23&0xff) - 127 + 15
	mant := b & 0x7fffff
	switch {
	case b&0x7fffffff == 0:
		return sign
	case int32(b>>23&0xff) == 0xff: // inf or NaN
		if mant != 0 {
			return sign | 0x7e00
		}
		return sign | 0x7c00
	case exp >= 0x1f:
		return sign | 0x7c00
	case exp <= 0: // subnormal or zero
		if exp < -10 {
			return sign
		}
		mant |= 0x800000
		shift := uint32(14 - exp)
		h := mant >> shift
		rem := mant & (1<<shift - 1)
		half := uint32(1) << (shift - 1)
		if rem > half || rem == half && h&1 == 1 {
			h++
		}
		return sign | uint16(min(h, 0x400)) // 0x400: rounded up to the smallest normal, which is right
	}
	h := uint32(exp)<<10 | mant>>13
	rem := mant & 0x1fff
	if rem > 0x1000 || rem == 0x1000 && h&1 == 1 {
		h++ // may carry into the exponent, which is still right
	}
	return sign | uint16(min(h, 0x7c00)&0x7fff)
}

// Subset writes a model to to that knows only the tokens of texts, with the first dims columns as float16: a few
// hundred KB that tokenize and embed those texts exactly like the full model (tests). Only WordPiece models: the
// greedy longest match picks the same pieces when the vocabulary holds every piece it picked before.
func Subset(from, to string, texts []string, dims int) error {
	m, err := Load(from, dims)
	if err != nil {
		return err
	}
	if m.tok.kind != "WordPiece" {
		return errors.New("embed: subset needs a WordPiece model")
	}
	pieces := make(map[int]string, len(m.tok.ids))
	for p, id := range m.tok.ids {
		pieces[id] = p
	}
	used := map[int]int{} // old id -> new id
	var order []int
	for _, s := range texts {
		for _, id := range m.tok.Encode(s) {
			if _, ok := used[id]; !ok {
				used[id] = len(order)
				order = append(order, id)
			}
		}
	}
	vocab := make(map[string]int, len(order))
	t := table{rows: len(order), cols: m.dim, data: make([]uint16, len(order)*m.dim)}
	for n, id := range order {
		vocab[pieces[id]] = n
		copy(t.data[n*m.dim:(n+1)*m.dim], m.table[id*m.dim:(id+1)*m.dim])
	}
	tok, err := json.MarshalIndent(map[string]any{"model": map[string]any{"type": "WordPiece",
		"continuing_subword_prefix": m.tok.prefix, "max_input_chars_per_word": m.tok.maxWord, "vocab": vocab}}, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(to, 0o755); err != nil { //nolint:gosec // test data in the repository
		return err
	}
	dst, err := os.OpenRoot(filepath.Clean(to))
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	if err := dst.WriteFile("tokenizer.json", append(tok, '\n'), 0o644); err != nil {
		return err
	}
	out, err := dst.Create("model.safetensors")
	if err != nil {
		return err
	}
	if err := writeTable(out, t); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
