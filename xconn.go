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
)

var (
	errSendToClosedConn = errors.New("send to closed conn")
	errSendListFull     = errors.New("send list full")

	bufferPool1K = &sync.Pool{
		New: func() interface{} {
			return make([]byte, 1<<10)
		},
	}
	bufferPool2K = &sync.Pool{
		New: func() interface{} {
			return make([]byte, 2<<10)
		},
	}
	bufferPool4K = &sync.Pool{
		New: func() interface{} {
			return make([]byte, 4<<10)
		},
	}
	bufferPoolBig = &sync.Pool{}
)

func getBufferFromPool(targetSize int) []byte {
	var buf []byte
	if targetSize <= 1<<10 {
		buf = bufferPool1K.Get().([]byte)
	} else if targetSize <= 2<<10 {
		buf = bufferPool2K.Get().([]byte)
	} else if targetSize <= 4<<10 {
		buf = bufferPool4K.Get().([]byte)
	} else {
		itr := bufferPoolBig.Get()
		if itr != nil {
			buf = itr.([]byte)
		} else {
			buf = make([]byte, targetSize)
		}
	}
	buf = buf[:targetSize]
	return buf
}

func putBufferToPool(buf []byte) {
	cap := cap(buf)
	if cap <= 1<<10 {
		bufferPool1K.Put(buf)
	} else if cap <= 2<<10 {
		bufferPool2K.Put(buf)
	} else if cap <= 4<<10 {
		bufferPool4K.Put(buf)
	} else {
		bufferPoolBig.Put(buf)
	}
}

// A Conn represents the server side of an tcp connection.
type Conn struct {
	sync.Mutex
	Opts        *Options
	RawConn     net.Conn
	UserData    interface{}
	sendBufList chan []byte
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
		sendBufList: make(chan []byte, opts.SendBufListLen),
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
			//close(c.sendBufList) // leave channel open, because other goroutine maybe use it in Send.
			close(c.closed)
		} else {
			atomic.StoreInt32(&c.state, connStateStopping)
			// c.RawConn.Close() 	// will close in sendLoop
			// close(c.sendBufList)
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

	if c.Opts.AsyncWrite {
		c.wg.Add(2)
		go c.sendLoop()
	} else {
		c.wg.Add(1)
	}
	c.recvLoop()

	c.Opts.Handler.OnClose(c)
}

func (c *Conn) recvLoop() {
	var tempDelay time.Duration
	tempBuf := make([]byte, c.Opts.RecvBufSize)
	recvBuf := bytes.NewBuffer(make([]byte, 0, c.Opts.RecvBufSize))
	maxDelay := 1 * time.Second

	defer func() {
		log.Debug("XTCP - Conn recv loop exit : ", c.RawConn.RemoteAddr())
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
					log.Errorf("XTCP - Conn[%v] recv error : %v", c.RawConn.RemoteAddr(), err)
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
				c.Opts.Handler.OnUnpackErr(c, recvBuf.Bytes(), err)
			}

			if pl > 0 {
				_ = recvBuf.Next(pl)
			}

			if p != nil {
				c.Opts.Handler.OnRecv(c, p)
			} else {
				break
			}
		}
	}
}

func (c *Conn) sendBuf(buf []byte) (int, error) {
	sended := 0
	var tempDelay time.Duration
	maxDelay := 1 * time.Second
	for sended < len(buf) {
		if c.Opts.WriteDeadline != 0 {
			c.RawConn.SetWriteDeadline(time.Now().Add(c.Opts.WriteDeadline))
		}
		wn, err := c.RawConn.Write(buf[sended:])
		if wn > 0 {
			sended += wn
			atomic.AddUint64(&c.sendBytes, uint64(wn))
		}

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
			return sended, err
		}
		tempDelay = 0
	}
	return sended, nil
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
		case buf, ok := <-c.sendBufList:
			if !ok {
				return
			}
			_, err := c.sendBuf(buf)
			if err != nil {
				return
			}
			putBufferToPool(buf)
		case <-c.closed:
			if atomic.LoadInt32(&c.state) == connStateStopping {
				if len(c.sendBufList) == 0 {
					atomic.SwapInt32(&c.state, connStateStopped)
					c.RawConn.Close()
					return
				}
			}
		}
	}
}

func (c *Conn) sendByteBuffer(buffer *bytes.Buffer) (int, error) {
	select {
	case c.sendBufList <- buffer:
		return buffer.Len(), nil
	default:
		putBufferToPool(buffer)
		atomic.AddUint32(&c.dropped, 1)
		return 0, errSendListFull
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

	if c.Opts.AsyncWrite {
		buffer := getBufferFromPool(len(buf))
		copy(buffer, buf)
		select {
		case c.sendBufList <- buffer:
			return bufLen, nil
		default:
			atomic.AddUint32(&c.dropped, 1)
			return 0, errSendListFull
		}
	} else {
		c.Lock() // Ensure entirety of buf is written together
		n, err := c.sendBuf(buf)
		c.Unlock()
		return n, err
	}
}

// SendPacket use for send packet, can be call in any goroutines.
func (c *Conn) SendPacket(p Packet) (int, error) {
	if atomic.LoadInt32(&c.state) != connStateNormal {
		return 0, errSendToClosedConn
	}

	needSize := c.Opts.Protocol.PackSize(p)
	if needSize <= 0 {
		return 0, nil
	}

	buffer := getBufferFromPool(needSize)
	_, err := c.Opts.Protocol.PackTo(p, buffer)
	if err != nil {
		putBufferToPool(buffer)
		return 0, err
	}
	return c.sendByteBuffer(buffer)
}

// DialAndServe connects to the addr and serve.
func (c *Conn) DialAndServe(addr string) error {
	rawConn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}

	c.RawConn = rawConn

	c.Opts.Handler.OnConnect(c)

	c.serve()

	return nil
}
