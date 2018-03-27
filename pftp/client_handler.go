package pftp

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

var commandsMap map[string]*CommandDescription

type CommandDescription struct {
	Open bool                 // Open to clients without auth
	Fn   func(*clientHandler) // Function to handle it
}

func init() {
	commandsMap = make(map[string]*CommandDescription)
	commandsMap["USER"] = &CommandDescription{Fn: (*clientHandler).handleUSER}
	commandsMap["AUTH"] = &CommandDescription{Fn: (*clientHandler).handleAUTH}
	commandsMap["EPSV"] = &CommandDescription{Fn: (*clientHandler).handlePASV}
	commandsMap["LIST"] = &CommandDescription{Fn: (*clientHandler).handleLIST}
	commandsMap["FEAT"] = &CommandDescription{Fn: (*clientHandler).handleFEAT}
}

type clientHandler struct {
	id            uint32        // ID of the client
	daddy         *FtpServer    // Server on which the connection was accepted
	conn          net.Conn      // TCP connection
	writer        *bufio.Writer // Writer on the TCP connection
	reader        *bufio.Reader // Reader on the TCP connection
	connectedAt   time.Time     // Date of connection
	line          string
	command       string
	param         string
	ctxRnfr       string          // Rename from
	ctxRest       int64           // Restart point
	transferTLS   bool            // Use TLS for transfer connection
	transfer      transferHandler // Transfer connection (only passive is implemented at this stage)
	controlProxy  *ProxyServer
	transferProxy *ProxyServer
}

func (server *FtpServer) newClientHandler(connection net.Conn, id uint32) *clientHandler {
	p := &clientHandler{
		daddy:       server,
		conn:        connection,
		writer:      bufio.NewWriter(connection),
		reader:      bufio.NewReader(connection),
		connectedAt: time.Now().UTC(),
	}

	return p
}

func (c *clientHandler) disconnect() {
	c.conn.Close()
}

func (c *clientHandler) end() {
	c.daddy.ClientCounter--
}

func (c *clientHandler) WelcomeUser() (string, error) {
	if c.daddy.ClientCounter > c.daddy.config.MaxConnections {
		return "Cannot accept any additional client", fmt.Errorf("too many clients: %d > % d", c.daddy.ClientCounter, c.daddy.config.MaxConnections)
	}

	return fmt.Sprint("Welcome on ftpserver"), nil
}

func (c *clientHandler) HandleCommands() {
	defer c.end()
	if msg, err := c.WelcomeUser(); err == nil {
		c.writeMessage(220, msg)
	} else {
		c.writeMessage(500, msg)
		return
	}

	for {
		if c.reader == nil {
			logrus.Debug("Clean disconnect")
			return
		}

		if c.daddy.config.IdleTimeout > 0 {
			c.conn.SetDeadline(time.Now().Add(time.Duration(time.Second.Nanoseconds() * int64(c.daddy.config.IdleTimeout))))
		}

		line, err := c.reader.ReadString('\n')
		if err != nil {
			switch err := err.(type) {
			case net.Error:
				if err.Timeout() {
					c.conn.SetDeadline(time.Now().Add(time.Minute))
					logrus.Info("IDLE timeout")
					c.writeMessage(421, fmt.Sprintf("command timeout (%d seconds): closing control connection", c.daddy.config.IdleTimeout))
					if err := c.writer.Flush(); err != nil {
						logrus.Error("Network flush error")
					}
					if err := c.conn.Close(); err != nil {
						logrus.Error("Network close error")
					}
					break
				}
				logrus.Error("Network error ftp.net_error")
			default:
				if err == io.EOF {
					logrus.Debug("TCP disconnect")
				} else {
					logrus.Error("Read error")
				}
			}
			return
		}
		c.handleCommand(line)
	}
}

func (c *clientHandler) handleCommand(line string) {
	command, param := parseLine(line)
	c.command = strings.ToUpper(command)
	c.param = param
	c.line = line

	cmdDesc := commandsMap[c.command]
	defer func() {
		if r := recover(); r != nil {
			c.writeMessage(500, fmt.Sprintf("Internal error: %s", r))
		}
	}()

	if cmdDesc != nil {
		cmdDesc.Fn(c)
	}

	if c.controlProxy != nil &&
		command != "EPSV" &&
		command != "FEAT" {
		c.controlProxy.SendToOriginWithProxy(line)
	}
}

func (c *clientHandler) writeLine(line string) {
	c.writer.Write([]byte(line))
	c.writer.Write([]byte("\r\n"))
	c.writer.Flush()
}

func (c *clientHandler) writeMessage(code int, message string) {
	c.writeLine(fmt.Sprintf("%d %s", code, message))
}

func parseLine(line string) (string, string) {
	params := strings.SplitN(strings.Trim(line, "\r\n"), " ", 2)
	if len(params) == 1 {
		return params[0], ""
	}
	return params[0], params[1]
}

func (c *clientHandler) handleUSER() {
	p, err := NewProxyServer(c.daddy.config.ProxyTimeout, c.conn, "localhost:2321")
	if err != nil {
		c.writeMessage(530, "I can't deal with you (proxy error)")
		return
	}

	// read welcome message
	p.ReadFromOrigin()
	c.controlProxy = p
}

func (c *clientHandler) handleAUTH() {
	if c.daddy.config.TLSConfig != nil {
		c.writeMessage(234, "AUTH command ok. Expecting TLS Negotiation.")
		c.conn = tls.Server(c.conn, c.daddy.config.TLSConfig)
		c.reader = bufio.NewReader(c.conn)
		c.writer = bufio.NewWriter(c.conn)
	} else {
		c.writeMessage(550, fmt.Sprint("Cannot get a TLS config"))
	}
}

func (c *clientHandler) handleLIST() {
	c.controlProxy.SendToOriginWithProxy(c.line)
	if proxy, err := c.TransferOpen(); err == nil {
		go proxy.Start()
		for {
			res, err := c.controlProxy.ReadFromOrigin()
			if err != nil {
				logrus.Error(err)
				return
			}

			time.Sleep(10)
			err = c.controlProxy.SendToClient(res)
			if err != nil {
				logrus.Error(err)
			}
			return

		}
	}
}

func (c *clientHandler) TransferOpen() (*ProxyServer, error) {
	if c.transfer == nil {
		return nil, errors.New("no passive connection declared")
	}
	conn, err := c.transfer.Open()
	if err != nil {
		return nil, err
	}
	return conn, err
}

func (c *clientHandler) TransferClose() {
	if c.transfer != nil {
		c.transfer = nil
	}
}

func (c *clientHandler) handleFEAT() {
	c.controlProxy.SendToOriginWithProxy(c.line)
	for {
		b, err := c.controlProxy.ReadFromOrigin()
		if err != nil {
			logrus.Error(err)
			return
		}

		if err := c.controlProxy.SendToClient(b); err != nil {
			logrus.Error(err)
			return
		}
		if strings.HasSuffix(strings.ToUpper(b), "END") || string(b[0]) == "5" {
			return
		}
	}
}
