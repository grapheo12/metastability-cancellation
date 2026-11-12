// Source: abstraction/server/message_buffer/design.md (message_buffer v0.1.0)
package server

import "sync"

// MessageCursorStride spreads the first slot of each client's MessageCursor.
//
// API: abstraction/server/message_buffer/api.md#constants
const MessageCursorStride = 7919

// makeMessage returns a new message whose every byte is n mod 256.
//
// Design: abstraction/server/message_buffer/design.md#make_message
func makeMessage(n uint64, size int) []byte {
	m := make([]byte, size)
	b := byte(n) // n mod 256
	for i := range m {
		m[i] = b
	}
	return m
}

// MessageBuffer is the window of messages shared by every worker, and each
// client's MessageCursor, both guarded by mu.
//
// Design: abstraction/server/message_buffer/design.md#messagebuffer
type MessageBuffer struct {
	mu      sync.Mutex
	slots   [][]byte
	cursors map[uint32]uint32 // client_id -> next slot to overwrite
}

// NewMessageBuffer fills every slot, in slot order, with a new message. It
// runs before the bind; running out of memory here kills the process before
// "listening on".
//
// Design: abstraction/server/message_buffer/design.md#messagebuffer
// Design: abstraction/server/message_buffer/design.md#failure-and-edge-cases
func NewMessageBuffer(windowSize uint32, messageBytes int) *MessageBuffer {
	b := &MessageBuffer{slots: make([][]byte, windowSize), cursors: map[uint32]uint32{}}
	for i := range b.slots {
		b.slots[i] = makeMessage(uint64(i), messageBytes)
	}
	return b
}

// Push writes msgs, already created, into consecutive slots from the client's
// MessageCursor, wrapping at the window size, under the lock.
//
// Design: abstraction/server/message_buffer/design.md#messagecursor
func (b *MessageBuffer) Push(clientID uint32, msgs [][]byte) {
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
