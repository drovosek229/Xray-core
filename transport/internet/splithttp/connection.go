package splithttp

import (
	"io"
	"net"
	"sync"
	"time"
)

type splitConn struct {
	writer     io.WriteCloser
	reader     io.ReadCloser
	remoteAddr net.Addr
	localAddr  net.Addr
	onClose    func()
	deadlineMu sync.Mutex
	readTimer  *time.Timer
	writeTimer *time.Timer
}

func (c *splitConn) Write(b []byte) (int, error) {
	return c.writer.Write(b)
}

func (c *splitConn) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}

func (c *splitConn) Close() error {
	c.stopDeadlineTimers()
	if c.onClose != nil {
		c.onClose()
	}

	err := c.writer.Close()
	err2 := c.reader.Close()
	if err != nil {
		return err
	}

	if err2 != nil {
		return err
	}

	return nil
}

func (c *splitConn) LocalAddr() net.Addr {
	return c.localAddr
}

func (c *splitConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

func (c *splitConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

func (c *splitConn) SetReadDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()

	if c.readTimer != nil {
		c.readTimer.Stop()
		c.readTimer = nil
	}
	if t.IsZero() {
		return nil
	}

	delay := time.Until(t)
	if delay <= 0 {
		return c.reader.Close()
	}

	c.readTimer = time.AfterFunc(delay, func() {
		c.reader.Close()
	})
	return nil
}

func (c *splitConn) SetWriteDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()

	if c.writeTimer != nil {
		c.writeTimer.Stop()
		c.writeTimer = nil
	}
	if t.IsZero() {
		return nil
	}

	delay := time.Until(t)
	if delay <= 0 {
		return c.writer.Close()
	}

	c.writeTimer = time.AfterFunc(delay, func() {
		c.writer.Close()
	})
	return nil
}

func (c *splitConn) stopDeadlineTimers() {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()

	if c.readTimer != nil {
		c.readTimer.Stop()
		c.readTimer = nil
	}
	if c.writeTimer != nil {
		c.writeTimer.Stop()
		c.writeTimer = nil
	}
}
