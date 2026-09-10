package lsh

import (
	"testing"
)

func TestComputeBands(t *testing.T) {
	sig := make([]uint64, 128)
	for i := range sig {
		sig[i] = uint64(i*1000 + 7)
	}

	bands1 := ComputeBands(sig)
	if len(bands1) != DefaultNumBands {
		t.Fatalf("expected %d bands, got %d", DefaultNumBands, len(bands1))
	}

	bands2 := ComputeBands(sig)
	for i := range bands1 {
		if bands1[i] != bands2[i] {
			t.Errorf("band %d mismatch between runs: %+v vs %+v", i, bands1[i], bands2[i])
		}
	}

	// Changing one element in band 0 should change band 0 hash, but not band 1
	sigModified := make([]uint64, len(sig))
	copy(sigModified, sig)
	sigModified[0] ^= 0xFF

	bandsMod := ComputeBands(sigModified)
	if bandsMod[0].BucketHash == bands1[0].BucketHash {
		t.Errorf("expected band 0 hash to change when element modified")
	}
	if bandsMod[1].BucketHash != bands1[1].BucketHash {
		t.Errorf("expected band 1 hash to stay unchanged")
	}
}

func TestComputeBands_EdgeCases(t *testing.T) {
	if ComputeBands(nil) != nil {
		t.Errorf("expected nil for nil sig")
	}
	if ComputeBands([]uint64{}) != nil {
		t.Errorf("expected nil for empty sig")
	}

	shortSig := make([]uint64, 12)
	bands := ComputeBands(shortSig) // 12 / 5 = 2 bands
	if len(bands) != 2 {
		t.Errorf("expected 2 bands for len 12 with r=5, got %d", len(bands))
	}
}
