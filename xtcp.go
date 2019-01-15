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
	// DefaultAsyncWrite is enable async write or not.
	DefaultAsyncWrite = true
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

// Handler is the event callback.
// Note : don't block in event handler.
type Handler interface {
	// OnAccept mean server accept a new connect.
	OnAccept(*Conn)
	// OnConnect mean client connected to a server.
	OnConnect(*Conn)
	// OnRecv mean conn recv a packet.
	OnRecv(*Conn, Packet)
	// OnUnpackErr mean failed to unpack recved data.
	OnUnpackErr(*Conn, []byte, error)
	// OnClose mean conn is closed.
	OnClose(*Conn)
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
	AsyncWrite      bool          // default is DefaultAsyncWrite  if you don't set.
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
		AsyncWrite:     DefaultAsyncWrite,
		NoDelay:        true,
		KeepAlive:      false,
	}
}
