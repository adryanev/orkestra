package pty

import (
	"sync"
)

// RingBuffer is a thread-safe circular buffer that overwrites the oldest data
// when capacity is exceeded.
type RingBuffer struct {
	mu       sync.RWMutex
	buf      []byte
	capacity int
	start    int // index of oldest byte
	size     int // current number of bytes stored
}

// NewRingBuffer creates a new ring buffer with the given capacity.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = 4096 // default to 4KB
	}
	return &RingBuffer{
		buf:      make([]byte, capacity),
		capacity: capacity,
	}
}

// Write appends data to the ring buffer.
// If the buffer is full, it overwrites the oldest data.
// Implements io.Writer interface.
func (rb *RingBuffer) Write(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}

	rb.mu.Lock()
	defer rb.mu.Unlock()

	n = len(p)

	// If incoming data is larger than capacity, only keep the tail
	if n >= rb.capacity {
		copy(rb.buf, p[n-rb.capacity:])
		rb.start = 0
		rb.size = rb.capacity
		return n, nil
	}

	// Calculate the write position before updating start/size
	writePos := (rb.start + rb.size) % rb.capacity

	// Calculate how many bytes will be discarded
	totalAfterWrite := rb.size + n
	if totalAfterWrite > rb.capacity {
		// We'll need to advance start to discard old data
		discard := totalAfterWrite - rb.capacity
		rb.start = (rb.start + discard) % rb.capacity
		rb.size = rb.capacity
	} else {
		rb.size = totalAfterWrite
	}

	// Write data in chunks, wrapping around as needed
	if writePos+n <= rb.capacity {
		// Data fits contiguously
		copy(rb.buf[writePos:], p)
	} else {
		// Data wraps around
		firstPart := rb.capacity - writePos
		copy(rb.buf[writePos:], p[:firstPart])
		copy(rb.buf, p[firstPart:])
	}

	return n, nil
}

// Bytes returns an ordered snapshot of the current buffer contents.
// The returned slice is a copy and safe to use after the call returns.
func (rb *RingBuffer) Bytes() []byte {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.size == 0 {
		return nil
	}

	result := make([]byte, rb.size)

	if rb.start+rb.size <= rb.capacity {
		// Data is contiguous
		copy(result, rb.buf[rb.start:rb.start+rb.size])
	} else {
		// Data wraps around
		firstPart := rb.capacity - rb.start
		copy(result, rb.buf[rb.start:])
		copy(result[firstPart:], rb.buf[:rb.size-firstPart])
	}

	return result
}

// Len returns the current number of bytes in the buffer.
func (rb *RingBuffer) Len() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.size
}

// Reset clears the buffer.
func (rb *RingBuffer) Reset() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.start = 0
	rb.size = 0
}
