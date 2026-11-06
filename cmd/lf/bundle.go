package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"laserfeet/lang"
)

// A built program is the lf engine with the program attached at the end:
//
//	[lf executable][payload JSON][payload SHA-256 (32 bytes)][payload length (8 bytes)][magic (8 bytes)]
//
// When the file starts, it looks for the magic marker at its own end. If it finds one,
// it checks the SHA-256 and runs the attached program instead of acting as lf.
const bundleMagic = "LFBUNDL1"

const footerSize = 32 + 8 + len(bundleMagic)

type bundle struct {
	Version string            `json:"version"`
	Main    string            `json:"main"`
	Files   map[string]string `json:"files"`
}

// buildProgram writes a standalone executable for the program in path.
func buildProgram(path, out string) int {
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "I couldn't open %s: %v\n", path, err)
		return 2
	}
	if _, err := lang.Parse(string(src)); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\nFix this before building.\n", path, err)
		return 1
	}
	if out == "" {
		out = strings.TrimSuffix(filepath.Base(path), ".lf")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
	}
	if abs, _ := filepath.Abs(out); abs == mustAbs(path) {
		fmt.Fprintln(os.Stderr, "The output would overwrite your .lf file. Choose another name with -o.")
		return 2
	}

	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "I couldn't find the lf program itself: %v\n", err)
		return 1
	}
	engine, err := os.ReadFile(self)
	if err != nil {
		fmt.Fprintf(os.Stderr, "I couldn't read the lf program: %v\n", err)
		return 1
	}
	// If this lf is itself a built program, strip its attachment first.
	if n, ok := attachedSize(engine); ok {
		engine = engine[:len(engine)-n]
	}

	payload, _ := json.Marshal(bundle{Version: version, Main: filepath.Base(path), Files: map[string]string{filepath.Base(path): string(src)}})
	sum := sha256.Sum256(payload)
	var buf bytes.Buffer
	buf.Write(engine)
	buf.Write(payload)
	buf.Write(sum[:])
	binary.Write(&buf, binary.LittleEndian, uint64(len(payload)))
	buf.WriteString(bundleMagic)

	if err := os.WriteFile(out, buf.Bytes(), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "I couldn't write %s: %v\n", out, err)
		return 1
	}
	if err := signForMac(out); err != nil {
		fmt.Fprintf(os.Stderr, "Built %s, but signing it for macOS failed: %v\n", out, err)
		return 1
	}
	fmt.Printf("Built %s (%.1f MB). It runs on its own, with nothing else installed:\n  ./%s\n", out, float64(buf.Len())/1e6, out)
	return 0
}

func mustAbs(p string) string {
	a, _ := filepath.Abs(p)
	return a
}

// attachedSize reports how many bytes at the end of data are an attached program.
func attachedSize(data []byte) (int, bool) {
	if len(data) < footerSize || string(data[len(data)-len(bundleMagic):]) != bundleMagic {
		return 0, false
	}
	n := binary.LittleEndian.Uint64(data[len(data)-len(bundleMagic)-8:])
	total := uint64(footerSize) + n
	if n == 0 || total > uint64(len(data)) {
		return 0, false
	}
	return int(total), true
}

// runAttached runs the program attached to this executable, if there is one.
func runAttached() (code int, found bool) {
	self, err := os.Executable()
	if err != nil {
		return 0, false
	}
	f, err := os.Open(self)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() < int64(footerSize) {
		return 0, false
	}
	foot := make([]byte, footerSize)
	if _, err := f.ReadAt(foot, st.Size()-int64(footerSize)); err != nil {
		return 0, false
	}
	if string(foot[footerSize-len(bundleMagic):]) != bundleMagic {
		return 0, false
	}
	b, err := readBundle(f, st.Size(), foot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "This program can't start: %v\n", err)
		return 1, true
	}
	in := lang.NewInterp(os.Stdout)
	if err := in.Run(b.Files[b.Main]); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", b.Main, err)
		return 1, true
	}
	return 0, true
}

func readBundle(r io.ReaderAt, size int64, foot []byte) (*bundle, error) {
	want := foot[:32]
	n := binary.LittleEndian.Uint64(foot[32:40])
	if n == 0 || n > uint64(size)-uint64(footerSize) || n > 256<<20 {
		return nil, errors.New("its attached program is damaged")
	}
	payload := make([]byte, n)
	if _, err := r.ReadAt(payload, size-int64(footerSize)-int64(n)); err != nil {
		return nil, errors.New("its attached program couldn't be read")
	}
	if got := sha256.Sum256(payload); !bytes.Equal(got[:], want) {
		return nil, errors.New("its attached program failed its SHA-256 check (the file was changed or damaged after it was built)")
	}
	var b bundle
	if err := json.Unmarshal(payload, &b); err != nil || b.Files[b.Main] == "" {
		return nil, errors.New("its attached program is damaged")
	}
	return &b, nil
}
