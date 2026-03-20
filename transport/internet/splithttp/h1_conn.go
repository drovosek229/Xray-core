package splithttp

import (
	"bufio"
	"net"
)

type H1Conn struct {
	UnreadedResponsesCount int
	RespBufReader          *bufio.Reader
	ReqBufWriter           *bufio.Writer
	net.Conn
}

func NewH1Conn(conn net.Conn) *H1Conn {
	return &H1Conn{
		RespBufReader: bufio.NewReader(conn),
		ReqBufWriter:  bufio.NewWriter(conn),
		Conn:          conn,
	}
}
