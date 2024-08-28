package urihandler

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall"
	"time"
)

// SocketStatus defines the status of a SocketHandler including its mode, role, and current connections.
type SocketStatus struct {
	mode          Mode
	role          Role
	address       string
	connections   map[string]string // Mapping of local to remote addresses for connections
	readDeadline  time.Duration
	writeDeadline time.Duration
	isOpen        bool
}

// Getter methods for SocketStatus
func (s SocketStatus) GetMode() Mode      { return s.mode }
func (s SocketStatus) GetRole() Role      { return s.role }
func (s SocketStatus) GetAddress() string { return s.address }
func (s SocketStatus) IsOpen() bool       { return s.isOpen }

// SocketHandler manages Unix socket connections, providing methods to open, close, and manage streams.
type SocketHandler struct {
	address       string
	readDeadline  time.Duration
	writeDeadline time.Duration
	mode          Mode
	role          Role
	listener      net.Listener
	dataChannel   chan []byte
	events        chan error
	connections   map[net.Conn]struct{}
	mu            sync.RWMutex
	status        SocketStatus
}

// NewSocketHandler initializes a new SocketHandler with the specified settings.
func NewSocketHandler(
	mode Mode,
	role Role,
	dataChannel chan []byte,
	events chan error,
	address string,
	readDeadline,
	writeDeadline time.Duration,
) URIHandler {
	handler := &SocketHandler{
		mode:          mode,
		role:          role,
		dataChannel:   dataChannel,
		events:        events,
		address:       address,
		readDeadline:  readDeadline,
		writeDeadline: writeDeadline,
		connections:   make(map[net.Conn]struct{}),
		status: SocketStatus{
			mode:          mode,
			role:          role,
			address:       address,
			readDeadline:  readDeadline,
			writeDeadline: writeDeadline,
			connections:   make(map[string]string),
		},
	}
	return handler
}

func (h *SocketHandler) GetDataChannel() chan []byte {
	return h.dataChannel
}

func (h *SocketHandler) GetEventsChannel() chan error {
	return h.events
}

// socketBufferPool is a pool of byte slices used to reduce garbage collection overhead.
var socketBufferPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 4096)
		return &b
	},
}

// Status returns the current status of the SocketHandler.
func (h *SocketHandler) Status() Status {
	h.mu.RLock()
	defer h.mu.RUnlock()

	i := 0
	connections := make(map[string]string, len(h.connections))
	for conn := range h.connections {
		connections[fmt.Sprintf("sock%d", i)] = conn.RemoteAddr().String()
		i++
	}

	h.status.connections = connections
	h.status.address = h.address
	h.status.isOpen = len(h.connections) > 0

	return h.status
}

// Open starts the SocketHandler, setting up a Unix socket connection for sending or receiving data.
func (h *SocketHandler) Open(ctx context.Context) error {
	if h.mode == Server {
		ln, err := net.Listen("unix", h.address)
		if err != nil {
			return fmt.Errorf("error creating socket: %w", err)
		}

		fmt.Printf("Listening on %s\n", ln.Addr().String())

		h.listener = ln
		h.mu.Lock()
		h.status.address = ln.Addr().String()
		h.mu.Unlock()
		go h.acceptConnections(ctx)
	} else if h.mode == Client {
		conn, err := net.Dial("unix", h.address)
		if err != nil {
			return fmt.Errorf("error connecting to socket: %w", err)
		}

		h.mu.Lock()
		h.connections[conn] = struct{}{}
		h.mu.Unlock()
		go h.manageStream(ctx, conn)
	}
	return nil
}

func (h *SocketHandler) acceptConnections(ctx context.Context) {
	defer h.Close()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("SocketHandler acceptConnections context canceled")
			return
		default:
			conn, err := h.listener.Accept()
			if err != nil {
				// Specific error checks to decide whether to retry
				if isRetryableError(err) {
					fmt.Printf("Retryable accept error: %v\n", err)
					time.Sleep(time.Millisecond * 5)
					continue
				}
				h.SendError(fmt.Errorf("permanent accept error: %w", err))
				return
			}
			h.mu.Lock()
			h.connections[conn] = struct{}{}
			h.mu.Unlock()

			go h.manageStream(ctx, conn)
		}
	}
}

// isRetryableError checks if the error is a retryable network error.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Example of retryable errors
	if opErr, ok := err.(*net.OpError); ok {
		if opErr.Err == syscall.ECONNRESET || opErr.Err == syscall.ENETUNREACH || opErr.Err == syscall.ECONNREFUSED {
			return true
		}
	}

	return false
}

// manageStream handles data transmission over the connection based on the socket's role.
func (h *SocketHandler) manageStream(ctx context.Context, conn net.Conn) {
	defer func() {
		conn.Close()
		h.mu.Lock()
		delete(h.connections, conn)
		h.mu.Unlock()
	}()

	if h.role == Writer {
		h.handleWrite(ctx, conn)
	} else if h.role == Reader {
		h.handleRead(ctx, conn)
	}
}

// handleWrite manages writing data to the connection.
func (h *SocketHandler) handleWrite(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("SocketHandler handleWrite context canceled")
			return
		case message, ok := <-h.dataChannel:
			if !ok {
				fmt.Println("Data channel closed, stopping handleWrite.")
				return // Channel closed
			}

			if h.writeDeadline > 0 {
				conn.SetWriteDeadline(time.Now().Add(h.writeDeadline))
			}

			_, err := conn.Write(message)
			if err != nil {
				h.SendError(fmt.Errorf("write error: %w", err))
				return // Exit if there is an error writing
			}
		}
	}
}

// handleRead manages reading data from the connection without using a buffer pool.
func (h *SocketHandler) handleRead(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("SocketHandler handleRead context canceled")
			return
		default:
			buffer := make([]byte, 1024*1024) // Adjust the buffer size as needed

			if h.readDeadline > 0 {
				conn.SetReadDeadline(time.Now().Add(h.readDeadline))
			}

			n, err := conn.Read(buffer)
			if err != nil {
				if err == io.EOF {
					fmt.Println("Connection closed by peer.")
					return
				}

				fmt.Printf("SocketHandler read error: %v\n", err)
				h.SendError(fmt.Errorf("read error: %w", err))
				return
			}

			h.dataChannel <- buffer[:n]
		}
	}
}

// Close terminates the handler's operations and closes the Unix socket connection.
func (h *SocketHandler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.listener != nil {
		h.listener.Close()
	}

	for conn := range h.connections {
		conn.Close()
	}
	h.connections = nil

	close(h.dataChannel)

	// if h.mode == Server {
	// 	err := syscall.Unlink(h.address)
	// 	if err != nil && err != syscall.ENOENT {
	// 		return fmt.Errorf("error unlinking socket: %w", err)
	// 	}
	// }

	return nil
}

// SendError sends an error message to the events channel if it is defined.
func (h *SocketHandler) SendError(err error) {
	if h.events != nil {
		h.events <- err
	}
}
