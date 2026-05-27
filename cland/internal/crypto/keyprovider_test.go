package crypto

import (
	"bytes"
	"testing"
	"time"
)

func TestNewSimpleKeyProvider_RejectsEmpty(t *testing.T) {
	if _, err := NewSimpleKeyProvider(nil); err == nil {
		t.Errorf("nil key should error")
	}
	if _, err := NewSimpleKeyProvider([]byte{}); err == nil {
		t.Errorf("empty key should error")
	}
}

func TestSimpleKeyProvider_CurrentGrace(t *testing.T) {
	k := []byte("the-first-key")
	p, err := NewSimpleKeyProvider(k)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.Current(), k) {
		t.Errorf("Current() mismatch")
	}
	if _, ok := p.Grace(); ok {
		t.Errorf("grace should be absent initially")
	}
}

func TestSimpleKeyProvider_Rotate(t *testing.T) {
	old := []byte("old-key")
	p, _ := NewSimpleKeyProvider(old)

	newKey := []byte("new-key")
	if err := p.Rotate(newKey, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.Current(), newKey) {
		t.Errorf("Current should be newKey after Rotate")
	}
	g, ok := p.Grace()
	if !ok {
		t.Fatalf("grace should be present during window")
	}
	if !bytes.Equal(g, old) {
		t.Errorf("grace should be the old key")
	}
}

func TestSimpleKeyProvider_RotateRejectsEmpty(t *testing.T) {
	p, _ := NewSimpleKeyProvider([]byte("x"))
	if err := p.Rotate(nil, time.Hour); err == nil {
		t.Errorf("Rotate(nil) should error")
	}
}

func TestSimpleKeyProvider_GraceExpires(t *testing.T) {
	old := []byte("old")
	p, _ := NewSimpleKeyProvider(old)
	if err := p.Rotate([]byte("new"), 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Grace(); !ok {
		t.Errorf("grace should be present immediately after rotate")
	}
	time.Sleep(40 * time.Millisecond)
	if _, ok := p.Grace(); ok {
		t.Errorf("grace should have expired")
	}
}

func TestSimpleKeyProvider_DropGrace(t *testing.T) {
	p, _ := NewSimpleKeyProvider([]byte("old"))
	_ = p.Rotate([]byte("new"), time.Hour)
	p.DropGrace()
	if _, ok := p.Grace(); ok {
		t.Errorf("DropGrace should clear grace immediately")
	}
}

func TestSimpleKeyProvider_CurrentReturnsCopy(t *testing.T) {
	k := []byte("immutable")
	p, _ := NewSimpleKeyProvider(k)
	got := p.Current()
	got[0] = 'X'
	if !bytes.Equal(p.Current(), k) {
		t.Errorf("mutating returned slice changed internal state")
	}
}
