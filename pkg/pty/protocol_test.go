package pty

import (
	"bytes"
	"strings"
	"testing"
)

func TestProtocolScannerAllowsLargeMessages(t *testing.T) {
	payload := strings.Repeat("x", 512*1024)
	msg := Msg{
		Type: MsgBuffer,
		Data: EncodeData([]byte(payload)),
	}
	encoded, err := Encode(msg)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if len(encoded) <= 64*1024 {
		t.Fatalf("test message is not larger than default scanner limit: %d", len(encoded))
	}

	scanner := newProtocolScanner(bytes.NewReader(encoded))
	if !scanner.Scan() {
		t.Fatalf("scanner failed: %v", scanner.Err())
	}

	decoded, err := Decode(scanner.Bytes())
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}
	data, err := DecodeData(decoded.Data)
	if err != nil {
		t.Fatalf("DecodeData failed: %v", err)
	}
	if string(data) != payload {
		t.Fatal("decoded payload mismatch")
	}
}
