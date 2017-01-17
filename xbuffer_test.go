package xtcp

import (
	"testing"
)

func TestWrite(t *testing.T) {
	buf := NewBuffer(128, 256)
	buf128 := make([]byte, 128)
	wn, err := buf.Write(buf128)
	if err != nil {
		t.Error("write err :", err)
	}
	if wn != 128 {
		t.Error("writen len != return len")
	}

	if buf.UnreadLen() != 128 {
		t.Error("writen len != unread len")
	}

	if len(buf.UnreadBytes()) != 128 {
		t.Error("writen len != unread bytes len")
	}

	if buf.Cap() != 128 {
		t.Error("writen len != buf cap")
	}
}

func TestMaxSize(t *testing.T) {
	buf := NewBuffer(128, 256)
	buf257 := make([]byte, 257)
	wn, err := buf.Write(buf257)
	if err != ErrSpaceLimit || wn != 0 {
		t.Errorf("'ErrSpaceLimit' expected, got %q", err)
	}
}

func TestAdvance(t *testing.T) {
	buf := NewBuffer(128, 128)
	buf128 := make([]byte, 128)
	buf.Write(buf128)
	advBuf, err := buf.Advance(buf.UnreadLen())
	if err != nil {
		t.Error("advance err : ", err)
	}
	if len(advBuf) != 128 {
		t.Error("advance len != return buf len")
	}
	if buf.UnreadLen() != 0 || len(buf.UnreadBytes()) != 0 {
		t.Error("after advance all, unread len != 0")
	}
}

func TestGrow(t *testing.T) {
	buf := NewBuffer(100, 10240)
	buf.Grow(100) // cap: 100
	if buf.Cap() != 100 {
		t.Errorf("100 expected, got %v after cap[100] -> grow[100]", buf.Cap())
		return
	}
	buf.Grow(200) // cap: 2 * 100 + 200
	if buf.Cap() != 400 {
		t.Errorf("400 expected, got %v after cap[100] -> grow[200]", buf.Cap())
		return
	}
	buf.Grow(500) // cap: 2 * 400 + 500
	if buf.Cap() != 1300 {
		t.Errorf("1300 expected, got %v after cap[400] -> grow[500]", buf.Cap())
		return
	}
}
