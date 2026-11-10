// Package h2client opens dedicated h2c (cleartext, prior-knowledge) client
// connections: the open_h2c_connection primitive of the load generator, the
// Ingress Upstream Pool and the Ingress Load Canceller.
//
// Source: abstraction/load_generator/run_controller/design.md (run_controller v0.1.1),
// abstraction/ingress/upstream_pool/design.md (upstream_pool v0.1.1),
// abstraction/ingress/load_canceller/design.md (load_canceller v0.3.0)
package h2client

import (
	"context"
	"net"
	"time"

	"golang.org/x/net/http2"
)

// Conn is one HTTP/2 connection owned by a single user (a client, an
// upstream pair, or the canceller).
type Conn struct {
	CC        *http2.ClientConn
	Authority string // the target parameter, used as :authority
}

// Dial opens a TCP connection, sends the HTTP/2 preface and waits for a PING
// round trip, so the server has accepted the connection and SETTINGS are exchanged.
//
// Design: abstraction/load_generator/run_controller/design.md#startup
// Design: abstraction/ingress/load_canceller/design.md#startup
// Design: abstraction/ingress/upstream_pool/design.md#get
func Dial(target string) (*Conn, error) {
	tcp, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		return nil, err
	}
	cc, err := (&http2.Transport{AllowHTTP: true}).NewClientConn(tcp)
	if err != nil {
		_ = tcp.Close()
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cc.Ping(ctx); err != nil {
		_ = cc.Close()
		return nil, err
	}
	return &Conn{CC: cc, Authority: target}, nil
}

// Close sends GOAWAY (best effort, bounded wait) and closes the connection.
// Errors are ignored.
//
// Design: abstraction/load_generator/run_controller/design.md#shutdown-ordering
// Design: abstraction/ingress/load_canceller/design.md#cancellerdrain
func (c *Conn) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	_ = c.CC.Shutdown(ctx)
	cancel()
	_ = c.CC.Close()
}
