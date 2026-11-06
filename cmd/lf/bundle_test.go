package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

func makeBundle(t *testing.T, src string) []byte {
	t.Helper()
	payload, _ := json.Marshal(bundle{Main: "app.lf", Files: map[string]string{"app.lf": src}})
	sum := sha256.Sum256(payload)
	var buf bytes.Buffer
	buf.WriteString("ENGINE-BYTES")
	buf.Write(payload)
	buf.Write(sum[:])
	binary.Write(&buf, binary.LittleEndian, uint64(len(payload)))
	buf.WriteString(bundleMagic)
	return buf.Bytes()
}

func TestBundleRoundTrip(t *testing.T) {
	data := makeBundle(t, `show "hi"`)
	n, ok := attachedSize(data)
	if !ok || string(data[:len(data)-n]) != "ENGINE-BYTES" {
		t.Fatalf("attachment not found correctly (n=%d ok=%v)", n, ok)
	}
	b, err := readBundle(bytes.NewReader(data), int64(len(data)), data[len(data)-footerSize:])
	if err != nil || b.Files[b.Main] != `show "hi"` {
		t.Fatalf("round trip failed: %v", err)
	}
}

func TestBundleTamperingIsRefused(t *testing.T) {
	data := makeBundle(t, `show "hi"`)
	i := bytes.Index(data, []byte("hi"))
	data[i] = 'X'
	_, err := readBundle(bytes.NewReader(data), int64(len(data)), data[len(data)-footerSize:])
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("a changed program must be refused, got %v", err)
	}
}

func TestPlainEngineHasNoAttachment(t *testing.T) {
	if _, ok := attachedSize([]byte("just an engine")); ok {
		t.Fatal("found an attachment where there is none")
	}
}
