package internet_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/sagernet/sing/common/control"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/transport/internet"
)

func TestRegisterListenerController(t *testing.T) {
	var gotFd uintptr

	common.Must(internet.RegisterListenerController(func(network, address string, conn syscall.RawConn) error {
		return control.Raw(conn, func(fd uintptr) error {
			gotFd = fd
			return nil
		})
	}))

	conn, err := internet.ListenSystemPacket(context.Background(), &net.UDPAddr{
		IP: net.IPv4zero,
	}, nil)
	common.Must(err)
	common.Must(conn.Close())

	if gotFd == 0 {
		t.Error("expected none-zero fd, but actually 0")
	}
}

func newUnixSocketDir(t *testing.T) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("unix socket tests are not supported on windows")
	}

	dir, err := os.MkdirTemp("/tmp", "xruds-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})

	return dir
}

func TestListenSystemRemovesStaleUnixSocket(t *testing.T) {
	socketPath := filepath.Join(newUnixSocketDir(t), "stale.sock")

	staleListener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}

	unixListener, ok := staleListener.(*net.UnixListener)
	if !ok {
		t.Fatalf("expected *net.UnixListener, got %T", staleListener)
	}
	unixListener.SetUnlinkOnClose(false)
	if err := unixListener.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("expected stale socket to remain on disk: %v", err)
	}

	listener, err := internet.ListenSystem(context.Background(), &net.UnixAddr{
		Name: socketPath,
		Net:  "unix",
	}, nil)
	if err != nil {
		t.Fatalf("expected stale socket cleanup to allow listen, got: %v", err)
	}
	defer listener.Close()
}

func TestListenSystemDoesNotRemoveActiveUnixSocket(t *testing.T) {
	socketPath := filepath.Join(newUnixSocketDir(t), "active.sock")

	activeListener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer activeListener.Close()

	_, err = internet.ListenSystem(context.Background(), &net.UnixAddr{
		Name: socketPath,
		Net:  "unix",
	}, nil)
	if err == nil {
		t.Fatal("expected active unix socket to reject a second listener")
	}

	if _, statErr := os.Stat(socketPath); statErr != nil {
		t.Fatalf("expected active socket file to remain intact: %v", statErr)
	}
}
