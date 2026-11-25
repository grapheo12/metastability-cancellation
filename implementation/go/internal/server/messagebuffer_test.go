// Source: abstraction/server/message_buffer/design.md (message_buffer v0.2.1)
package server

import (
	"testing"

	"testbed/internal/sampling"
)

// size counts a tree's nodes; shape records (value, left size) in pre-order.
func size(n *TreeNode) int {
	if n == nil {
		return 0
	}
	return 1 + size(n.Left) + size(n.Right)
}

func shape(n *TreeNode, out *[][2]uint64) {
	if n == nil {
		return
	}
	*out = append(*out, [2]uint64{n.Value, uint64(size(n.Left))})
	shape(n.Left, out)
	shape(n.Right, out)
}

// Exact size for every n, and the pre-order draw order: value, left size,
// left subtree, right subtree, read back from an identical copy of the Rng.
//
// Design: abstraction/server/message_buffer/design.md#messagetree
// Design: abstraction/server/message_buffer/design.md#worked-examples
func TestMakeTree(t *testing.T) {
	for _, n := range []int{0, 1, 4, 15, 1000, 65536} {
		rng := sampling.NewRng(42)
		if got := size(makeTree(rng, n)); got != n {
			t.Fatalf("n = %d: %d nodes", n, got)
		}
	}
	for _, n := range []int{1, 4, 15} {
		rng := sampling.NewRng(7)
		ref := *rng // the same state
		var got [][2]uint64
		shape(makeTree(rng, n), &got)
		// Replay the draws in the order the spec gives.
		var want [][2]uint64
		var replay func(m int)
		replay = func(m int) {
			if m == 0 {
				return
			}
			v := ref.NextU64()
			k := ref.UniformInt(0, uint64(m-1))
			want = append(want, [2]uint64{v, k})
			replay(int(k))
			replay(m - 1 - int(k))
		}
		replay(n)
		if len(got) != len(want) || *rng != ref {
			t.Fatalf("n = %d: draw count differs", n)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("n = %d node %d: %v want %v", n, i, got[i], want[i])
			}
		}
	}
	leaf := makeTree(sampling.NewRng(1), 1)
	if leaf.Left != nil || leaf.Right != nil {
		t.Fatal("n = 1 is a single node")
	}
}

// Design: abstraction/server/message_buffer/design.md#messagebuffer
func TestFillFromPrefillStream(t *testing.T) {
	b := NewMessageBuffer(300, 5, 9)
	rng := sampling.NewRng(sampling.DeriveSeed(9, sampling.StreamServerPrefill, 0))
	for i, s := range b.slots {
		want := makeTree(rng, 5) // slot order
		if size(s) != 5 || s.Value != want.Value {
			t.Fatalf("slot %d differs from the prefill stream", i)
		}
	}
	if c := NewMessageBuffer(300, 5, 9); c.slots[299].Value != b.slots[299].Value {
		t.Fatal("same seed must give the same window")
	}
}

// Design: abstraction/server/message_buffer/design.md#messagecursor
// Design: abstraction/server/message_buffer/design.md#worked-examples
func TestMessageCursorWorkedExample(t *testing.T) {
	b := NewMessageBuffer(10, 1, 0)
	before := append([]*TreeNode(nil), b.slots...)
	msg := func(c byte) *TreeNode { return &TreeNode{Value: uint64(c)} }
	b.Push(1, []*TreeNode{msg('a'), msg('b'), msg('c')})
	b.Push(0, []*TreeNode{msg('d'), msg('e')})
	b.Push(1, []*TreeNode{msg('f')})
	b.Push(3, []*TreeNode{msg('g'), msg('h'), msg('i'), msg('j')})
	want := map[int]byte{0: 'j', 1: 'e', 2: 'f', 7: 'g', 8: 'h', 9: 'i'}
	for i, s := range b.slots {
		if w, ok := want[i]; ok {
			if s.Value != uint64(w) {
				t.Fatalf("slot %d = %q want %q", i, rune(s.Value), w)
			}
		} else if s != before[i] {
			t.Fatalf("slot %d must be untouched since the fill", i)
		}
	}
	for c, cur := range map[uint32]uint32{0: 2, 1: 3, 3: 1} {
		if b.cursors[c] != cur {
			t.Fatalf("cursor of client %d = %d want %d", c, b.cursors[c], cur)
		}
	}
	one := NewMessageBuffer(1, 1, 0)
	one.Push(5, []*TreeNode{msg('x'), msg('y')})
	if one.slots[0].Value != 'y' || one.cursors[5] != 0 {
		t.Fatalf("W = 1: %v %d", one.slots[0].Value, one.cursors[5])
	}
}

// The concrete worked example (seed 0, n = 4) of the spec.
//
// Design: abstraction/server/message_buffer/design.md#worked-examples
func TestMessageTreeConcreteExample(t *testing.T) {
	for _, c := range []struct {
		seed uint64
		want [][2]uint64
	}{
		{sampling.DeriveSeed(0, sampling.StreamServerPrefill, 0), [][2]uint64{
			{3347217672396778586, 2}, {11281592653131264720, 0}, {9616498591242348923, 0}, {1901490388733148195, 0}}},
		{sampling.DeriveSeed(sampling.DeriveSeed(0, sampling.StreamServerMessages, 2), sampling.StreamServerMessages, 5),
			[][2]uint64{{4208604537885474736, 3}, {206431563091969346, 2}, {2947458949422951496, 1}, {15453664076641952497, 0}}},
	} {
		var got [][2]uint64
		shape(makeTree(sampling.NewRng(c.seed), 4), &got)
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Fatalf("seed %d node %d: %v want %v", c.seed, i, got[i], c.want[i])
			}
		}
	}
	if sampling.DeriveSeed(0, sampling.StreamServerPrefill, 0) != 3061075853855529836 {
		t.Fatal("prefill stream seed")
	}
}
