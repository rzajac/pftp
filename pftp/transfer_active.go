package pftp

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

func (c *clientHandler) handlePORT() {
	raddr, err := parseRemoteAddr(c.param)

	if err != nil {
		c.writeMessage(500, fmt.Sprintf("Problem parsing PORT: %v", err))
		return
	}

	var laddr *net.TCPAddr
	if c.daddy.config.UseUnknownActiveDataPort {
		laddr = nil
	} else {
		laddr, _ = net.ResolveTCPAddr("tcp", ":20")
	}
	var tcpListener *net.TCPListener

	tcpListener, err = net.ListenTCP("tcp", laddr)
	if err != nil {
		c.writeMessage(500, fmt.Sprintf("Problem parsing PORT: %v", err))
		return
	}

	c.transfer = &activeTransferHandler{
		listener:   tcpListener,
		clientAddr: raddr,
	}

	port := tcpListener.Addr().(*net.TCPAddr).Port
	p1 := port / 256
	p2 := port - (p1 * 256)
	ip := strings.Split(c.conn.LocalAddr().String(), ":")[0]
	quads := strings.Split(ip, ".")

	if err := c.controleProxy.SendToOrigin(fmt.Sprintf("PORT %s,%s,%s,%s,%d,%d\r\n", quads[0], quads[1], quads[2], quads[3], p1, p2)); err != nil {
		c.writeMessage(500, fmt.Sprintf("Problem parsing PORT: %v", err))
		return
	}

	c.writeMessage(200, "PORT command successful")
}

type activeTransferHandler struct {
	listener    net.Listener
	clientAddr  *net.TCPAddr
	proxyServer *ProxyServer
}

func (a *activeTransferHandler) Open() (*ProxyServer, error) {

	conn, err := a.listener.Accept()
	if err != nil {
		return nil, err
	}

	if a.proxyServer == nil {
		var err error
		proxy, err := NewProxyServer(60, conn, a.clientAddr.String())

		if err != nil {
			return nil, err
		}
		a.proxyServer = proxy
	}

	return a.proxyServer, nil
}

func (a *activeTransferHandler) Close() error {
	if a.proxyServer != nil {
		a.proxyServer.Close()
		a.proxyServer = nil
	}
	return nil
}

func parseRemoteAddr(param string) (*net.TCPAddr, error) {
	params := strings.Split(param, ",")
	if len(params) != 6 {
		return nil, errors.New("bad number of args")
	}
	ip := strings.Join(params[0:4], ".")

	p1, err := strconv.Atoi(params[4])
	if err != nil {
		return nil, err
	}
	p2, err := strconv.Atoi(params[5])
	if err != nil {
		return nil, err
	}
	port := p1<<8 + p2

	return net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:%d", ip, port))
}
