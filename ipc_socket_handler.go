package urihandler

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
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

const bufferSize = 16384

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

// Buffer pool for reusing byte buffers to reduce memory allocations.
var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, bufferSize)
	},
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

// validateSocketPath ensures the Unix socket path is within system limits.
func validateSocketPath(address string) error {
	const maxUnixSocketPathLength = 108
	if len(address) > maxUnixSocketPathLength {
		return fmt.Errorf("socket path length exceeds the maximum of %d characters", maxUnixSocketPathLength)
	}
	if len(address) == 0 {
		return fmt.Errorf("socket path is empty")
	}
	return nil
}

// Open starts the SocketHandler, setting up a Unix socket connection for sending or receiving data.
func (h *SocketHandler) Open(ctx context.Context) error {

	fmt.Printf("SocketHandler opening: %#v\n", h.address)

	// Validate socket path length
	if err := validateSocketPath(h.address); err != nil {
		h.SendError(fmt.Errorf("invalid socket path: %w", err))
		return err
	}

	if h.mode == Server {
		// Clean up any existing socket file
		if _, err := os.Stat(h.address); err == nil {
			if err := os.Remove(h.address); err != nil {
				fmt.Println("Couldn't remove existing file:", err) // Add this for debugging
				h.SendError(fmt.Errorf("failed to remove existing socket file: %w", err))
				return err
			}
		}

		ln, err := net.Listen("unix", h.address)
		if err != nil {
			fmt.Println("Error creating socket:", err) // Add this for debugging
			h.SendError(fmt.Errorf("error creating socket: %w", err))
			return err
		}

		h.listener = ln
		h.mu.Lock()
		h.status.address = ln.Addr().String()
		h.mu.Unlock()
		go h.acceptConnections(ctx)
	} else if h.mode == Client {
		conn, err := net.Dial("unix", h.address)
		if err != nil {
			fmt.Println("Error connecting to socket:", err) // Add this for debugging
			h.SendError(fmt.Errorf("error connecting to socket: %w", err))
			return err
		}

		fmt.Println("Connected to socket:", h.address) // Add this for debugging

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
			fmt.Printf("SocketHandler acceptConnections context canceled: %s\n", h.address)
			return
		default:
			conn, err := h.listener.Accept()
			if err != nil {
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

		time.Sleep(1 * time.Millisecond) // Prevent busy looping if nothing happens
	}
}

// isRetryableError checks if the error is a retryable network error.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

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

	const maxRetries = 10
	const retryDelay = 1 * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("SocketHandler handleWrite context canceled: %s\n", h.address)
			return
		case message, ok := <-h.dataChannel:
			if !ok {
				fmt.Println("Data channel closed, stopping handleWrite.")
				return // Channel closed
			}

			for i := 0; i < maxRetries; i++ {
				// Check if context is done before writing
				select {
				case <-ctx.Done():
					fmt.Printf("SocketHandler handleWrite context canceled: %s\n", h.address)
					return
				default:
				}

				if h.writeDeadline > 0 {
					conn.SetWriteDeadline(time.Now().Add(h.writeDeadline))
				}

				_, err := conn.Write(message)
				if err == nil {
					// Successfully wrote the message, break out of retry loop
					break
				}

				// Retry on timeout
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					fmt.Println("Write timeout, retrying...")
					continue
				}

				// Handle EOF and other non-recoverable errors
				if err == io.EOF {
					fmt.Println("Connection closed by peer.")
					return
				}

				// Log other non-recoverable errors and stop
				fmt.Printf("SocketHandler write error: %v\n", err)
				h.SendError(fmt.Errorf("write error: %w", err))
				return
			}
		}
	}
}

// handleRead manages reading data from the connection using a buffer pool.
func (h *SocketHandler) handleRead(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("SocketHandler handleRead context canceled: %s\n", h.address)
			return
		default:
			buffer := bufferPool.Get().([]byte) // Get buffer from pool
			defer bufferPool.Put(buffer)        // Return buffer to pool when done

			if h.readDeadline > 0 {
				conn.SetReadDeadline(time.Now().Add(h.readDeadline))
			}

			n, err := conn.Read(buffer)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					fmt.Println("Read timeout, retrying...")
					continue // Retry instead of closing the connection
				} else if err == io.EOF {
					fmt.Println("Connection closed by peer.")
					return
				} else {
					fmt.Printf("SocketHandler read error: %v\n", err)
					h.SendError(fmt.Errorf("read error: %w", err))
					return
				}
			}

			h.dataChannel <- buffer[:n]
		}

		time.Sleep(1 * time.Millisecond) // Prevent busy looping if nothing happens
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

	if h.mode == Server {
		err := syscall.Unlink(h.address)
		if err != nil && err != syscall.ENOENT {
			return fmt.Errorf("error unlinking socket: %w", err)
		}
	}

	return nil
}

// SendError sends an error message to the events channel if it is defined.
func (h *SocketHandler) SendError(err error) {
	if h.events != nil {
		fmt.Printf("SocketHandler error: %v\n", err)
		h.events <- err
	}
}
