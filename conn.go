package xtcp

import (
	"bytes"
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

// A Conn represents the server side of an tcp connection.
type Conn struct {
	Handler      Handler
	Protocol     Protocol
	RawConn      net.Conn
	UserData     interface{}
	sendPackages chan Package
	close        chan struct{}
	state        int32
	wg           sync.WaitGroup
}

// NewConn return new conn.
func NewConn(h Handler, p Protocol, sendBufLen uint) (*Conn, error) {
	if sendBufLen == 0 {
		sendBufLen = DefaultSendBufLength
	}
	return &Conn{
		Handler:      h,
		Protocol:     p,
		sendPackages: make(chan Package, sendBufLen),
		close:        make(chan struct{}),
	}, nil
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
	c.Handler.OnConnected(c)

	go c.recv()
	c.send()

	c.Handler.OnClosed(c)
}

func (c *Conn) recv() {
	defer c.wg.Done()
	defer xlog.Debugf("recv exit: %v", c.RawConn.RemoteAddr().String())

	c.wg.Add(1)

	var tempDelay time.Duration
	buf := make([]byte, 0, 1024)
	var bufTmp [1024]byte
	for {
		n, err := c.RawConn.Read(bufTmp[:])
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

		buf = append(buf, bufTmp[:n]...)

		for {
			p, pl, err := c.Protocol.Unpack(buf)
			if err != nil {
				xlog.Error("Protocol unpack error: ", err)
			}

			if pl > 0 {
				copy(buf[:], buf[pl:])
				buf = buf[:len(buf)-pl]
			}

			if p != nil {
				p.Conn(c)
				c.Handler.OnRecv(p)
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
	defer c.wg.Done()
	defer xlog.Debugf("send exit: %v", c.RawConn.RemoteAddr().String())

	c.wg.Add(1)

	sendCachedBuf := bytes.NewBuffer(nil)

	for {
		select {
		case p := <-c.sendPackages:
			if c.IsStoped() {
				return
			}
			buf := p.GetCachedPackBuf()
			if buf == nil {
				sendCachedBuf.Reset()
				_, err := c.Protocol.PackTo(p, sendCachedBuf)
				if err == nil {
					xlog.Error("Protocol pack error: ", err)
					continue
				}
			}
			buf = sendCachedBuf.Bytes()
			if c.sendBuf(buf) != nil {
				return
			}
		case <-c.close:
			if atomic.LoadInt32(&c.state) != 1 {
				return
			} else if len(c.sendPackages) == 0 {
				// stop when state is closing and send buf list is empty.
				atomic.StoreInt32(&c.state, 2)
				c.RawConn.Close()
				return
			}
		}
	}
}

// Send will use the protocol to pack the package.
func (c *Conn) Send(p Package) error {
	if atomic.LoadInt32(&c.state) == 0 {
		c.sendPackages <- p
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

	c.serve()

	return nil
}
