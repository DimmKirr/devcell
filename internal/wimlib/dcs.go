package wimlib

import (
	"encoding/binary"
	"fmt"
)

var dcsMagic = [4]byte{'D', 'C', 'S', 0x01}

// DecompressDCS decompresses a DCS\x01 container (CBS delta-compressed store).
// The format wraps LZMS-compressed blocks:
//
//	Header (12 bytes): Signature(4) + NumberOfBlocks(4, LE) + UncompressedFileSize(4, LE)
//	Per block: BlockSize(4, LE) + DecompressedBlockSize(4, LE) + CompressedData[BlockSize-4]
//	Next block starts at current offset + BlockSize + 4.
func DecompressDCS(data []byte) ([]byte, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("dcs: data too short (%d bytes)", len(data))
	}
	if [4]byte(data[:4]) != dcsMagic {
		return nil, fmt.Errorf("dcs: bad signature %x", data[:4])
	}

	numBlocks := binary.LittleEndian.Uint32(data[4:8])
	uncompTotal := binary.LittleEndian.Uint32(data[8:12])

	if numBlocks == 0 || uncompTotal == 0 {
		return nil, fmt.Errorf("dcs: zero blocks or zero size")
	}

	// First pass: find the max decompressed block size for the decompressor.
	var maxDecomp uint32
	off := 12
	for i := uint32(0); i < numBlocks; i++ {
		if off+8 > len(data) {
			return nil, fmt.Errorf("dcs: block %d header truncated at offset %d", i, off)
		}
		blkSize := binary.LittleEndian.Uint32(data[off : off+4])
		decompSize := binary.LittleEndian.Uint32(data[off+4 : off+8])
		if decompSize > maxDecomp {
			maxDecomp = decompSize
		}
		off += int(blkSize) + 4
	}

	decomp, err := NewDecompressor(LZMS, int(maxDecomp))
	if err != nil {
		return nil, fmt.Errorf("dcs: creating LZMS decompressor: %w", err)
	}
	defer decomp.Close()

	out := make([]byte, uncompTotal)
	outOff := 0
	off = 12

	for i := uint32(0); i < numBlocks; i++ {
		if off+8 > len(data) {
			return nil, fmt.Errorf("dcs: block %d header truncated at offset %d", i, off)
		}
		blkSize := binary.LittleEndian.Uint32(data[off : off+4])
		decompSize := binary.LittleEndian.Uint32(data[off+4 : off+8])
		compSize := blkSize - 4

		if off+8+int(compSize) > len(data) {
			return nil, fmt.Errorf("dcs: block %d data truncated (need %d at offset %d, have %d)",
				i, compSize, off+8, len(data)-off-8)
		}
		if outOff+int(decompSize) > len(out) {
			return nil, fmt.Errorf("dcs: block %d would exceed total uncompressed size", i)
		}

		block, err := decomp.Decompress(data[off+8:off+8+int(compSize)], int(decompSize))
		if err != nil {
			return nil, fmt.Errorf("dcs: block %d LZMS decompress failed: %w", i, err)
		}
		copy(out[outOff:], block)

		outOff += int(decompSize)
		off += int(blkSize) + 4
	}

	if outOff != int(uncompTotal) {
		return nil, fmt.Errorf("dcs: decompressed %d bytes, expected %d", outOff, uncompTotal)
	}
	return out, nil
}

// IsDCS checks whether data starts with the DCS\x01 signature.
func IsDCS(data []byte) bool {
	return len(data) >= 4 && [4]byte(data[:4]) == dcsMagic
}
