package xtcp

import (
	"errors"
	"github.com/xfxdev/xlog"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

var (
	errSendToClosedConn = errors.New("send to closed conn")
	errSendEmptyBuf     = errors.New("send buf if empty")
)

// A Conn represents the server side of an tcp connection.
type Conn struct {
	Opts        *Options
	RawConn     net.Conn
	UserData    interface{}
	sendPackets chan Packet
	close       chan struct{}
	state       int32
	wg          sync.WaitGroup
}

// NewConn return new conn.
func NewConn(opts *Options) *Conn {
	return &Conn{
		Opts:        opts,
		sendPackets: make(chan Packet, opts.SendListLen),
		close:       make(chan struct{}),
	}
}

func (c *Conn) String() string {
	return c.RawConn.LocalAddr().String() + " -> " + c.RawConn.RemoteAddr().String()
}

// Stop stops the conn.
// StopImmediately: immediately closes recv and send.
// StopGracefullyButNotWait: stop accept new send, but all send bufs in the send list will continue send.
// StopGracefullyAndWait: stop accept new send, will block until all send bufs in the send list are sended.
func (c *Conn) Stop(mode StopMode) {
	if atomic.LoadInt32(&c.state) == 0 {
		if mode == StopImmediately {
			atomic.StoreInt32(&c.state, 2)
			close(c.close)
			c.RawConn.Close()
		} else {
			atomic.StoreInt32(&c.state, 1)
			close(c.close)
			if mode == StopGracefullyAndWait {
				c.wg.Wait()
			}
		}
	}
}

// IsStoped return true if Conn is closed, otherwise return false.
func (c *Conn) IsStoped() bool {
	return atomic.LoadInt32(&c.state) == 2
}

func (c *Conn) serve() {
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		c.recv()
	}()
	c.send()
	wg.Wait()

	c.Opts.Handler.OnEvent(EventClosed, c, nil)
}

func (c *Conn) recv() {
	//defer xlog.Debug("recv exit.")
	defer c.wg.Done()

	c.wg.Add(1)
	recvBuf := NewBuffer(c.Opts.RecvBufInitSize, c.Opts.RecvBufMaxSize)
	if recvBuf == nil {
		xlog.Error("Conn Recv error: cann't create recv buf")
		return
	}

	var tempDelay time.Duration
	for {
		err := recvBuf.Grow(256)
		if err != nil {
			xlog.Error("Conn Recv error: ", err)
			return
		}
		_, err = recvBuf.TryRead(c.RawConn)
		if err != nil {
			if nerr, ok := err.(net.Error); ok && nerr.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				xlog.Errorf("Conn Recv error: %v; retrying in %v", err, tempDelay)
				time.Sleep(tempDelay)
				continue
			}

			if !c.IsStoped() {
				if err != io.EOF {
					xlog.Error("Conn Recv error: ", err)
				}
				c.Stop(StopImmediately)
			}

			return
		}

		tempDelay = 0

		for {
			if recvBuf.UnreadLen() == 0 {
				// no buf can unpack.
				break
			}
			p, pl, err := c.Opts.Protocol.Unpack(recvBuf.UnreadBytes())
			if err != nil {
				xlog.Error("Protocol unpack error: ", err)
			}

			if pl > 0 {
				_, err = recvBuf.Advance(pl)
				if err != nil {
					xlog.Error("Protocol unpack error: ", err)
				}
			}

			if p != nil {
				c.Opts.Handler.OnEvent(EventRecv, c, p)
			} else {
				break
			}
		}
	}
}

func (c *Conn) sendBuf(buf []byte) error {
	sended := 0
	var tempDelay time.Duration
	for sended < len(buf) {
		wn, err := c.RawConn.Write(buf[sended:])
		if err != nil {
			if nerr, ok := err.(net.Error); ok && nerr.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				xlog.Errorf("Conn Send error: %v; retrying in %v", err, tempDelay)
				time.Sleep(tempDelay)
				continue
			}

			if !c.IsStoped() {
				xlog.Error("Conn Send error: ", err)
				c.Stop(StopImmediately)
			}
			return err
		}
		tempDelay = 0
		sended += wn
	}
	return nil
}

func (c *Conn) send() {
	//defer xlog.Debug("send exit.")
	defer c.wg.Done()

	c.wg.Add(1)

	sendBuf := NewBuffer(256, 2048)

	for {
		select {
		case p := <-c.sendPackets:
			if c.IsStoped() {
				return
			}
			_, err := c.Opts.Protocol.PackTo(p, sendBuf)
			if err != nil {
				xlog.Error("Protocol pack error: ", err)
				continue
			}
			buf, err := sendBuf.Advance(sendBuf.UnreadLen())
			if err != nil {
				xlog.Error("Conn Recv error: ", err)
				continue
			}
			if c.sendBuf(buf) != nil {
				return
			}

			c.Opts.Handler.OnEvent(EventSend, c, p)
		case <-c.close:
			if atomic.LoadInt32(&c.state) != 1 {
				return
			} else if len(c.sendPackets) == 0 {
				// stop when state is closing and send buf list is empty.
				atomic.StoreInt32(&c.state, 2)
				c.RawConn.Close()
				return
			}
		}
	}
}

// Send will use the protocol to pack the Packet.
func (c *Conn) Send(p Packet) error {
	if atomic.LoadInt32(&c.state) == 0 {
		c.sendPackets <- p
		return nil
	}
	return errSendToClosedConn
}

// DialAndServe connects to the addr and serve.
func (c *Conn) DialAndServe(addr string) error {
	rawConn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}

	c.RawConn = rawConn

	c.Opts.Handler.OnEvent(EventConnected, c, nil)

	c.serve()

	return nil
}
