package urihandler

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// TCPStatus represents the status of a TCPHandler instance.
type TCPStatus struct {
	Mode        Mode              // Mode represents the operational mode of the TCPHandler.
	Role        Role              // Role represents the role of the TCPHandler, whether it's a server or client.
	Address     string            // Address represents the network address the TCPHandler is bound to.
	Connections map[string]string // Connections holds a map of connection information (local address to remote address).
}

// GetMode returns the operational mode of the TCPHandler.
func (t TCPStatus) GetMode() Mode {
	return t.Mode
}

// GetRole returns the role of the TCPHandler.
func (t TCPStatus) GetRole() Role {
	return t.Role
}

// GetAddress returns the network address the TCPHandler is bound to.
func (t TCPStatus) GetAddress() string {
	return t.Address
}

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
) URIHandler { // Changed return type to URIHandler
	handler := &TCPHandler{
		mode:          mode,
		role:          role,
		dataChannel:   dataChannel,
		events:        events,
		address:       address,
		readDeadline:  readDeadline,
		writeDeadline: writeDeadline,
		connections:   make(map[net.Conn]struct{}),
	}

	// Initialize TCPStatus with default values.
	handler.status = TCPStatus{
		Address:     address,
		Mode:        mode,
		Role:        role,
		Connections: make(map[string]string),
	}

	return handler
}

func (h *TCPHandler) GetDataChannel() chan []byte {
	return h.dataChannel
}

func (h *TCPHandler) GetEventsChannel() chan error {
	return h.events
}

// Open starts the TCPHandler instance based on its operational mode.
func (h *TCPHandler) Open() error {
	if h.mode == Client {
		return h.connectClient()
	} else if h.mode == Server {
		return h.startServer()
	}
	return fmt.Errorf("invalid mode specified")
}

// Status returns the current status of the TCPHandler.
func (h *TCPHandler) Status() Status {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Populate the connections map with active connection information.
	for c := range h.connections {
		h.status.Connections[c.LocalAddr().String()] = c.RemoteAddr().String()
	}

	return h.status
}

// connectClient establishes a client connection to the TCP server.
func (h *TCPHandler) connectClient() error {
	conn, err := net.Dial("tcp", h.address)
	if err != nil {
		fmt.Printf("Failed to connect to %s: %v\n", h.address, err)
		return err
	}

	h.mu.Lock()
	h.connections[conn] = struct{}{}
	h.mu.Unlock()

	go h.manageStream(conn)
	return nil
}

// startServer starts the TCP server and listens for incoming client connections.
func (h *TCPHandler) startServer() error {
	ln, err := net.Listen("tcp", h.address)
	if err != nil {
		return err
	}
	h.listener = ln
	h.mu.Lock()
	h.status.Address = ln.Addr().String()
	h.mu.Unlock()
	go h.acceptClients()
	return nil
}

// acceptClients accepts incoming client connections and manages them concurrently.
func (h *TCPHandler) acceptClients() {
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

		go h.manageStream(conn)
	}
}

// manageStream manages the TCP connection stream based on the role of the TCPHandler.
func (h *TCPHandler) manageStream(conn net.Conn) {
	defer func() {
		conn.Close()
		h.mu.Lock()
		delete(h.connections, conn)
		h.mu.Unlock()
	}()

	// Handle data transmission based on the role of the TCPHandler.
	if h.role == Writer {
		h.handleWrite(conn)
	} else if h.role == Reader {
		h.handleRead(conn)
	}
}

// handleWrite manages writing data to the TCP connection.
func (h *TCPHandler) handleWrite(conn net.Conn) {
	for {
		message, ok := <-h.dataChannel
		if !ok {
			fmt.Println("Data channel closed, stopping handleWrite.")
			break // Channel closed
		}

		const maxRetries = 10
		const retryDelay = 1 * time.Millisecond

		for i := 0; i < maxRetries; i++ {
			if h.writeDeadline > 0 {
				conn.SetWriteDeadline(time.Now().Add(h.writeDeadline))
			}

			_, err := conn.Write(message)
			if err == nil {
				break
			}

			if netErr, ok := err.(net.Error); ok && netErr.Temporary() {
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

// handleRead manages reading data from the TCP connection.
func (h *TCPHandler) handleRead(conn net.Conn) {
	for {
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
