package pty

import (
	"bytes"
	"sync"
	"testing"
)

func TestRingBuffer_HappyPath(t *testing.T) {
	rb := NewRingBuffer(10)

	// Write less than capacity
	n, err := rb.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != 5 {
		t.Errorf("expected n=5, got %d", n)
	}
	if rb.Len() != 5 {
		t.Errorf("expected len=5, got %d", rb.Len())
	}

	result := rb.Bytes()
	if !bytes.Equal(result, []byte("hello")) {
		t.Errorf("expected 'hello', got %q", result)
	}

	// Write more data
	rb.Write([]byte("world"))
	result = rb.Bytes()
	if !bytes.Equal(result, []byte("helloworld")) {
		t.Errorf("expected 'helloworld', got %q", result)
	}
}

func TestRingBuffer_Wraparound(t *testing.T) {
	rb := NewRingBuffer(10)

	// Fill the buffer
	rb.Write([]byte("0123456789"))
	result := rb.Bytes()
	if !bytes.Equal(result, []byte("0123456789")) {
		t.Errorf("expected '0123456789', got %q", result)
	}

	// Write more, causing wraparound
	rb.Write([]byte("abc"))
	result = rb.Bytes()
	if !bytes.Equal(result, []byte("3456789abc")) {
		t.Errorf("expected '3456789abc', got %q", result)
	}

	// Write more to test multiple wraparounds
	rb.Write([]byte("XYZ"))
	result = rb.Bytes()
	if !bytes.Equal(result, []byte("6789abcXYZ")) {
		t.Errorf("expected '6789abcXYZ', got %q", result)
	}
}

func TestRingBuffer_LargerThanCapacity(t *testing.T) {
	rb := NewRingBuffer(5)

	// Write data larger than capacity
	rb.Write([]byte("0123456789"))
	result := rb.Bytes()
	// Should only keep the last 5 bytes
	if !bytes.Equal(result, []byte("56789")) {
		t.Errorf("expected '56789', got %q", result)
	}
}

func TestRingBuffer_Empty(t *testing.T) {
	rb := NewRingBuffer(10)

	result := rb.Bytes()
	if result != nil {
		t.Errorf("expected nil for empty buffer, got %q", result)
	}

	if rb.Len() != 0 {
		t.Errorf("expected len=0, got %d", rb.Len())
	}
}

func TestRingBuffer_Reset(t *testing.T) {
	rb := NewRingBuffer(10)

	rb.Write([]byte("hello"))
	rb.Reset()

	if rb.Len() != 0 {
		t.Errorf("expected len=0 after reset, got %d", rb.Len())
	}

	result := rb.Bytes()
	if result != nil {
		t.Errorf("expected nil after reset, got %q", result)
	}
}

func TestRingBuffer_ZeroCapacity(t *testing.T) {
	rb := NewRingBuffer(0)

	// Should default to 4096
	if rb.capacity != 4096 {
		t.Errorf("expected default capacity 4096, got %d", rb.capacity)
	}
}

func TestRingBuffer_EmptyWrite(t *testing.T) {
	rb := NewRingBuffer(10)

	n, err := rb.Write([]byte{})
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != 0 {
		t.Errorf("expected n=0, got %d", n)
	}
	if rb.Len() != 0 {
		t.Errorf("expected len=0, got %d", rb.Len())
	}
}

func TestRingBuffer_Concurrency(t *testing.T) {
	rb := NewRingBuffer(1000)
	var wg sync.WaitGroup

	// Number of concurrent writers
	writers := 10
	writesPerWriter := 100

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			data := []byte{byte(id)}
			for j := 0; j < writesPerWriter; j++ {
				rb.Write(data)
			}
		}(i)
	}

	// Concurrent readers
	readers := 5
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < writesPerWriter; j++ {
				_ = rb.Bytes()
				_ = rb.Len()
			}
		}()
	}

	wg.Wait()

	// Verify buffer is valid (doesn't panic, has valid length)
	result := rb.Bytes()
	if rb.Len() != len(result) {
		t.Errorf("len mismatch: Len()=%d, len(Bytes())=%d", rb.Len(), len(result))
	}
	if rb.Len() > rb.capacity {
		t.Errorf("buffer exceeded capacity: len=%d, capacity=%d", rb.Len(), rb.capacity)
	}
}

func TestRingBuffer_MultipleWraparounds(t *testing.T) {
	rb := NewRingBuffer(8)

	// Write in chunks that will cause multiple wraparounds
	rb.Write([]byte("abc"))   // "abc_____" (3 bytes)
	rb.Write([]byte("def"))   // "abcdef__" (6 bytes)
	rb.Write([]byte("ghi"))   // "bcdefghi" (8 bytes, discarded 'a')
	rb.Write([]byte("jkl"))   // "efghijkl" (8 bytes, discarded 'bcd')
	rb.Write([]byte("mnop"))  // "ijklmnop" (8 bytes, discarded 'efgh')

	result := rb.Bytes()
	expected := []byte("ijklmnop")
	if !bytes.Equal(result, expected) {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestRingBuffer_SingleByteWrites(t *testing.T) {
	rb := NewRingBuffer(5)

	// Write one byte at a time
	for _, b := range []byte("0123456789") {
		rb.Write([]byte{b})
	}

	result := rb.Bytes()
	if !bytes.Equal(result, []byte("56789")) {
		t.Errorf("expected '56789', got %q", result)
	}
}
