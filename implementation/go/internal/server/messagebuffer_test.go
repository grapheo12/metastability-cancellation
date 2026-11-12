// Source: abstraction/server/message_buffer/design.md (message_buffer v0.1.0)
package server

import (
	"bytes"
	"testing"
)

// Design: abstraction/server/message_buffer/design.md#make_message
// Design: abstraction/server/message_buffer/design.md#messagebuffer
func TestMakeMessageAndFill(t *testing.T) {
	if m := makeMessage(0x105, 3); !bytes.Equal(m, []byte{5, 5, 5}) {
		t.Fatalf("makeMessage: %v", m)
	}
	b := NewMessageBuffer(300, 2)
	for i, s := range b.slots {
		if len(s) != 2 || s[0] != byte(i) || s[1] != byte(i) {
			t.Fatalf("slot %d: %v", i, s)
		}
	}
}

// Design: abstraction/server/message_buffer/design.md#messagecursor
// Design: abstraction/server/message_buffer/design.md#worked-examples
func TestMessageCursorWorkedExample(t *testing.T) {
	b := NewMessageBuffer(10, 1)
	msg := func(c byte) []byte { return []byte{c} }
	b.Push(1, [][]byte{msg('a'), msg('b'), msg('c')})
	b.Push(0, [][]byte{msg('d'), msg('e')})
	b.Push(1, [][]byte{msg('f')})
	b.Push(3, [][]byte{msg('g'), msg('h'), msg('i'), msg('j')})
	want := map[int]byte{0: 'j', 1: 'e', 2: 'f', 7: 'g', 8: 'h', 9: 'i'}
	for i, s := range b.slots {
		w, ok := want[i]
		if !ok {
			w = byte(i) // untouched since the fill
		}
		if s[0] != w {
			t.Fatalf("slot %d = %q want %q", i, s[0], w)
		}
	}
	for c, cur := range map[uint32]uint32{0: 2, 1: 3, 3: 1} {
		if b.cursors[c] != cur {
			t.Fatalf("cursor of client %d = %d want %d", c, b.cursors[c], cur)
		}
	}
	one := NewMessageBuffer(1, 1)
	one.Push(5, [][]byte{msg('x'), msg('y')})
	if one.slots[0][0] != 'y' || one.cursors[5] != 0 {
		t.Fatalf("W = 1: %q %d", one.slots[0][0], one.cursors[5])
	}
}
