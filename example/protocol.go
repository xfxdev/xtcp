package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"github.com/golang/protobuf/proto"
	"github.com/xfxdev/xtcp"
	"io"
	"reflect"
)

var (
	errUnknownProtobufMsgType = errors.New("Unknown protobuf message type")
	errBufSizeNotEnoughToPack = errors.New("buf size not enough")
)

// ProtobufPacket size : msglen + msgNameLen + len(msgName) + len(msgData)
type ProtobufPacket struct {
	Msg proto.Message
}

// NewProtobufPacket create new ProtobufPacket by proto.Message
func NewProtobufPacket(msg proto.Message) *ProtobufPacket {
	p := &ProtobufPacket{
		Msg: msg,
	}

	return p
}

// ProtobufProtocol type.
type ProtobufProtocol struct {
}

func (p *ProtobufPacket) String() string {
	if p.Msg == nil {
		return "<nil proto msg>"
	}

	// return proto.MarshalTextString(p.Msg)
	return proto.CompactTextString(p.Msg)
}

// PackSize return size need for pack ProtobufPacket.
func (pro *ProtobufProtocol) PackSize(p xtcp.Packet) int {
	pp := p.(*ProtobufPacket)
	if pp == nil {
		return 0
	}

	msgName := proto.MessageName(pp.Msg)
	msgNameLen := len(msgName)
	if msgNameLen == 0 {
		return 0
	}
	return 4 + 4 + msgNameLen + proto.Size(pp.Msg)
}

// PackTo :
func (pro *ProtobufProtocol) PackTo(p xtcp.Packet, w io.Writer) (int, error) {
	pp := p.(*ProtobufPacket)
	if pp == nil {
		return 0, errUnknownProtobufMsgType
	}

	msgName := proto.MessageName(pp.Msg)
	msgNameLen := len(msgName)
	if msgNameLen == 0 {
		return 0, errUnknownProtobufMsgType
	}
	msgLen := 4 + 4 + msgNameLen + proto.Size(pp.Msg)

	data, err := proto.Marshal(pp.Msg)
	if err != nil {
		return 0, err
	}

	wl := 0
	err = binary.Write(w, binary.BigEndian, uint32(msgLen))
	if err != nil {
		return wl, err
	}
	wl += 4
	err = binary.Write(w, binary.BigEndian, uint32(msgNameLen))
	if err != nil {
		return wl, err
	}
	wl += 4
	n, err := w.Write([]byte(msgName))
	wl += n
	if err != nil {
		return wl, err
	}
	n, err = w.Write(data)
	wl += n
	if err != nil {
		return wl, err
	}
	return wl, nil
}

// Pack :
func (pro *ProtobufProtocol) Pack(p xtcp.Packet) ([]byte, error) {
	len := pro.PackSize(p)
	if len != 0 {
		buf := bytes.NewBuffer(nil)
		_, err := pro.PackTo(p, buf)
		return buf.Bytes(), err
	}
	return nil, errUnknownProtobufMsgType
}

// Unpack :
func (pro *ProtobufProtocol) Unpack(buf []byte) (xtcp.Packet, int, error) {
	if len(buf) < 4 {
		return nil, 0, nil
	}

	msgLen := int(binary.BigEndian.Uint32(buf[:4]))
	if len(buf[4:]) < msgLen {
		return nil, 0, nil
	}

	msgNameLen := binary.BigEndian.Uint32(buf[4:8])
	msgName := string(buf[8 : 8+msgNameLen])

	t := proto.MessageType(msgName)
	if t == nil {
		return nil, msgLen, errUnknownProtobufMsgType
	}
	msg := reflect.New(t.Elem()).Interface().(proto.Message)
	err := proto.Unmarshal(buf[8+msgNameLen:msgLen], msg)
	if err != nil {
		return nil, msgLen, err
	}

	p := NewProtobufPacket(msg)
	return p, msgLen, nil
}
