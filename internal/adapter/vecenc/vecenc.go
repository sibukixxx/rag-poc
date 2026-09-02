// Package vecenc encodes/decodes []float32 embedding vectors as bytes for
// SQLite BLOB storage. It's a standalone package (rather than living in
// internal/adapter/sqlite) so the embedded vector search adapter planned
// for W4 can decode the same format without importing the DB layer.
package vecenc

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Encode writes each float32 as 4 little-endian bytes.
func Encode(vector []float32) []byte {
	buf := make([]byte, 4*len(vector))
	for i, v := range vector {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// Decode is Encode's inverse. It errors on a length that isn't a multiple
// of 4 bytes, which would indicate on-disk corruption.
func Decode(data []byte) ([]float32, error) {
	if len(data)%4 != 0 {
		return nil, fmt.Errorf("vecenc: invalid vector byte length %d", len(data))
	}
	vector := make([]float32, len(data)/4)
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return vector, nil
}
