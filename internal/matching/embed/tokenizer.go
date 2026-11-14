package embed

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Tokenizer covers the two kinds of tokenizer.json that static models ship: BERT WordPiece (lower case, accents
// stripped) and SentencePiece Unigram with a Metaspace pre-tokenizer. Unknown pieces are dropped: their vector says
// nothing about the text.
type Tokenizer struct {
	kind string // WordPiece or Unigram
	ids  map[string]int
	size int
	// WordPiece
	prefix  string
	maxWord int
	// Unigram
	scores   []float64
	maxPiece int // in bytes
}

// maxText bounds the text one call tokenizes; job descriptions are far shorter.
const maxText = 100_000

// LoadTokenizer reads the Hugging Face tokenizer.json in dir.
func LoadTokenizer(dir string) (*Tokenizer, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return loadTokenizer(root)
}

// maxTokenizer bounds tokenizer.json (multilingual vocabularies are a few tens of MB).
const maxTokenizer = 64 << 20

func loadTokenizer(root *os.Root) (*Tokenizer, error) {
	file, err := root.Open("tokenizer.json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	b, err := io.ReadAll(io.LimitReader(file, maxTokenizer+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxTokenizer {
		return nil, errors.New("tokenizer: file too large")
	}
	var f struct {
		Model struct {
			Type     string          `json:"type"`
			Prefix   string          `json:"continuing_subword_prefix"`
			MaxChars int             `json:"max_input_chars_per_word"`
			Vocab    json.RawMessage `json:"vocab"`
		} `json:"model"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("tokenizer: %w", err)
	}
	t := &Tokenizer{kind: f.Model.Type, ids: map[string]int{}}
	switch f.Model.Type {
	case "WordPiece":
		if err := json.Unmarshal(f.Model.Vocab, &t.ids); err != nil {
			return nil, fmt.Errorf("tokenizer: %w", err)
		}
		for _, id := range t.ids {
			t.size = max(t.size, id+1)
		}
		t.prefix, t.maxWord = f.Model.Prefix, max(f.Model.MaxChars, 1)
	case "Unigram":
		var vocab [][2]json.RawMessage
		if err := json.Unmarshal(f.Model.Vocab, &vocab); err != nil {
			return nil, fmt.Errorf("tokenizer: %w", err)
		}
		t.scores = make([]float64, len(vocab))
		for id, e := range vocab {
			var piece string
			if err := json.Unmarshal(e[0], &piece); err != nil {
				return nil, fmt.Errorf("tokenizer: %w", err)
			}
			if err := json.Unmarshal(e[1], &t.scores[id]); err != nil {
				return nil, fmt.Errorf("tokenizer: %w", err)
			}
			if _, dup := t.ids[piece]; !dup {
				t.ids[piece] = id
			}
			t.maxPiece = max(t.maxPiece, len(piece))
		}
		t.size = len(vocab)
	default:
		return nil, fmt.Errorf("tokenizer: model %q not supported", f.Model.Type)
	}
	if t.size == 0 {
		return nil, errors.New("tokenizer: empty vocabulary")
	}
	return t, nil
}

// Size is the number of token ids.
func (t *Tokenizer) Size() int { return t.size }

// Encode returns the ids of the known tokens of text.
func (t *Tokenizer) Encode(text string) []int {
	if len(text) > maxText {
		text = text[:maxText]
	}
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, " ")
	}
	if t.kind == "WordPiece" {
		return t.wordPiece(text)
	}
	return t.unigram(text)
}

// --- WordPiece (BERT, uncased) ---

func (t *Tokenizer) wordPiece(text string) []int {
	var ids []int
	for _, w := range bertWords(text) {
		ids = t.appendWord(ids, w)
	}
	return ids
}

// bertWords lower-cases, strips accents and splits on spaces and punctuation, as BERT's normalizer and
// pre-tokenizer do.
func bertWords(text string) []string {
	var words []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			words = append(words, b.String())
			b.Reset()
		}
	}
	for _, r := range norm.NFD.String(strings.ToLower(text)) {
		switch {
		case unicode.Is(unicode.Mn, r), r == 0, r == utf8.RuneError, unicode.IsControl(r) && !unicode.IsSpace(r):
		case unicode.IsSpace(r):
			flush()
		case isPunct(r) || unicode.Is(unicode.Han, r):
			flush()
			words = append(words, string(r))
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return words
}

func isPunct(r rune) bool {
	return r >= 33 && r <= 47 || r >= 58 && r <= 64 || r >= 91 && r <= 96 || r >= 123 && r <= 126 || unicode.IsPunct(r)
}

// appendWord adds the greedy longest-match pieces of w; a word with an unknown part adds nothing.
func (t *Tokenizer) appendWord(ids []int, w string) []int {
	if utf8.RuneCountInString(w) > t.maxWord {
		return ids
	}
	n := len(ids)
	for start := 0; start < len(w); {
		end, id := len(w), -1
		for ; end > start; end-- {
			piece := w[start:end]
			if start > 0 {
				piece = t.prefix + piece
			}
			if v, ok := t.ids[piece]; ok {
				id = v
				break
			}
		}
		if id < 0 {
			return ids[:n]
		}
		ids = append(ids, id)
		start = end
	}
	return ids
}

// --- Unigram (SentencePiece with Metaspace) ---

// asciiPunct are split off as words by Model2Vec's normalizer.
const asciiPunct = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

func (t *Tokenizer) unigram(text string) []int {
	var b strings.Builder
	space := true
	for _, r := range norm.NFKC.String(text) {
		switch {
		case unicode.IsSpace(r) || unicode.IsControl(r):
			if !space {
				b.WriteByte(' ')
				space = true
			}
		case r < utf8.RuneSelf && strings.ContainsRune(asciiPunct, r):
			if !space {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			b.WriteByte(' ')
			space = true
		default:
			b.WriteRune(r)
			space = false
		}
	}
	s := "▁" + strings.ReplaceAll(strings.TrimSpace(b.String()), " ", "▁")
	return t.viterbi(s)
}

// viterbi splits s into the pieces with the best total score; positions no piece covers are skipped.
func (t *Tokenizer) viterbi(s string) []int {
	n := len(s)
	best := make([]float64, n+1)
	from := make([]int, n+1)
	piece := make([]int, n+1)
	for i := 1; i <= n; i++ {
		best[i] = math.Inf(-1)
	}
	for i := 0; i < n; i++ {
		if math.IsInf(best[i], -1) || !utf8.RuneStart(s[i]) {
			continue
		}
		matched := false
		for j := i + 1; j <= min(n, i+t.maxPiece); j++ {
			if j < n && !utf8.RuneStart(s[j]) {
				continue
			}
			if id, ok := t.ids[s[i:j]]; ok {
				matched = true
				if sc := best[i] + t.scores[id]; sc > best[j] {
					best[j], from[j], piece[j] = sc, i, id
				}
			}
		}
		if !matched { // skip one unknown character
			_, size := utf8.DecodeRuneInString(s[i:])
			if sc := best[i] - 20; sc > best[i+size] {
				best[i+size], from[i+size], piece[i+size] = sc, i, -1
			}
		}
	}
	var ids []int
	for j := n; j > 0; j = from[j] {
		if math.IsInf(best[j], -1) {
			break
		}
		if piece[j] >= 0 {
			ids = append(ids, piece[j])
		}
	}
	for l, r := 0, len(ids)-1; l < r; l, r = l+1, r-1 {
		ids[l], ids[r] = ids[r], ids[l]
	}
	return ids
}
