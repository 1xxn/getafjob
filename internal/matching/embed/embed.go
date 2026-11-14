// Package embed turns text into a vector with a static embedding model (Model2Vec, sentence-transformers
// StaticEmbedding): a tokenizer and one vector per token, averaged. There are no network layers, so a text costs a
// table lookup per token; the model runs in-process on any CPU (#485).
package embed

import (
	"errors"
	"fmt"
	"math"
	"os"
)

// Model is a static embedding model: a tokenizer and a token table of Dim columns.
type Model struct {
	tok   *Tokenizer
	dim   int
	table []float32 // rows of dim, one per token id
}

// Load reads tokenizer.json and model.safetensors from dir. dims > 0 keeps only the first dims columns (Matryoshka
// models stay good when cut); 0 keeps all.
func Load(dir string, dims int) (*Model, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	tok, err := loadTokenizer(root)
	if err != nil {
		return nil, err
	}
	f, err := root.Open("model.safetensors")
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	t, err := readTable(f)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	if t.rows < tok.Size() {
		return nil, fmt.Errorf("embed: table has %d rows for %d tokens", t.rows, tok.Size())
	}
	m := &Model{tok: tok, dim: t.cols, table: t.data}
	if dims > 0 && dims < t.cols {
		m.table, m.dim = cut(t.data, t.rows, t.cols, dims), dims
	}
	return m, nil
}

func cut(data []float32, rows, cols, dims int) []float32 {
	out := make([]float32, rows*dims)
	for r := range rows {
		copy(out[r*dims:(r+1)*dims], data[r*cols:r*cols+dims])
	}
	return out
}

// Dim is the length of the vectors.
func (m *Model) Dim() int { return m.dim }

// ErrEmpty reports a text without a known token.
var ErrEmpty = errors.New("embed: no known token")

// Embed returns the unit-length mean of the token vectors of text.
func (m *Model) Embed(text string) ([]float32, error) {
	ids := m.tok.Encode(text)
	if len(ids) == 0 {
		return nil, ErrEmpty
	}
	v := make([]float32, m.dim)
	for _, id := range ids {
		row := m.table[id*m.dim : (id+1)*m.dim]
		for i, x := range row {
			v[i] += x
		}
	}
	normalize(v)
	return v, nil
}

func normalize(v []float32) {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	if s == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(s))
	for i := range v {
		v[i] *= inv
	}
}

// Cosine is the cosine similarity of two unit vectors of the same length.
func Cosine(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}
