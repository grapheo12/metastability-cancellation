// Source: abstraction/ingress/upstream_pool/design.md (upstream_pool v0.1.2)
package ingress

import (
	"sync"

	"testbed/internal/h2client"
)

// UpstreamDialTimeoutMs bounds one upstream dial. h2client.Dial applies the
// same 5 s bound to the TCP connect and to the PING round trip.
//
// API: abstraction/ingress/upstream_pool/api.md#constants
const UpstreamDialTimeoutMs = 5000

type pairKey struct{ client, server uint32 }

// upstreamSlot holds at most one open connection for one (client, backend).
type upstreamSlot struct {
	mu   sync.Mutex
	conn *h2client.Conn
}

// UpstreamPool keeps one h2c connection per (client_id, server_id), so the
// servers' per-connection client binding and serial worker hold.
//
// Design: abstraction/ingress/upstream_pool/design.md#upstreampool
// Design: abstraction/ingress/design.md#why-one-upstream-connection-per-client-and-backend
type UpstreamPool struct {
	backends []string
	dial     func(target string) (*h2client.Conn, error) // h2client.Dial; replaceable in tests
	mapMu    sync.Mutex
	slots    map[pairKey]*upstreamSlot
}

// NewUpstreamPool dials nothing.
//
// Design: abstraction/ingress/upstream_pool/design.md#upstreampool
func NewUpstreamPool(backends []string) *UpstreamPool {
	return &UpstreamPool{backends: backends, dial: h2client.Dial, slots: map[pairKey]*upstreamSlot{}}
}

func (p *UpstreamPool) slotFor(c, s uint32) *upstreamSlot {
	p.mapMu.Lock()
	defer p.mapMu.Unlock()
	k := pairKey{c, s}
	sl := p.slots[k]
	if sl == nil {
		sl = &upstreamSlot{}
		p.slots[k] = sl
	}
	return sl
}

// Get returns the pair's connection, dialing it first if needed. The slot
// lock is held during the dial, so concurrent first calls share one dial.
// A failed dial is not cached.
//
// Design: abstraction/ingress/upstream_pool/design.md#get
func (p *UpstreamPool) Get(c, s uint32) (*h2client.Conn, error) {
	sl := p.slotFor(c, s)
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if sl.conn != nil {
		return sl.conn, nil
	}
	conn, err := p.dial(p.backends[s])
	if err != nil {
		return nil, err
	}
	sl.conn = conn
	return conn, nil
}

// Discard drops conn from the pair's slot if it is still there, then closes it.
//
// Design: abstraction/ingress/upstream_pool/design.md#get
func (p *UpstreamPool) Discard(c, s uint32, conn *h2client.Conn) {
	sl := p.slotFor(c, s)
	sl.mu.Lock()
	if sl.conn == conn {
		sl.conn = nil
	}
	sl.mu.Unlock()
	conn.Close()
}

// CloseAll sends GOAWAY on and closes every connection.
//
// Design: abstraction/ingress/upstream_pool/design.md#upstreampool
func (p *UpstreamPool) CloseAll() {
	p.mapMu.Lock()
	all := make([]*upstreamSlot, 0, len(p.slots))
	for _, sl := range p.slots {
		all = append(all, sl)
	}
	p.mapMu.Unlock()
	for _, sl := range all {
		sl.mu.Lock()
		if sl.conn != nil {
			sl.conn.Close()
			sl.conn = nil
		}
		sl.mu.Unlock()
	}
}
