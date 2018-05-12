package xtcp

import (
	"bytes"
	"errors"
	log "github.com/xfxdev/xlog"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

var (
	errSendToClosedConn = errors.New("send to closed conn")
	bufferPool          = sync.Pool{}
)

const (
	connStateNormal int32 = iota
	connStateStopping
	connStateStopped
)

// A Conn represents the server side of an tcp connection.
type Conn struct {
	Opts           *Options
	RawConn        net.Conn
	UserData       interface{}
	sendBuffer     *bytes.Buffer
	sendBufferCond *sync.Cond
	state          int32
	wg             sync.WaitGroup
	once           sync.Once
	SendDropped    uint32
	sendBytes      uint64
	recvBytes      uint64
}

// NewConn return new conn.
func NewConn(opts *Options) *Conn {
	if opts.RecvBufSize <= 0 {
		log.Warnf("Invalid Opts.RecvBufSize : %v, use DefaultRecvBufSize instead", opts.RecvBufSize)
		opts.RecvBufSize = DefaultRecvBufSize
	}
	c := &Conn{
		Opts:           opts,
		sendBufferCond: sync.NewCond(&sync.Mutex{}),
		state:          connStateNormal,
	}
	c.sendBuffer = c.getBufferFromPool()

	return c
}

func (c *Conn) getBufferFromPool() *bytes.Buffer {
	itr := bufferPool.Get()
	if itr != nil {
		buffer := itr.(*bytes.Buffer)
		buffer.Reset()
		return buffer
	}
	return bytes.NewBuffer(make([]byte, 0, c.Opts.RecvBufSize))
}

func (c *Conn) String() string {
	return c.RawConn.LocalAddr().String() + " -> " + c.RawConn.RemoteAddr().String()
}

func (c *Conn) SendBytes() uint64 {
	return atomic.LoadUint64(&c.sendBytes)
}

func (c *Conn) RecvBytes() uint64 {
	return atomic.LoadUint64(&c.recvBytes)
}

// Stop stops the conn.
func (c *Conn) Stop(mode StopMode) {
	c.once.Do(func() {
		if mode == StopImmediately {
			atomic.StoreInt32(&c.state, connStateStopped)
			c.RawConn.Close()
			c.sendBufferCond.Signal() // notify sendLoop to exit wait, call after state changed.
		} else {
			atomic.StoreInt32(&c.state, connStateStopping)
			// c.RawConn.Close() // will close in sendLoop
			c.sendBufferCond.Signal() // notify sendLoop to exit wait, call after state changed.
			if mode == StopGracefullyAndWait {
				c.wg.Wait()
			}
		}
	})
}

// IsStoped return true if Conn is stopped, otherwise return false.
func (c *Conn) IsStoped() bool {
	return atomic.LoadInt32(&c.state) != connStateNormal
}

func (c *Conn) serve() {
	tcpConn := c.RawConn.(*net.TCPConn)
	tcpConn.SetNoDelay(c.Opts.NoDelay)
	tcpConn.SetKeepAlive(c.Opts.KeepAlive)
	if c.Opts.KeepAlivePeriod != 0 {
		tcpConn.SetKeepAlivePeriod(c.Opts.KeepAlivePeriod)
	}

	c.wg.Add(2)
	go c.sendLoop()
	c.recvLoop()

	c.Opts.Handler.OnEvent(EventClosed, c, nil)
}

func (c *Conn) recvLoop() {
	var tempDelay time.Duration
	tempBuf := make([]byte, c.Opts.RecvBufSize)
	recvBuf := c.getBufferFromPool()
	maxDelay := 1 * time.Second

	defer func() {
		log.Debug("XTCP - Conn recv loop exit : ", c.RawConn.RemoteAddr())

		bufferPool.Put(recvBuf)
		c.wg.Done()
	}()

	for {
		if c.Opts.ReadDeadline != 0 {
			c.RawConn.SetReadDeadline(time.Now().Add(c.Opts.ReadDeadline))
		}

		n, err := c.RawConn.Read(tempBuf)
		if err != nil {
			if nerr, ok := err.(net.Error); ok {
				if nerr.Timeout() {
					// timeout
				} else if nerr.Temporary() {
					if tempDelay == 0 {
						tempDelay = 5 * time.Millisecond
					} else {
						tempDelay *= 2
					}
					if tempDelay > maxDelay {
						tempDelay = maxDelay
					}
					log.Errorf("XTCP - Conn[%v] recv error : %v; retrying in %v", c.RawConn.RemoteAddr(), err, tempDelay)
					time.Sleep(tempDelay)
					continue
				}
			}

			if !c.IsStoped() {
				if err != io.EOF {
					log.Errorf("XTCP - Conn[%v] recv error : ", c.RawConn.RemoteAddr(), err)
				}
				c.Stop(StopImmediately)
			}

			return
		}

		recvBuf.Write(tempBuf[:n])
		atomic.AddUint64(&c.recvBytes, uint64(n))
		tempDelay = 0

		for recvBuf.Len() > 0 {
			p, pl, err := c.Opts.Protocol.Unpack(recvBuf.Bytes())
			if err != nil {
				buf := recvBuf.Bytes()
				if len(buf) > 128 {
					buf = buf[:128]
				}
				log.Errorf("XTCP - Conn[%v] protocol unpack error: %v, BufLen : %v, Buf : %v", c.RawConn.RemoteAddr(), err, len(recvBuf.Bytes()), buf)
			}

			if pl > 0 {
				_ = recvBuf.Next(pl)
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
	maxDelay := 1 * time.Second
	for sended < len(buf) {
		if c.Opts.WriteDeadline != 0 {
			c.RawConn.SetWriteDeadline(time.Now().Add(c.Opts.WriteDeadline))
		}
		wn, err := c.RawConn.Write(buf[sended:])
		sended += wn
		atomic.AddUint64(&c.sendBytes, uint64(wn))

		if err != nil {
			if nerr, ok := err.(net.Error); ok {
				if nerr.Timeout() {
					// timeout
				} else if nerr.Temporary() {
					if tempDelay == 0 {
						tempDelay = 5 * time.Millisecond
					} else {
						tempDelay *= 2
					}
					if tempDelay > maxDelay {
						tempDelay = maxDelay
					}
					log.Errorf("XTCP - Conn[%v] Send error: %v; retrying in %v", c.RawConn.RemoteAddr(), err, tempDelay)
					time.Sleep(tempDelay)
					continue
				}
			}

			if !c.IsStoped() {
				log.Errorf("XTCP - Conn[%v] Send error : %v", c.RawConn.RemoteAddr(), err)
				c.Stop(StopImmediately)
			}
			return err
		}
		tempDelay = 0
	}
	return nil
}

func (c *Conn) sendLoop() {
	defer func() {
		log.Debug("XTCP - Conn send loop exit : ", c.RawConn.RemoteAddr())

		bufferPool.Put(c.sendBuffer)
		c.wg.Done()
	}()
	for {
		c.sendBufferCond.L.Lock()
		for c.sendBuffer.Len() == 0 && atomic.LoadInt32(&c.state) == connStateNormal {
			c.sendBufferCond.Wait()
		}

		switch atomic.LoadInt32(&c.state) {
		case connStateNormal:
			err := c.sendBuf(c.sendBuffer.Bytes())
			c.sendBuffer.Reset()
			c.sendBufferCond.L.Unlock()
			if err != nil {
				return
			}
		case connStateStopping:
			c.sendBuf(c.sendBuffer.Bytes())
			c.sendBuffer.Reset()
			c.sendBufferCond.L.Unlock()

			atomic.StoreInt32(&c.state, connStateStopped)
			c.RawConn.Close()
			return
		case connStateStopped:
			c.sendBufferCond.L.Unlock()
			return
		}
	}
}

// Send use for send data, can be call in any goroutines.
func (c *Conn) Send(buf []byte) (int, error) {
	if atomic.LoadInt32(&c.state) != connStateNormal {
		return 0, errSendToClosedConn
	}
	c.sendBufferCond.L.Lock()
	defer c.sendBufferCond.L.Unlock()
	defer c.sendBufferCond.Signal()
	return c.sendBuffer.Write(buf)
}

// SendPacket use for send packet, can be call in any goroutines.
func (c *Conn) SendPacket(p Packet) (int, error) {
	if atomic.LoadInt32(&c.state) != connStateNormal {
		return 0, errSendToClosedConn
	}
	buf, err := c.Opts.Protocol.Pack(p)
	if err != nil {
		return 0, err
	}

	return c.Send(buf)
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
