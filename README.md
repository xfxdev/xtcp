# xtcp

A TCP Server Framework with graceful shutdown,custom protocol.

[![Build Status](https://travis-ci.org/xfxdev/xtcp.svg?branch=master)](https://travis-ci.org/xfxdev/xtcp)
[![Go Report Card](https://goreportcard.com/badge/github.com/xfxdev/xtcp)](https://goreportcard.com/report/github.com/xfxdev/xtcp)
[![GoDoc](https://godoc.org/github.com/xfxdev/xtcp?status.svg)](https://godoc.org/github.com/xfxdev/xtcp)


## Install

~~~
go get github.com/xfxdev/xlog // xtcp use xlog inner.
go get github.com/xfxdev/xtcp
~~~

## Usage

### define your protocol format:
Before create server and client, you need define the protocol format first.
~~~
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
~~~

### provide event handler:
In xtcp, there are some events to notify the state of net conn, you can handle them according your need:
~~~
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
~~~
To handle the event, just implement the OnEvent interface.
~~~
// Handler is the event callback.
// p will be nil when event is EventAccept/EventConnected/EventClosed
type Handler interface {
	OnEvent(et EventType, c *Conn, p Packet)
}
~~~

### create server:
~~~
// 1. create protocol and handler.
// ...

// 2. create opts.
opts := xtcp.NewOpts(handler, protocol)

// 3. create server.
server := xtcp.NewServer(opts)

// 4. start.
// note : ListenAndServe is a **block** function, if you don't want block, just run it in a goroutine.
// go function() {
//     server.ListenAndServe("addr")
// }()
server.ListenAndServe("addr")
~~~

### create client:
~~~
// 1. create protocol and handler.
// ...

// 2. create opts.
opts := xtcp.NewOpts(handler, protocol)

// 3. create client.
client := NewConn(opts)

// 4. start
// note : DialAndServe is a **block** function, if you don't want block, just run it in a goroutine.
// go function() {
//     client.DialAndServe("addr")
// }()
client.DialAndServe("addr")
~~~

### send and recv packet.
To send a packet, just call the 'Send' function of Conn. You can safe call it in any goroutines.
**Note** : Conn has a packets channel for send, so Send will **block** when the packets channel is full.
You can set the channel length in the Options when create server or client.
~~~
func (c *Conn) Send(p Packet) error
~~~

To recv a packet, implement your handler function:
~~~
func (h *myhandler) OnEvent(et EventType, c *Conn, p Packet) {
	switch et {
		case EventRecv:
			...
	}
}
~~~

### stop
xtcp have three stop modes, stop gracefully mean conn will stop until all the packets in the send channel sended.
~~~
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
~~~

## Example
The example define a protocol format which use protobuf inner.
You can see how to define the protocol and how to create server and client.

[example](https://github.com/xfxdev/xtcp/tree/master/example)
