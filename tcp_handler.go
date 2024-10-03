package urihandler

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// TCPStatus represents the status of a TCPHandler instance.
type TCPStatus struct {
	mode        Mode              // Mode represents the operational mode of the TCPHandler.
	role        Role              // Role represents the role of the TCPHandler, whether it's a server or client.
	address     string            // Address represents the network address the TCPHandler is bound to.
	connections map[string]string // Connections holds a map of connection information (local address to remote address).
	isOpen      bool              // IsOpen indicates whether the TCPHandler is open.
}

func (t TCPStatus) GetMode() Mode      { return t.mode }
func (t TCPStatus) GetRole() Role      { return t.role }
func (t TCPStatus) GetAddress() string { return t.address }
func (t TCPStatus) IsOpen() bool       { return t.isOpen }

// TCPHandler manages TCP connections and provides methods for handling TCP communication.
type TCPHandler struct {
	address       string                // address represents the network address the TCPHandler is bound to.
	readDeadline  time.Duration         // readDeadline represents the read deadline for incoming data.
	writeDeadline time.Duration         // writeDeadline represents the write deadline for outgoing data.
	mode          Mode                  // mode represents the operational mode of the TCPHandler.
	role          Role                  // role represents the role of the TCPHandler, whether it's a server or client.
	listener      net.Listener          // listener represents the TCP listener for server mode.
	dataChannel   chan []byte           // dataChannel is a channel for sending and receiving data.
	events        chan error            // events is a channel for sending error events.
	connections   map[net.Conn]struct{} // connections holds a map of active TCP connections.
	mu            sync.RWMutex          // Use RWMutex to allow concurrent reads.

	status TCPStatus // status represents the current status of the TCPHandler.
}

// NewTCPHandler creates a new instance of TCPHandler with the specified configuration.
func NewTCPHandler(
	mode Mode,
	role Role,
	dataChannel chan []byte,
	events chan error,
	address string,
	readDeadline,
	writeDeadline time.Duration,
) URIHandler {
	handler := &TCPHandler{
		mode:          mode,
		role:          role,
		dataChannel:   dataChannel,
		events:        events,
		address:       address,
		readDeadline:  readDeadline,
		writeDeadline: writeDeadline,
		connections:   make(map[net.Conn]struct{}),
		status: TCPStatus{
			mode:    mode,
			role:    role,
			address: address,
		},
	}

	handler.status = handler.Status().(TCPStatus)

	fmt.Printf("New TCPHandler created: %#v\n", handler.status)

	return handler
}

func (h *TCPHandler) GetDataChannel() chan []byte {
	return h.dataChannel
}

func (h *TCPHandler) GetEventsChannel() chan error {
	return h.events
}

// Open starts the TCPHandler instance based on its operational mode.
func (h *TCPHandler) Open(ctx context.Context) error {
	if h.mode == Client {
		return h.connectClient(ctx)
	} else if h.mode == Server {
		return h.startServer(ctx)
	}
	return fmt.Errorf("invalid mode specified")
}

// Status returns the current status of the TCPHandler.
func (h *TCPHandler) Status() Status {
	h.mu.RLock()
	defer h.mu.RUnlock()

	status := TCPStatus{
		mode:        h.mode,
		role:        h.role,
		address:     h.address,
		connections: make(map[string]string),
	}

	for c := range h.connections {
		status.connections[c.LocalAddr().String()] = c.RemoteAddr().String()
	}

	status.isOpen = h.listener != nil && len(h.connections) > 0

	h.status = status
	return h.status
}

// connectClient establishes a client connection to the TCP server.
func (h *TCPHandler) connectClient(ctx context.Context) error {
	conn, err := net.Dial("tcp", h.address)
	if err != nil {
		fmt.Printf("Failed to connect to %s: %v\n", h.address, err)
		return err
	}

	h.mu.Lock()
	h.connections[conn] = struct{}{}
	h.mu.Unlock()

	h.status.address = conn.LocalAddr().String()

	go h.manageStream(ctx, conn)
	return nil
}

// startServer starts the TCP server and listens for incoming client connections.
func (h *TCPHandler) startServer(ctx context.Context) error {
	ln, err := net.Listen("tcp", h.address)
	if err != nil {
		return err
	}
	h.listener = ln
	h.mu.Lock()
	h.address = ln.Addr().String()
	h.mu.Unlock()
	go h.acceptClients(ctx)
	return nil
}

// acceptClients accepts incoming client connections and manages them concurrently.
func (h *TCPHandler) acceptClients(ctx context.Context) {
	for {
		conn, err := h.listener.Accept()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Temporary() {
				fmt.Printf("Temporary accept error: %v\n", err)
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

// manageStream manages the TCP connection stream based on the role of the TCPHandler.
func (h *TCPHandler) manageStream(ctx context.Context, conn net.Conn) {
	defer func() {
		conn.Close()
		h.mu.Lock()
		delete(h.connections, conn)
		h.mu.Unlock()
	}()

	// Handle data transmission based on the role of the TCPHandler.
	if h.role == Writer {
		h.handleWrite(ctx, conn)
	} else if h.role == Reader {
		h.handleRead(ctx, conn)
	}
}

// handleWrite manages writing data to the TCP connection.
func (h *TCPHandler) handleWrite(ctx context.Context, conn net.Conn) {
	defer h.Close()

	const maxRetries = 10
	const retryDelay = 1 * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("TCPHandler handleWrite context canceled: %s\n", h.address)
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
					fmt.Printf("TCPHandler handleWrite context canceled: %s\n", h.address)
					return
				default:
				}

				if h.writeDeadline > 0 {
					conn.SetWriteDeadline(time.Now().Add(h.writeDeadline))
				}

				_, err := conn.Write(message)
				if err == nil {
					break
				}

				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					fmt.Printf("Temporary write error: %v, retrying...\n", err)
					time.Sleep(retryDelay)
					continue
				}

				fmt.Printf("TCPHandler write error: %v\n", err)
				h.SendError(fmt.Errorf("write error: %w", err))
				return
			}
		}
	}
}

// handleRead manages reading data from the TCP connection.
func (h *TCPHandler) handleRead(ctx context.Context, conn net.Conn) {
	defer h.Close()

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("TCPHandler handleRead context canceled: %s\n", h.address)
			return
		default:
			buffer := make([]byte, 1024*1024)

			if h.readDeadline > 0 {
				conn.SetReadDeadline(time.Now().Add(h.readDeadline))
			}

			n, err := conn.Read(buffer)
			if err != nil {
				if err == io.EOF {
					fmt.Println("Connection closed by peer.")
					return
				}

				if netErr, ok := err.(net.Error); ok && netErr.Temporary() {
					fmt.Printf("Temporary read error: %v, retrying...\n", err)
					continue
				}

				fmt.Printf("TCPHandler read error: %v\n", err)
				h.SendError(fmt.Errorf("read error: %w", err))
				return
			}

			h.dataChannel <- buffer[:n]
		}
	}
}

// Close closes the TCPHandler instance and all active connections.
func (h *TCPHandler) Close() error {
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
	return nil
}

// SendError sends an error message to the events channel if it is defined.
func (h *TCPHandler) SendError(err error) {
	if h.events != nil {
		h.events <- err
	}
}
