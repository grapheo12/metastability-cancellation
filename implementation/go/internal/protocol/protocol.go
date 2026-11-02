// Package protocol implements the /ping and /cancel wire formats shared by
// the server, the load generator and the load canceller.
//
// Source: abstraction/protocol/design.md (protocol v0.3.0)
package protocol

import (
	"encoding/json"
	"errors"
	"strconv"
)

// API: abstraction/protocol/api.md#constants
const (
	PingPath               = "/ping"
	PingMethod             = "POST"
	CancelPath             = "/cancel"
	CancelMethod           = "DELETE"
	ContentTypeJSON        = "application/json"
	StatusOK               = 202
	StatusRejected         = 503
	StatusBadRequest       = 400
	StatusNotFound         = 404
	StatusMethodNotAllowed = 405
	RSTCodeCancel          = 0x8
	MinBodyBytes           = 42
)

// ErrParse is returned by ParseBody for any body that does not carry a valid RequestId.
var ErrParse = errors.New("protocol: unparseable body")

// MakeRequestID packs a client id (high 32 bits) and a sequence number (low 32 bits).
//
// Design: abstraction/protocol/design.md#requestid
func MakeRequestID(seq, clientID uint32) uint64 {
	return uint64(clientID)<<32 | uint64(seq)
}

// SplitRequestID is the inverse of MakeRequestID.
//
// Design: abstraction/protocol/design.md#requestid
func SplitRequestID(id uint64) (seq, clientID uint32) {
	return uint32(id & 0xFFFFFFFF), uint32(id >> 32)
}

// bodyFixedOverhead is len(`{"id":"`) + len(`","payload":"`) + len(`"}`) = 7 + 13 + 2.
//
// Design: abstraction/protocol/design.md#paddedjsonbody
const bodyFixedOverhead = 22

// BuildBody returns {"id":"<dec>","payload":"xxx..."} of exactly totalBytes bytes.
//
// Design: abstraction/protocol/design.md#paddedjsonbody
func BuildBody(id uint64, totalBytes int) []byte {
	dec := strconv.FormatUint(id, 10)
	k := totalBytes - bodyFixedOverhead - len(dec)
	if k < 0 {
		panic("protocol: body size below MinBodyBytes")
	}
	b := make([]byte, 0, totalBytes)
	b = append(b, `{"id":"`...)
	b = append(b, dec...)
	b = append(b, `","payload":"`...)
	for i := 0; i < k; i++ {
		b = append(b, 'x')
	}
	b = append(b, `"}`...)
	return b
}

// ValidateBodySize rejects sizes that cannot hold the longest (20-digit) id.
//
// Design: abstraction/protocol/design.md#minimum-body-size
func ValidateBodySize(totalBytes int) error {
	if totalBytes < MinBodyBytes {
		return errors.New("body size must be >= 42 bytes")
	}
	return nil
}

// BuildCancelBody returns the unpadded /cancel body {"id":"<dec>"}.
//
// Design: abstraction/protocol/design.md#cancelbody
func BuildCancelBody(id uint64) []byte {
	return []byte(`{"id":"` + strconv.FormatUint(id, 10) + `"}`)
}

// BuildCancelReply returns the CancelReplyBody of a 202 answer to /cancel:
// {"finished":"<dec>"} when ok is true, {"finished":null} otherwise.
//
// Design: abstraction/protocol/design.md#cancelreplybody
func BuildCancelReply(finished uint64, ok bool) []byte {
	if !ok {
		return []byte(`{"finished":null}`)
	}
	return []byte(`{"finished":"` + strconv.FormatUint(finished, 10) + `"}`)
}

// ParseCancelReply extracts the FinishedWatermark from a CancelReplyBody.
// ok is false for a JSON null; err is ErrParse for anything else that is not a decimal string.
//
// Design: abstraction/protocol/design.md#cancelreplybody
func ParseCancelReply(body []byte) (finished uint64, ok bool, err error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		return 0, false, ErrParse
	}
	raw, present := obj["finished"]
	if !present {
		return 0, false, ErrParse
	}
	if string(raw) == "null" {
		return 0, false, nil
	}
	n, err := parseDecimalString(raw)
	if err != nil {
		return 0, false, ErrParse
	}
	return n, true, nil
}

// parseDecimalString decodes a JSON string of 1..20 ASCII digits that fits in a u64.
//
// Design: abstraction/protocol/design.md#parsing
func parseDecimalString(raw json.RawMessage) (uint64, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, ErrParse
	}
	if len(s) == 0 || len(s) > 20 {
		return 0, ErrParse
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, ErrParse
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, ErrParse
	}
	return n, nil
}

// ParseBody leniently extracts the RequestId from a PaddedJsonBody or a CancelBody.
// Keys are matched case-sensitively; "payload" and any other field are ignored.
//
// Design: abstraction/protocol/design.md#parsing
func ParseBody(body []byte) (uint64, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		return 0, ErrParse
	}
	raw, ok := obj["id"]
	if !ok {
		return 0, ErrParse
	}
	return parseDecimalString(raw)
}
