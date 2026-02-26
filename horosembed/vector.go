// CLAUDE:SUMMARY Vector serialization, deserialization, cosine similarity, and L2 norm computation.
// CLAUDE:EXPORTS SerializeVector, DeserializeVector, CosineSimilarity, CosineSimilarityOptimized, CalculateNorm
package horosembed

import (
	"encoding/binary"
	"math"
)

// SerializeVector converts a float32 slice to bytes (little-endian).
func SerializeVector(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// DeserializeVector converts bytes back to a float32 slice.
func DeserializeVector(blob []byte) []float32 {
	vec := make([]float32, len(blob)/4)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return vec
}

// CosineSimilarity computes cosine similarity between two vectors.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// CosineSimilarityOptimized computes cosine similarity with pre-calculated L2 norms.
func CosineSimilarityOptimized(a, b []float32, normA, normB float64) float64 {
	if len(a) != len(b) || normA == 0 || normB == 0 {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot / (normA * normB)
}

// CalculateNorm computes the L2 norm of a vector.
func CalculateNorm(vec []float32) float64 {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum)
}
