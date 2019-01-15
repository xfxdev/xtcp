package main

import (
	"github.com/golang/protobuf/proto"
	"github.com/xfxdev/xlog"
	"github.com/xfxdev/xtcp"
	"github.com/xfxdev/xtcp/example/exampleproto"
	"net"
)

type processor func(c *xtcp.Conn, msg proto.Message)

var (
	protocol     = &ProtobufProtocol{}
	mapProcessor = make(map[string]processor)
	msghello     = "client : Hello"
	msgByeBye    = "client : ByeBye"
	msgHi        = "server : Hi"
	msgBye       = "server : Bye"
)

func init() {
	mapProcessor[proto.MessageName((*exampleproto.LoginRequest)(nil))] = loginRequestProcess
	mapProcessor[proto.MessageName((*exampleproto.LoginResponse)(nil))] = loginResponseProcess
	mapProcessor[proto.MessageName((*exampleproto.Chat)(nil))] = chatProcess
}
func loginRequestProcess(c *xtcp.Conn, msg proto.Message) {
	if request, ok := msg.(*exampleproto.LoginRequest); ok {
		xlog.Info("login account : ", request.GetAccount())
		xlog.Info("login pw : ", request.GetPassword())

		response := &exampleproto.LoginResponse{
			Ret: proto.Int32(1),
		}
		buf, err := protocol.Pack(NewProtobufPacket(response))
		if err != nil {
			xlog.Error("failed to pack msg : ", err)
			return
		}
		c.Send(buf)
	}
}
func loginResponseProcess(c *xtcp.Conn, msg proto.Message) {
	if response, ok := msg.(*exampleproto.LoginResponse); ok {
		if response.GetRet() == 1 {
			xlog.Info("login success.")

			// start chat after login success.
			chatMsg := &exampleproto.Chat{
				Msg: proto.String(msghello),
			}
			c.SendPacket(NewProtobufPacket(chatMsg))
		} else {
			xlog.Info("login failed.")
		}
	}
}
func chatProcess(c *xtcp.Conn, msg proto.Message) {
	if chatMsg, ok := msg.(*exampleproto.Chat); ok {

		xlog.Info(" - ", chatMsg.GetMsg())

		var strResponseMsg string
		switch c.UserData.(string) {
		case "server":
			switch chatMsg.GetMsg() {
			case msghello:
				strResponseMsg = msgHi
			case msgByeBye:
				strResponseMsg = msgBye
			}
		case "client":
			switch chatMsg.GetMsg() {
			case msgHi:
				strResponseMsg = msgByeBye
			case msgBye:
				c.Stop(xtcp.StopGracefullyButNotWait)
				return
			}
		}

		msgChatResponse := &exampleproto.Chat{
			Msg: proto.String(strResponseMsg),
		}
		c.SendPacket(NewProtobufPacket(msgChatResponse))
	}
}

type myHandler struct {
}

func (h *myHandler) OnAccept(c *xtcp.Conn) {
	xlog.Info("accept : ", c)
	c.UserData = "server"
}
func (h *myHandler) OnConnect(c *xtcp.Conn) {
	xlog.Info("connected : ", c)
	c.UserData = "client"
}
func (h *myHandler) OnRecv(c *xtcp.Conn, p xtcp.Packet) {
	if protobufPacket, ok := p.(*ProtobufPacket); ok {
		proc := mapProcessor[proto.MessageName(protobufPacket.Msg)]
		if proc != nil {
			proc(c, protobufPacket.Msg)
		} else {
			xlog.Error("no processor for Packet : ", p)
		}
	}
}
func (h *myHandler) OnUnpackErr(c *xtcp.Conn, buf []byte, err error) {

}
func (h *myHandler) OnClose(c *xtcp.Conn) {
	xlog.Debugf("close : %v", c.RawConn.RemoteAddr())
}

func main() {
	h := &myHandler{}
	opts := xtcp.NewOpts(h, protocol)
	l, err := net.Listen("tcp", ":")
	if err != nil {
		xlog.Error("listen err : ", err)
		return
	}
	server := xtcp.NewServer(opts)
	go func() {
		server.Serve(l)
	}()

	client := xtcp.NewConn(opts)
	clientClosed := make(chan struct{})
	go func() {
		err := client.DialAndServe(l.Addr().String())
		if err != nil {
			xlog.Error("client dial err : ", err)
		}
		close(clientClosed)
	}()

	// send login request.
	msgLoginRequest := &exampleproto.LoginRequest{
		Account:  proto.String("123"),
		Password: proto.String("456"),
	}
	client.SendPacket(NewProtobufPacket(msgLoginRequest))

	<-clientClosed
	server.Stop(xtcp.StopGracefullyAndWait)
	xlog.Info("server and client stoped. thanks.")
}
