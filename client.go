// Package godrop is an IRC client library.
package godrop

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/horgh/irc"
)

// Client holds an IRC client connection.
type Client struct {
	// conn: The connection if we are actively connected.
	conn net.Conn

	// rw: Read/write handle to the connection
	rw *bufio.ReadWriter

	// nick is the desired nickname.
	nick string

	// name is the realname to use.
	name string

	// ident is the ident to use.
	ident string

	// host is the IP/hostname of the IRC server to connect to.
	host string

	// port is the port of the host of the IRC server to connect to.
	port int

	// tls toggles whether we connect with TLS/SSL or not.
	tls bool

	// Config holds the parsed config file data.
	//
	// TODO(horgh): This doesn't really seem to belong here.
	Config map[string]string

	// Track whether we've successfully registered.
	registered bool

	// Deadline on read/writes.
	timeoutTime time.Duration
}

const (
	// timeoutConnect is how long we wait for connection attempts to time out.
	timeoutConnect = 30 * time.Second

	// timeoutTime is how long we wait on network I/O by default.
	timeoutTime = 5 * time.Minute

	// keepAliveDuration is how long between TCP keepalives.
	keepAliveDuration = 30 * time.Second
)

// Hooks are functions to call for each message. Packages can take actions
// this way.
var Hooks []func(*Client, irc.Message)

// New creates a new client connection.
func New(nick, name, ident, host string, port int, tls bool) *Client {
	return &Client{
		nick:        nick,
		name:        name,
		ident:       ident,
		host:        host,
		port:        port,
		tls:         tls,
		timeoutTime: timeoutTime,
	}
}

// SetTimeoutTime sets the duration we wait on reads/writes.
func (c *Client) SetTimeoutTime(d time.Duration) {
	c.timeoutTime = d
}

// Close cleans up the client. It closes the connection.
func (c *Client) Close() error {
	c.registered = false
	c.rw = nil

	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// Connect opens a new connection to the server.
func (c *Client) Connect() error {
	dialer := &net.Dialer{
		Timeout:   timeoutConnect,
		KeepAlive: keepAliveDuration,
	}

	if c.tls {
		conn, err := tls.DialWithDialer(dialer, "tcp",
			fmt.Sprintf("%s:%d", c.host, c.port),
			&tls.Config{
				// Often IRC servers don't have valid certs.
				InsecureSkipVerify: true,
			})
		if err != nil {
			return err
		}

		c.conn = conn
		c.rw = bufio.NewReadWriter(bufio.NewReader(c.conn), bufio.NewWriter(c.conn))
		return nil
	}

	conn, err := dialer.Dial("tcp", fmt.Sprintf("%s:%d", c.host, c.port))
	if err != nil {
		return err
	}

	c.conn = conn
	c.rw = bufio.NewReadWriter(bufio.NewReader(c.conn), bufio.NewWriter(c.conn))
	return nil
}

// ReadMessage reads a line from the connection and parses it as an IRC message.
func (c Client) ReadMessage() (irc.Message, error) {
	buf, err := c.read()
	if err != nil {
		return irc.Message{}, err
	}

	m, err := irc.ParseMessage(buf)
	if err != nil && err != irc.ErrTruncated {
		return irc.Message{}, fmt.Errorf("unable to parse message: %s: %s", buf,
			err)
	}

	return m, nil
}

// read reads a line from the connection.
func (c Client) read() (string, error) {
	if err := c.conn.SetDeadline(time.Now().Add(c.timeoutTime)); err != nil {
		return "", fmt.Errorf("unable to set deadline: %s", err)
	}

	line, err := c.rw.ReadString('\n')
	if err != nil {
		return "", err
	}

	log.Printf("Read: %s", strings.TrimRight(line, "\r\n"))

	return line, nil
}

// WriteMessage writes an IRC message to the connection.
func (c Client) WriteMessage(m irc.Message) error {
	buf, err := m.Encode()
	if err != nil && err != irc.ErrTruncated {
		return fmt.Errorf("unable to encode message: %s", err)
	}

	return c.write(buf)
}

// write writes a string to the connection
func (c Client) write(s string) error {
	if err := c.conn.SetDeadline(time.Now().Add(c.timeoutTime)); err != nil {
		return fmt.Errorf("unable to set deadline: %s", err)
	}

	sz, err := c.rw.WriteString(s)
	if err != nil {
		return err
	}

	if sz != len(s) {
		return fmt.Errorf("short write")
	}

	if err := c.rw.Flush(); err != nil {
		return fmt.Errorf("flush error: %s", err)
	}

	log.Printf("Sent: %s", strings.TrimRight(s, "\r\n"))

	return nil
}

// Loop enters a loop reading from the server.
//
// We maintain the IRC connection.
//
// Hook events will fire.
func (c *Client) Loop() error {
	for {
		msg, err := c.ReadMessage()
		if err != nil {
			return err
		}

		if msg.Command == "PING" {
			if err := c.Pong(msg); err != nil {
				return err
			}
		}

		if msg.Command == "ERROR" {
			// Error terminates the connection. We get it as an acknowledgement after
			// sending a QUIT.
			return c.Close()
		}

		c.hooks(msg)
	}
}

// hooks calls each registered IRC package hook.
func (c *Client) hooks(message irc.Message) {
	for _, hook := range Hooks {
		hook(c, message)
	}
}

// IsConnected checks whether the client is connected
func (c *Client) IsConnected() bool {
	return c.conn != nil
}

// SetRegistered sets us as registered.
func (c *Client) SetRegistered() {
	c.registered = true
}

// IsRegistered checks whether the client is registered.
func (c *Client) IsRegistered() bool {
	return c.registered
}

// GetNick retrieves the client's nick.
func (c *Client) GetNick() string {
	return c.nick
}

// Register sends the client's registration/greeting. This consists of NICK and
// USER.
func (c *Client) Register() error {
	if err := c.Nick(); err != nil {
		return err
	}

	return c.User()
}

// Nick sends the NICK command.
func (c *Client) Nick() error {
	if err := c.WriteMessage(irc.Message{
		Command: "NICK",
		Params:  []string{c.nick},
	}); err != nil {
		return fmt.Errorf("failed to send NICK: %s", err)
	}

	return nil
}

// User sends the USER command.
func (c *Client) User() error {
	if err := c.WriteMessage(irc.Message{
		Command: "USER",
		Params:  []string{c.ident, "0", "*", c.name},
	}); err != nil {
		return fmt.Errorf("failed to send USER: %s", err)
	}

	return nil
}

// Pong sends a PONG in response to the given PING message.
func (c *Client) Pong(ping irc.Message) error {
	return c.WriteMessage(irc.Message{
		Command: "PONG",
		Params:  []string{ping.Params[0]},
	})
}

// Join joins a channel.
func (c *Client) Join(name string) error {
	return c.WriteMessage(irc.Message{
		Command: "JOIN",
		Params:  []string{name},
	})
}

// Message sends a message.
//
// If the message is too long for a single line, then it will be split over
// several lines.
func (c *Client) Message(target string, message string) error {
	// 512 is the maximum IRC protocol length.
	// However, user and host takes up some of that. Let's cut down a bit.
	// This is arbitrary.
	maxMessage := 412

	// Number of overhead bytes.
	overhead := len("PRIVMSG ") + len(" :") + len("\r\n")

	for i := 0; i < len(message); i += maxMessage - overhead {
		endIndex := i + maxMessage - overhead
		if endIndex > len(message) {
			endIndex = len(message)
		}
		piece := message[i:endIndex]

		if err := c.WriteMessage(irc.Message{
			Command: "PRIVMSG",
			Params:  []string{target, piece},
		}); err != nil {
			return nil
		}
	}

	return nil
}

// Quit sends a quit.
//
// We track when we send this as we expect an ERROR message in response.
func (c *Client) Quit(message string) error {
	return c.WriteMessage(irc.Message{
		Command: "QUIT",
		Params:  []string{message},
	})
}

// Oper sends an OPER command
func (c *Client) Oper(name string, password string) error {
	return c.WriteMessage(irc.Message{
		Command: "OPER",
		Params:  []string{name, password},
	})
}

// UserMode sends a MODE command.
func (c *Client) UserMode(nick string, modes string) error {
	return c.WriteMessage(irc.Message{
		Command: "MODE",
		Params:  []string{nick, modes},
	})
}
