package xtcp

import (
	"fmt"
	"io"
)

var (
	// DefaultSendListLen is the default length of send list.
	DefaultSendListLen = 16 // channel size
	// DefaultRecvBufInitSize is the default init size of recv buf.
	DefaultRecvBufInitSize = 1 << 10 // 1k
	// DefaultRecvBufMaxSize is the default max size of recv buf.
	DefaultRecvBufMaxSize = 4 << 10 // 4k
)

// StopMode define the stop mode of server and conn.
type StopMode uint8

const (
	// StopImmediately mean stop directly.
	StopImmediately StopMode = iota
	// StopGracefullyButNotWait mean stop gracefully but not wait.
	StopGracefullyButNotWait
	// StopGracefullyAndWait mean stop and wait.
	StopGracefullyAndWait
)

// EventType is the conn event type.
type EventType int

func (et EventType) String() string {
	switch et {
	case EventAccept:
		return "accept"
	case EventConnected:
		return "connected"
	case EventSend:
		return "send"
	case EventRecv:
		return "recv"
	case EventClosed:
		return "closed"
	default:
		return "<unknown xtcp event>"
	}
}

const (
	// EventAccept mean server accept a new connect.
	EventAccept EventType = iota
	// EventConnected mean client connected to a server.
	EventConnected
	// EventSend mean conn send a packet.
	EventSend
	// EventRecv mean conn recv a packet.
	EventRecv
	// EventClosed mean conn is closed.
	EventClosed
)

// Handler is the event callback.
// p will be nil when event is EventAccept/EventConnected/EventClosed
type Handler interface {
	OnEvent(et EventType, c *Conn, p Packet)
}

// Packet is the unit of data.
type Packet interface {
	fmt.Stringer
}

// Protocol use to pack/unpack Packet.
type Protocol interface {
	// return the size need for pack the Packet.
	PackSize(p Packet) int
	// PackTo pack the Packet to w.
	// The return value n is the number of bytes written;
	// Any error encountered during the write is also returned.
	PackTo(p Packet, w io.Writer) (int, error)
	// Pack pack the Packet to new created buf.
	Pack(p Packet) ([]byte, error)
	// try to unpack the buf to Packet. If return len > 0, then buf[:len] will be discard.
	// The following return conditions must be implement:
	// (nil, 0, nil) : buf size not enough for unpack one Packet.
	// (nil, len, err) : buf size enough but error encountered.
	// (p, len, nil) : unpack succeed.
	Unpack(buf []byte) (Packet, int, error)
}

// Options is the options used for net conn.
type Options struct {
	Handler         Handler
	Protocol        Protocol
	SendListLen     int // default is DefaultSendListLen if you don't set.
	RecvBufInitSize int // default is DefaultRecvBufInitSize if you don't set.
	RecvBufMaxSize  int // default is DefaultRecvBufMaxSize if you don't set.
}

// NewOpts create a new options and set some default value.
// will panic if handler or protocol is nil.
// eg: opts := NewOpts().SetSendListLen(len).SetRecvBufInitSize(len)...
func NewOpts(h Handler, p Protocol) *Options {
	if h == nil || p == nil {
		panic("xtcp.NewOpts: nil handler or protocol")
	}
	return &Options{
		Handler:         h,
		Protocol:        p,
		SendListLen:     DefaultSendListLen,
		RecvBufInitSize: DefaultRecvBufInitSize,
		RecvBufMaxSize:  DefaultRecvBufMaxSize,
	}
}

// SetSendListLen set init size of the recv buf, 0 mean DefaultSendListLen.
func (opts *Options) SetSendListLen(len int) *Options {
	if len < 0 {
		panic("xtcp.Options.SetSendListLen: negative size")
	}
	opts.SendListLen = len
	return opts
}

// SetRecvBufInitSize set init size of the recv buf, 0 mean DefaultRecvBufInitSize.
func (opts *Options) SetRecvBufInitSize(s int) *Options {
	if s < 0 {
		panic("xtcp.Options.SetRecvBufInitSize: negative size")
	}
	opts.RecvBufInitSize = s
	return opts
}

// SetRecvBufMaxSize set max size of the recv buf, 0 mean DefaultRecvBufMaxSize.
func (opts *Options) SetRecvBufMaxSize(s int) *Options {
	if s < 0 {
		panic("xtcp.Options.SetRecvBufMaxSize: negative size")
	}
	opts.RecvBufMaxSize = s
	return opts
}
