package xtcp

import (
	"fmt"
	"io"
	"time"
)

var (
	// DefaultRecvBufSize is the default size of recv buf.
	DefaultRecvBufSize = 4 << 10 // 4k
	// DefaultSendBufListLen is the default length of send buf list.
	DefaultSendBufListLen = 1 << 10 // 1k
)

// StopMode define the stop mode of server and conn.
type StopMode uint8

const (
	// StopImmediately mean stop directly, the cached data maybe will not send.
	StopImmediately StopMode = iota
	// StopGracefullyButNotWait stop and flush cached data.
	StopGracefullyButNotWait
	// StopGracefullyAndWait stop and block until cached data sended.
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
	// EventRecv mean conn recv a packet.
	EventRecv
	// EventClosed mean conn is closed.
	EventClosed
)

// Handler is the event callback.
// p will be nil when event is EventAccept/EventConnected/EventClosed
// Note : don't block in event handler.
type Handler interface {
	OnEvent(et EventType, c *Conn, p Packet)
}

// Packet is the unit of data.
type Packet interface {
	fmt.Stringer
}

// Protocol use to pack/unpack Packet.
type Protocol interface {
	// PackSize return the size need for pack the Packet.
	PackSize(p Packet) int
	// PackTo pack the Packet to w.
	// The return value n is the number of bytes written;
	// Any error encountered during the write is also returned.
	PackTo(p Packet, w io.Writer) (int, error)
	// Pack pack the Packet to new created buf.
	Pack(p Packet) ([]byte, error)
	// Unpack try to unpack the buf to Packet. If return len > 0, then buf[:len] will be discard.
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
	RecvBufSize     int           // default is DefaultRecvBufSize if you don't set.
	SendBufListLen  int           // default is DefaultSendBufListLen if you don't set.
	NoDelay         bool          // default is true
	KeepAlive       bool          // default is false
	KeepAlivePeriod time.Duration // default is 0, mean use system setting.
	ReadDeadline    time.Duration // default is 0, means Read will not time out.
	WriteDeadline   time.Duration // default is 0, means Write will not time out.
}

// NewOpts create a new options and set some default value.
// will panic if handler or protocol is nil.
// eg: opts := NewOpts().SetSendListLen(len).SetRecvBufInitSize(len)...
func NewOpts(h Handler, p Protocol) *Options {
	if h == nil || p == nil {
		panic("xtcp.NewOpts: nil handler or protocol")
	}
	return &Options{
		Handler:        h,
		Protocol:       p,
		RecvBufSize:    DefaultRecvBufSize,
		SendBufListLen: DefaultSendBufListLen,
		NoDelay:        true,
		KeepAlive:      false,
	}
}
