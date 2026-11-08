package openlibrary

import "fmt"

// ApproximateSize holds the compressed size of each dump as published by
// OpenLibrary. It is used only for the pre-download estimate; the real size
// comes from the HTTP response.
var ApproximateSize = map[Kind]int64{
	KindEditions: 9_200_000_000,
	KindWorks:    2_900_000_000,
	KindAuthors:  500_000_000,
}

// ApproximateTotal returns the estimated download size for kinds.
func ApproximateTotal(kinds []Kind) int64 {
	var total int64
	for _, kind := range kinds {
		total += ApproximateSize[kind]
	}
	return total
}

// FormatSize renders a byte count using decimal units.
func FormatSize(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d B", n)
	}
	units := "kMGTPE"
	value := float64(n)
	unit := 0
	for value >= 1000 && unit < len(units)-1 {
		value /= 1000
		unit++
	}
	return fmt.Sprintf("%.1f %cB", value, units[unit-1])
}
