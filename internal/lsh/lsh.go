package lsh

const (
	// DefaultNumBands is the default number of bands (b).
	DefaultNumBands = 25

	// DefaultRowsPerBand is the default number of rows per band (r).
	DefaultRowsPerBand = 5

	// MinSimilarityThreshold is the Jaccard threshold (75%) above which two
	// book files are considered manifestations of the same book.
	MinSimilarityThreshold = 0.75
)

// Band represents a single LSH band index and its bucket hash.
type Band struct {
	BandIdx    int
	BucketHash int64
}

// ComputeBands divides the MinHash signature into DefaultNumBands bands of
// DefaultRowsPerBand rows each and hashes each band to a 64-bit bucket key.
func ComputeBands(sig []uint64) []Band {
	return ComputeBandsCustom(sig, DefaultNumBands, DefaultRowsPerBand)
}

// ComputeBandsCustom divides sig into numBands of rowsPerBand rows.
// If sig has fewer than numBands*rowsPerBand elements, as many full bands as possible are produced.
func ComputeBandsCustom(sig []uint64, numBands, rowsPerBand int) []Band {
	if numBands <= 0 || rowsPerBand <= 0 || len(sig) == 0 {
		return nil
	}
	availableBands := len(sig) / rowsPerBand
	n := min(numBands, availableBands)
	bands := make([]Band, n)
	for i := range n {
		start := i * rowsPerBand
		end := start + rowsPerBand
		bands[i] = Band{
			BandIdx:    i,
			BucketHash: HashBand(sig[start:end]),
		}
	}
	return bands
}

// HashBand computes a 64-bit hash for a single band of MinHash rows.
func HashBand(rows []uint64) int64 {
	var h uint64 = 14695981039346656037
	for _, v := range rows {
		h ^= v
		h *= 1099511628211
		h ^= h >> 32
	}
	return int64(h)
}
