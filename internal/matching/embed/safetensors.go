package embed

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
)

// table is the one 2-D tensor of a static model file.
type table struct {
	rows, cols int
	data       []float32
}

// maxHeader bounds the JSON header of a safetensors file.
const maxHeader = 1 << 20

// readTable reads the single 2-D tensor of a safetensors file (F32 or F16) as float32.
func readTable(r io.Reader) (table, error) {
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
	raw := make([]byte, info.Offsets[1])
	if _, err := io.ReadFull(r, raw); err != nil {
		return table{}, err
	}
	data := make([]float32, rows*cols)
	for i := range data {
		if size == 4 {
			data[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
		} else {
			data[i] = half(binary.LittleEndian.Uint16(raw[i*2:]))
		}
	}
	return table{rows: rows, cols: cols, data: data}, nil
}

// half converts an IEEE 754 half-precision number.
func half(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := uint32(h>>10) & 0x1f
	frac := uint32(h) & 0x3ff
	switch exp {
	case 0:
		if frac == 0 {
			return math.Float32frombits(sign)
		}
		f := float32(frac) / 1024 * float32(math.Pow(2, -14))
		if sign != 0 {
			f = -f
		}
		return f
	case 0x1f:
		return math.Float32frombits(sign | 0x7f800000 | frac<<13)
	}
	return math.Float32frombits(sign | (exp+112)<<23 | frac<<13)
}
