package embed

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeModel writes a tiny model: tokenizer.json and a safetensors table with one row per token.
func writeModel(t *testing.T, tokenizer any, rows [][]float32, dtype string) string {
	t.Helper()
	dir := t.TempDir()
	tb, err := json.Marshal(tokenizer)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokenizer.json"), tb, 0o600); err != nil {
		t.Fatal(err)
	}
	size := 4
	if dtype == "F16" {
		size = 2
	}
	cols := len(rows[0])
	var data bytes.Buffer
	for _, r := range rows {
		for _, x := range r {
			if size == 4 {
				_ = binary.Write(&data, binary.LittleEndian, math.Float32bits(x))
			} else {
				_ = binary.Write(&data, binary.LittleEndian, toHalf(x))
			}
		}
	}
	head, _ := json.Marshal(map[string]any{"embedding.weight": map[string]any{
		"dtype": dtype, "shape": []int{len(rows), cols}, "data_offsets": []int{0, data.Len()}}})
	var f bytes.Buffer
	_ = binary.Write(&f, binary.LittleEndian, uint64(len(head)))
	f.Write(head)
	f.Write(data.Bytes())
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), f.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// toHalf converts small exact values (tests only).
func toHalf(f float32) uint16 {
	b := math.Float32bits(f)
	sign := uint16(b>>16) & 0x8000
	if f == 0 {
		return sign
	}
	exp := int((b>>23)&0xff) - 127 + 15
	return sign | uint16(exp)<<10 | uint16((b>>13)&0x3ff)
}

var wordPiece = map[string]any{"model": map[string]any{"type": "WordPiece", "continuing_subword_prefix": "##",
	"max_input_chars_per_word": 100, "vocab": map[string]int{"[UNK]": 0, "kube": 1, "##rnetes": 2, "go": 3, ",": 4, "cafe": 5}}}

func TestWordPiece(t *testing.T) {
	dir := writeModel(t, wordPiece, [][]float32{{0, 0}, {1, 0}, {1, 0}, {0, 1}, {0, 0}, {1, 1}}, "F32")
	tok, err := LoadTokenizer(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Lower case, accents stripped, punctuation split off, unknown words dropped.
	if got := tok.Encode("Kubernetes, GO und Café"); !slices.Equal(got, []int{1, 2, 4, 3, 5}) {
		t.Fatalf("Encode = %v", got)
	}
	m, err := Load(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := m.Embed("Kubernetes")
	b, _ := m.Embed("go")
	c, _ := m.Embed("kubernetes kubernetes")
	if Cosine(a, b) > 0.01 || Cosine(a, c) < 0.99 {
		t.Fatalf("cosines %v %v", Cosine(a, b), Cosine(a, c))
	}
	if _, err := m.Embed("unbekannt"); !errors.Is(err, ErrEmpty) {
		t.Fatalf("unknown text: %v", err)
	}
}

func TestUnigram(t *testing.T) {
	tok := map[string]any{"model": map[string]any{"type": "Unigram", "unk_id": 0, "vocab": [][]any{
		{"<unk>", 0}, {"▁go", -1}, {"▁entwickler", -2}, {"▁ent", -3}, {"wickler", -3}, {"▁", -4}, {"▁-", -2}}}}
	dir := writeModel(t, tok, [][]float32{{0}, {1}, {1}, {1}, {1}, {1}, {1}}, "F16")
	tk, err := LoadTokenizer(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The best split wins ("▁entwickler" beats "▁ent"+"wickler"); punctuation stands alone; unknown runes are skipped;
	// the case is kept.
	if got := tk.Encode("go-entwickler"); !slices.Equal(got, []int{1, 6, 2}) {
		t.Fatalf("Encode = %v", got)
	}
	if got := tk.Encode("go entwickler ☃"); !slices.Equal(got, []int{1, 2, 5}) {
		t.Fatalf("Encode = %v", got)
	}
	if _, err := Load(dir, 0); err != nil {
		t.Fatal(err)
	}
}

func TestLoadCutsColumnsAndRejectsBadFiles(t *testing.T) {
	dir := writeModel(t, wordPiece, [][]float32{{0, 0, 9}, {1, 0, 9}, {1, 0, 9}, {0, 1, 9}, {0, 0, 9}, {1, 1, 9}}, "F32")
	m, err := Load(dir, 2)
	if err != nil || m.Dim() != 2 {
		t.Fatalf("Load = %v, dim %d", err, m.Dim())
	}
	v, _ := m.Embed("go")
	if v[0] != 0 || v[1] != 1 {
		t.Fatalf("cut vector %v", v)
	}
	// A table with fewer rows than the vocabulary is refused.
	short := writeModel(t, wordPiece, [][]float32{{1}}, "F32")
	if _, err := Load(short, 0); err == nil {
		t.Fatal("short table accepted")
	}
	if err := os.WriteFile(filepath.Join(short, "model.safetensors"), []byte("garbage!"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(short, 0); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestHalfRoundTrip(t *testing.T) {
	for _, f := range []float32{0, 1, -1, 0.5, 0.1, -0.333, 65504, 1e-5, -6e-8, 3.14159} {
		got := half(floatToHalf(f))
		if diff := math.Abs(float64(got - f)); diff > math.Abs(float64(f))*1e-3+6e-8 {
			t.Errorf("half(floatToHalf(%v)) = %v", f, got)
		}
	}
	// Just below the smallest normal rounds up to it.
	if got := floatToHalf(math.Float32frombits(0x387fffff)); got != 0x400 {
		t.Errorf("rounding into the normal range = %#x", got)
	}
	if floatToHalf(1e6) != 0x7c00 || floatToHalf(float32(math.Inf(-1))) != 0xfc00 {
		t.Error("overflow is not infinity")
	}
	for h := range 1 << 16 { // every finite half survives the trip
		if f := half(uint16(h)); !math.IsNaN(float64(f)) && floatToHalf(f) != uint16(h) && f != 0 {
			t.Fatalf("half %#x -> %v -> %#x", h, f, floatToHalf(f))
		}
	}
}

func TestPack(t *testing.T) {
	dir := writeModel(t, wordPiece, [][]float32{{0, 0, 9}, {1, 0, 9}, {1, 0, 9}, {0, 1, 9}, {0, 0, 9}, {0.5, 0.25, 9}}, "F32")
	out := filepath.Join(t.TempDir(), "packed")
	if err := Pack(dir, out, 2); err != nil {
		t.Fatal(err)
	}
	m, err := Load(out, 0)
	if err != nil || m.Dim() != 2 {
		t.Fatalf("Load packed = %v, dim %d", err, m.Dim())
	}
	a, _ := m.Embed("cafe")
	if math.Abs(float64(a[0])-0.894) > 0.01 || math.Abs(float64(a[1])-0.447) > 0.01 {
		t.Fatalf("packed vector %v", a)
	}
}
