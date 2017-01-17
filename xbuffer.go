package xtcp

import (
	"errors"
	"io"
)

var (
	// ErrNoMemory is passed to panic if memory cannot be allocated to store data in a buffer.
	ErrNoMemory = errors.New("xtcp.buffer: no memory")
	// ErrSpaceLimit means that the required space beyond the upper limit.
	ErrSpaceLimit = errors.New("xtcp.buffer: required space beyond the upper limit")
	// ErrNegativeCount means that negative count.
	ErrNegativeCount = errors.New("xtcp.buffer: negative count")
)

// A Buffer is a variable-sized buffer of bytes.
type Buffer struct {
	buf     []byte // raw buf.
	or, ow  int    // read/write offset
	maxSize int    // the max size can alloc of raw buf.
}

// UnreadBytes returns a slice of length b.Len() holding the unread portion of the buffer.
// The slice is valid for use only until the next buffer modification.
func (b *Buffer) UnreadBytes() []byte { return b.buf[b.or:b.ow] }

// UnreadLen return the length of unread bytes.
func (b *Buffer) UnreadLen() int { return b.ow - b.or }

// Cap returns the capacity of the buffer's underlying byte slice, that is, the
// total space allocated for the buffer's data.
func (b *Buffer) Cap() int { return cap(b.buf) }

// Advance discard the n bytes from the last read.
// If n is negative return errNegativeCount.
// If the buffer can't advance it will return ErrSpaceLimit.
func (b *Buffer) Advance(n int) ([]byte, error) {
	if n < 0 {
		return nil, ErrNegativeCount
	}

	if b.or+n > b.ow {
		return nil, ErrSpaceLimit
	}

	b.or += n

	return b.buf[b.or-n : b.or], nil
}

// Grow grows the buffer's capacity until to max size.
// After Grow(n), at least n bytes can be written to the
// buffer without another allocation.
// return ErrNegativeCount if n is negative.
// return ErrSpaceLimit if need space size grater than the max size.
// If the buffer can't alloc memory it will panic with ErrNoMemory.
func (b *Buffer) Grow(n int) error {
	if n < 0 {
		return ErrNegativeCount
	}

	if b.or == b.ow && b.or != 0 {
		// reset to recover space.
		b.or = 0
		b.ow = 0
	}

	if b.ow+n > cap(b.buf) {
		lenRemain := b.ow - b.or
		if 2*(lenRemain+n) <= cap(b.buf) {
			// in order to avoid the frequent copy, only copy when (remain+need<=cap/2).
			copy(b.buf[:], b.buf[b.or:b.ow])
		} else {
			// space not enough, create new.
			newSize := cap(b.buf)*2 + n
			if newSize > b.maxSize {
				newSize = b.maxSize
				if newSize-lenRemain < n {
					return ErrSpaceLimit
				}
			}
			nb := makeSlice(newSize)
			copy(nb[:], b.buf[b.or:b.ow])
			b.buf = nb
		}
		b.ow = lenRemain
		b.or = 0
	}
	return nil
}

// TryRead reads data from r to the remain space.
func (b *Buffer) TryRead(r io.Reader) (int, error) {
	n, err := r.Read(b.buf[b.ow:])
	if n > 0 {
		b.ow += n
	}

	return n, err
}

// Write appends the contents of p to the buffer, growing the buffer as
// needed. The return value n is the length of p;
// return error will be ErrSpaceLimit if need space size grater than the max size.
// If the buffer can't alloc memory it will panic with ErrNoMemory.
func (b *Buffer) Write(p []byte) (int, error) {
	lenP := len(p)
	if lenP == 0 {
		// nothing to do.
		return 0, nil
	}
	err := b.Grow(lenP)
	if err != nil {
		return 0, err
	}
	copy(b.buf[b.ow:], p)
	b.ow += lenP
	return lenP, nil
}

// NewBuffer create a new Buffer.
// If initSize == 0 or initSize > maxSize it will return nil.
// If create buffer failed it will panic with ErrNoMemory.
func NewBuffer(initSize, maxSize int) *Buffer {
	if initSize == 0 || initSize > maxSize {
		return nil
	}
	b := &Buffer{
		buf:     makeSlice(initSize),
		maxSize: maxSize,
	}
	return b
}

// makeSlice allocates a slice of size n. If the allocation fails, it panics
// with ErrNoMemory.
func makeSlice(n int) []byte {
	// If the make fails, give a known error.
	defer func() {
		if recover() != nil {
			panic(ErrNoMemory)
		}
	}()
	return make([]byte, n)
}
