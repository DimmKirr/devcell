package mctcatalog

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// extractCABWithFallback tries cabextract (handles LZX) then falls back to pure Go.
func extractCABWithFallback(data []byte, logf func(string, ...any)) (map[string][]byte, error) {
	if cabPath, err := exec.LookPath("cabextract"); err == nil {
		logf("using cabextract (%s) for CAB extraction", cabPath)
		result, err := extractCABExternal(data, cabPath)
		if err == nil {
			return result, nil
		}
		logf("cabextract failed: %v — falling back to Go CAB reader", err)
	} else {
		logf("cabextract not on PATH — using Go CAB reader (MSZIP only)")
	}
	return extractCAB(data)
}

func extractCABExternal(data []byte, cabextractPath string) (map[string][]byte, error) {
	tmpDir, err := os.MkdirTemp("", "mct-cab-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	cabFile := filepath.Join(tmpDir, "catalog.cab")
	if err := os.WriteFile(cabFile, data, 0644); err != nil {
		return nil, err
	}

	outDir := filepath.Join(tmpDir, "out")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, err
	}

	cmd := exec.Command(cabextractPath, "-d", outDir, cabFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("cabextract: %w\n%s", err, out)
	}

	result := make(map[string][]byte)
	err = filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(outDir, path)
		result[rel] = content
		return nil
	})
	return result, err
}

// extractCAB extracts all files from a Microsoft Cabinet (CAB) archive.
// Only supports MSZIP and uncompressed folders — LZX (type 3) requires cabextract.
func extractCAB(data []byte) (map[string][]byte, error) {
	if len(data) < 36 {
		return nil, fmt.Errorf("cab: data too short (%d bytes)", len(data))
	}
	if string(data[:4]) != "MSCF" {
		return nil, fmt.Errorf("cab: bad magic %q (expected MSCF)", data[:4])
	}

	r := bytes.NewReader(data)

	var hdr cabHeader
	if err := binary.Read(r, binary.LittleEndian, &hdr); err != nil {
		return nil, fmt.Errorf("cab: reading header: %w", err)
	}

	// Skip reserved fields if present.
	if hdr.Flags&0x0004 != 0 { // cfhdrRESERVE_PRESENT
		var resHdr struct {
			CbCFHeader  uint16
			CbCFFolder  uint8
			CbCFData    uint8
		}
		if err := binary.Read(r, binary.LittleEndian, &resHdr); err != nil {
			return nil, fmt.Errorf("cab: reading reserve header: %w", err)
		}
		if resHdr.CbCFHeader > 0 {
			if _, err := r.Seek(int64(resHdr.CbCFHeader), io.SeekCurrent); err != nil {
				return nil, err
			}
		}
	}
	if hdr.Flags&0x0001 != 0 { // cfhdrPREV_CABINET
		skipCString(r)
		skipCString(r)
	}
	if hdr.Flags&0x0002 != 0 { // cfhdrNEXT_CABINET
		skipCString(r)
		skipCString(r)
	}

	folders := make([]cabFolder, hdr.CFolders)
	for i := range folders {
		if err := binary.Read(r, binary.LittleEndian, &folders[i]); err != nil {
			return nil, fmt.Errorf("cab: reading folder %d: %w", i, err)
		}
	}

	if _, err := r.Seek(int64(hdr.OffFiles), io.SeekStart); err != nil {
		return nil, fmt.Errorf("cab: seeking to files: %w", err)
	}

	type fileEntry struct {
		size   uint32
		offset uint32
		folder uint16
		name   string
	}
	files := make([]fileEntry, hdr.CFiles)
	for i := range files {
		var fe cabFile
		if err := binary.Read(r, binary.LittleEndian, &fe); err != nil {
			return nil, fmt.Errorf("cab: reading file entry %d: %w", i, err)
		}
		name, err := readCString(r)
		if err != nil {
			return nil, fmt.Errorf("cab: reading file name %d: %w", i, err)
		}
		files[i] = fileEntry{
			size:   fe.UncompSize,
			offset: fe.UncompOffset,
			folder: fe.FolderIndex,
			name:   name,
		}
	}

	folderData := make(map[uint16][]byte)
	for fi, f := range folders {
		if _, err := r.Seek(int64(f.DataOffset), io.SeekStart); err != nil {
			return nil, fmt.Errorf("cab: seeking to folder %d data: %w", fi, err)
		}
		var uncompressed bytes.Buffer
		for block := uint16(0); block < f.DataBlocks; block++ {
			var dh cabDataHeader
			if err := binary.Read(r, binary.LittleEndian, &dh); err != nil {
				return nil, fmt.Errorf("cab: reading data block %d/%d: %w", fi, block, err)
			}
			compData := make([]byte, dh.CompSize)
			if _, err := io.ReadFull(r, compData); err != nil {
				return nil, fmt.Errorf("cab: reading compressed data: %w", err)
			}

			compType := f.CompType & 0x000F
			switch compType {
			case 0: // none
				uncompressed.Write(compData)
			case 1: // MSZIP
				if len(compData) < 2 || compData[0] != 'C' || compData[1] != 'K' {
					return nil, fmt.Errorf("cab: MSZIP block missing CK signature")
				}
				fr := flate.NewReader(bytes.NewReader(compData[2:]))
				if _, err := io.Copy(&uncompressed, fr); err != nil {
					fr.Close()
					return nil, fmt.Errorf("cab: decompressing MSZIP block: %w", err)
				}
				fr.Close()
			default:
				return nil, fmt.Errorf("cab: unsupported compression type %d", compType)
			}
		}
		folderData[uint16(fi)] = uncompressed.Bytes()
	}

	result := make(map[string][]byte)
	for _, f := range files {
		fd, ok := folderData[f.folder]
		if !ok {
			return nil, fmt.Errorf("cab: file %q references unknown folder %d", f.name, f.folder)
		}
		end := f.offset + f.size
		if int(end) > len(fd) {
			return nil, fmt.Errorf("cab: file %q extends beyond folder data (%d > %d)", f.name, end, len(fd))
		}
		result[f.name] = fd[f.offset:end]
	}
	return result, nil
}

type cabHeader struct {
	Signature  [4]byte
	_          uint32 // reserved
	CabinetSz uint32
	_          uint32 // reserved
	OffFiles   uint32
	_          uint32 // reserved
	VerMinor   uint8
	VerMajor   uint8
	CFolders   uint16
	CFiles     uint16
	Flags      uint16
	SetID      uint16
	CabinetIdx uint16
}

type cabFolder struct {
	DataOffset uint32
	DataBlocks uint16
	CompType   uint16
}

type cabFile struct {
	UncompSize   uint32
	UncompOffset uint32
	FolderIndex  uint16
	Date         uint16
	Time         uint16
	Attrs        uint16
}

type cabDataHeader struct {
	Checksum uint32
	CompSize uint16
	UncompSz uint16
}

func readCString(r io.Reader) (string, error) {
	var buf []byte
	b := make([]byte, 1)
	for {
		if _, err := r.Read(b); err != nil {
			return "", err
		}
		if b[0] == 0 {
			break
		}
		buf = append(buf, b[0])
	}
	return string(buf), nil
}

func skipCString(r io.ReadSeeker) {
	b := make([]byte, 1)
	for {
		if _, err := r.Read(b); err != nil || b[0] == 0 {
			return
		}
	}
}
