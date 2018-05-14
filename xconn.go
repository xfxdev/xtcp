package xtcp

import (
	"bytes"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/xfxdev/xlog"
)

const (
	connStateNormal int32 = iota
	connStateStopping
	connStateStopped

	smallBufferSize = 64
)

var (
	errSendToClosedConn = errors.New("send to closed conn")
	errSendListFull     = errors.New("send buffer list full")

	bufferPoolSmall = &sync.Pool{
		New: func() interface{} {
			return bytes.NewBuffer(nil)
		},
	}
	bufferPool1K = &sync.Pool{
		New: func() interface{} {
			return bytes.NewBuffer(make([]byte, 0, 1<<10))
		},
	}
	bufferPool2K = &sync.Pool{
		New: func() interface{} {
			return bytes.NewBuffer(make([]byte, 0, 2<<10))
		},
	}
	bufferPool4K = &sync.Pool{
		New: func() interface{} {
			return bytes.NewBuffer(make([]byte, 0, 4<<10))
		},
	}
	bufferPoolBig = &sync.Pool{}
)

func getBufferFromPool(targetSize int) *bytes.Buffer {
	var buf *bytes.Buffer
	if targetSize <= smallBufferSize {
		buf = bufferPoolSmall.Get().(*bytes.Buffer)
	} else if targetSize <= 1<<10 {
		buf = bufferPool1K.Get().(*bytes.Buffer)
	} else if targetSize <= 2<<10 {
		buf = bufferPool2K.Get().(*bytes.Buffer)
	} else if targetSize <= 4<<10 {
		buf = bufferPool4K.Get().(*bytes.Buffer)
	} else {
		itr := bufferPoolBig.Get()
		if itr != nil {
			buf = itr.(*bytes.Buffer)
		} else {
			buf = bytes.NewBuffer(make([]byte, 0, targetSize))
		}
	}
	buf.Reset()
	return buf
}

func putBufferToPool(buffer *bytes.Buffer) {
	cap := buffer.Cap()
	if cap <= smallBufferSize {
		bufferPoolSmall.Put(buffer)
	} else if cap <= 1<<10 {
		bufferPool1K.Put(buffer)
	} else if cap <= 2<<10 {
		bufferPool2K.Put(buffer)
	} else if cap <= 4<<10 {
		bufferPool4K.Put(buffer)
	} else {
		bufferPoolBig.Put(buffer)
	}
}

// A Conn represents the server side of an tcp connection.
type Conn struct {
	Opts        *Options
	RawConn     net.Conn
	UserData    interface{}
	sendBufList chan *bytes.Buffer
	closed      chan struct{}
	state       int32
	wg          sync.WaitGroup
	once        sync.Once
	SendDropped uint32
	sendBytes   uint64
	recvBytes   uint64
	dropped     uint32
}

// NewConn return new conn.
func NewConn(opts *Options) *Conn {
	if opts.RecvBufSize <= 0 {
		log.Warnf("Invalid Opts.RecvBufSize : %v, use DefaultRecvBufSize instead", opts.RecvBufSize)
		opts.RecvBufSize = DefaultRecvBufSize
	}
	if opts.SendBufListLen <= 0 {
		log.Warnf("Invalid Opts.SendBufListLen : %v, use DefaultRecvBufSize instead", opts.SendBufListLen)
		opts.SendBufListLen = DefaultSendBufListLen
	}
	c := &Conn{
		Opts:        opts,
		sendBufList: make(chan *bytes.Buffer, opts.SendBufListLen),
		closed:      make(chan struct{}),
		state:       connStateNormal,
	}

	return c
}

func (c *Conn) String() string {
	return c.RawConn.LocalAddr().String() + " -> " + c.RawConn.RemoteAddr().String()
}

// SendBytes return the total send bytes.
func (c *Conn) SendBytes() uint64 {
	return atomic.LoadUint64(&c.sendBytes)
}

// RecvBytes return the total receive bytes.
func (c *Conn) RecvBytes() uint64 {
	return atomic.LoadUint64(&c.recvBytes)
}

// DroppedPacket return the total dropped packet.
func (c *Conn) DroppedPacket() uint32 {
	return atomic.LoadUint32(&c.dropped)
}

// Stop stops the conn.
func (c *Conn) Stop(mode StopMode) {
	c.once.Do(func() {
		if mode == StopImmediately {
			atomic.StoreInt32(&c.state, connStateStopped)
			c.RawConn.Close()
			close(c.sendBufList)
			close(c.closed)
		} else {
			atomic.StoreInt32(&c.state, connStateStopping)
			// c.RawConn.Close() 	// will close in sendLoop
			// close(c.sendBufList) // will close in sendLoop
			close(c.closed)
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
	recvBuf := getBufferFromPool(c.Opts.RecvBufSize)
	maxDelay := 1 * time.Second

	defer func() {
		log.Debug("XTCP - Conn recv loop exit : ", c.RawConn.RemoteAddr())

		putBufferToPool(recvBuf)
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
		c.wg.Done()
	}()
	for {
		if atomic.LoadInt32(&c.state) == connStateStopped {
			return
		}

		select {
		case buffer, ok := <-c.sendBufList:
			if !ok {
				return
			}
			err := c.sendBuf(buffer.Bytes())
			if err != nil {
				return
			}
			putBufferToPool(buffer)
		case <-c.closed:
			if atomic.LoadInt32(&c.state) == connStateStopping {
				if len(c.sendBufList) == 0 {
					atomic.SwapInt32(&c.state, connStateStopped)
					close(c.sendBufList)
					c.RawConn.Close()
					return
				}
			}
		}
	}
}

// Send use for send data, can be call in any goroutines.
func (c *Conn) Send(buf []byte) (int, error) {
	if atomic.LoadInt32(&c.state) != connStateNormal {
		return 0, errSendToClosedConn
	}
	bufLen := len(buf)
	if bufLen <= 0 {
		return 0, nil
	}
	buffer := getBufferFromPool(len(buf))
	n, err := buffer.Write(buf)
	if err != nil {
		return 0, err
	}
	select {
	case c.sendBufList <- buffer:
		return n, nil
	default:
		atomic.AddUint32(&c.dropped, 1)
		return 0, errSendListFull
	}
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
