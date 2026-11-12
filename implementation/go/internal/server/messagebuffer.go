// Source: abstraction/server/message_buffer/design.md (message_buffer v0.2.0)
package server

import (
	"sync"

	"testbed/internal/sampling"
)

// MessageCursorStride spreads the first slot of each client's MessageCursor.
//
// API: abstraction/server/message_buffer/api.md#constants
const MessageCursorStride = 7919

// TreeNode is one node of a MessageTree: 24 bytes, two of them pointers, so
// the collector must scan every node.
//
// API: abstraction/server/message_buffer/api.md#treenode
type TreeNode struct {
	Value       uint64
	Left, Right *TreeNode
}

// makeTree returns a new random tree of exactly n nodes (nil for 0), drawing
// in pre-order: value, left size, left subtree, right subtree.
//
// Design: abstraction/server/message_buffer/design.md#messagetree
func makeTree(rng *sampling.Rng, n int) *TreeNode {
	if n == 0 {
		return nil
	}
	node := &TreeNode{Value: rng.NextU64()}
	k := int(rng.UniformInt(0, uint64(n-1))) // nodes in the left subtree
	node.Left = makeTree(rng, k)
	node.Right = makeTree(rng, n-1-k)
	return node
}

// MessageBuffer is the window of messages shared by every worker, and each
// client's MessageCursor, both guarded by mu.
//
// Design: abstraction/server/message_buffer/design.md#messagebuffer
type MessageBuffer struct {
	mu      sync.Mutex
	slots   []*TreeNode       // MessageTree roots
	cursors map[uint32]uint32 // client_id -> next slot to overwrite
}

// NewMessageBuffer fills every slot, in slot order, with a new message. It
// runs before the bind; running out of memory here kills the process before
// "listening on".
//
// Design: abstraction/server/message_buffer/design.md#messagebuffer
// Design: abstraction/server/message_buffer/design.md#failure-and-edge-cases
func NewMessageBuffer(windowSize uint32, treeSize int, seed uint64) *MessageBuffer {
	b := &MessageBuffer{slots: make([]*TreeNode, windowSize), cursors: map[uint32]uint32{}}
	rng := sampling.NewRng(sampling.DeriveSeed(seed, sampling.StreamServerPrefill, 0))
	for i := range b.slots {
		b.slots[i] = makeTree(rng, treeSize) // in slot order
	}
	return b
}

// Push writes msgs, already created, into consecutive slots from the client's
// MessageCursor, wrapping at the window size, under the lock.
//
// Design: abstraction/server/message_buffer/design.md#messagecursor
func (b *MessageBuffer) Push(clientID uint32, msgs []*TreeNode) {
	w := uint64(len(b.slots))
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.cursors[clientID]
	if !ok {
		c = uint32(uint64(clientID) * MessageCursorStride % w) // first push: spread start
	}
	for _, m := range msgs {
		b.slots[c] = m // the message in that slot becomes garbage
		c = uint32((uint64(c) + 1) % w)
	}
	b.cursors[clientID] = c
}
