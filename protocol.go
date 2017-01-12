package xtcp

import (
	"fmt"
	"io"
)

// Package is the type of network protocol package.
type Package interface {
	fmt.Stringer
	Conn(c *Conn)
	GetConn() *Conn
	// CachedPackBuf cache the packed buf in package.
	// Avoid repack when you want to send a package to differents conn.
	CachedPackBuf(b []byte)
	GetCachedPackBuf() []byte
}

// Protocol is the protocol, use to pack/unpack package.
type Protocol interface {
	// return the size need for pack the package.
	PackSize(p Package) int
	// PackTo pack the package to w.
	// The return value n is the number of bytes written;
	// Any error encountered during the write is also returned.
	PackTo(p Package, w io.Writer) (int, error)
	// Pack pack the package to new created buf.
	Pack(p Package) ([]byte, error)
	// try to unpack the buf to package. If len > 0, then buf[:len] will be discard.
	// The following return conditions must be implement:
	// (nil, 0, nil) : buf size not enough for unpack one package.
	// (nil, len, err) : buf size enough but error encountered.
	// (p, len, nil) : unpack succeed.
	Unpack(buf []byte) (Package, int, error)
}
