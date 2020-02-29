package main

import (
	"fmt"
	"log"
	"net"

	"github.com/golang/protobuf/proto"
	"github.com/xfxdev/xtcp"
	"github.com/xfxdev/xtcp/_example/exampleproto"
)

type processor func(c *xtcp.Conn, msg proto.Message)

type exampleLogger struct {}
func (*exampleLogger)Log(l xtcp.LogLevel, v ...interface{}) {
	if l != xtcp.Debug {
		log.Output(2, logLevel2Str[l]+" "+fmt.Sprint(v...))
	}
}
func (*exampleLogger)Logf(l xtcp.LogLevel, format string, v ...interface{}) {
	if l != xtcp.Debug {
	log.Output(2, logLevel2Str[l]+" "+fmt.Sprintf(format, v...))
	}
}

var (
	logger = 	&exampleLogger{}
	protocol     = &ProtobufProtocol{}
	mapProcessor = make(map[string]processor)
	msghello     = "client : Hello"
	msgByeBye    = "client : ByeBye"
	msgHi        = "server : Hi"
	msgBye       = "server : Bye"

	logLevel2Str = []string{
		"[P]",
		"[F]",
		"[E]",
		"[W]",
		"[I]",
		"[D]",
	}
)

func init() {
	mapProcessor[proto.MessageName((*exampleproto.LoginRequest)(nil))] = loginRequestProcess
	mapProcessor[proto.MessageName((*exampleproto.LoginResponse)(nil))] = loginResponseProcess
	mapProcessor[proto.MessageName((*exampleproto.Chat)(nil))] = chatProcess
	xtcp.SetLogger(logger)
}
func loginRequestProcess(c *xtcp.Conn, msg proto.Message) {
	if request, ok := msg.(*exampleproto.LoginRequest); ok {
		logger.Logf(xtcp.Info, "login request - account : %v, pw : %v", request.GetAccount(), request.GetPassword())

		response := &exampleproto.LoginResponse{
			Ret: proto.Int32(1),
		}
		buf, err := protocol.Pack(NewProtobufPacket(response))
		if err != nil {
			logger.Log(xtcp.Info, "failed to pack msg : ", err)
			return
		}
		c.Send(buf)
	}
}
func loginResponseProcess(c *xtcp.Conn, msg proto.Message) {
	if response, ok := msg.(*exampleproto.LoginResponse); ok {
		if response.GetRet() == 1 {
			logger.Log(xtcp.Info, "login success.")

			// start chat after login success.
			chatMsg := &exampleproto.Chat{
				Msg: proto.String(msghello),
			}
			c.SendPacket(NewProtobufPacket(chatMsg))
		} else {
			logger.Log(xtcp.Info, "login failed.")
		}
	}
}
func chatProcess(c *xtcp.Conn, msg proto.Message) {
	if chatMsg, ok := msg.(*exampleproto.Chat); ok {

		logger.Log(xtcp.Info, " - ", chatMsg.GetMsg())

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
	logger.Log(xtcp.Info, "accept : ", c)
	c.UserData = "server"
}
func (h *myHandler) OnConnect(c *xtcp.Conn) {
	logger.Log(xtcp.Info, "connected : ", c)
	c.UserData = "client"
}
func (h *myHandler) OnRecv(c *xtcp.Conn, p xtcp.Packet) {
	if protobufPacket, ok := p.(*ProtobufPacket); ok {
		proc := mapProcessor[proto.MessageName(protobufPacket.Msg)]
		if proc != nil {
			proc(c, protobufPacket.Msg)
		} else {
			logger.Log(xtcp.Info, "no processor for Packet : ", p)
		}
	}
}
func (h *myHandler) OnUnpackErr(c *xtcp.Conn, buf []byte, err error) {

}
func (h *myHandler) OnClose(c *xtcp.Conn) {
	logger.Logf(xtcp.Info, "close : %v", c.RawConn.RemoteAddr())
}

func main() {
	h := &myHandler{}
	opts := xtcp.NewOpts(h, protocol)
	l, err := net.Listen("tcp", ":")
	if err != nil {
		logger.Log(xtcp.Info, "listen err : ", err)
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
			logger.Log(xtcp.Info, "client dial err : ", err)
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
	logger.Log(xtcp.Info, "server and client stoped. thanks.")
}
