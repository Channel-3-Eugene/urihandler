package urihandler

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// SocketStatus defines the status of a SocketHandler including its mode, role, and current connections.
type SocketStatus struct {
	Mode          Mode
	Role          Role
	Address       string
	Connections   []string // List of connection identifiers for simplicity
	ReadDeadline  time.Duration
	WriteDeadline time.Duration
}

// Getter methods for SocketStatus
func (s SocketStatus) GetMode() Mode      { return s.Mode }
func (s SocketStatus) GetRole() Role      { return s.Role }
func (s SocketStatus) GetAddress() string { return s.Address }

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
			Mode:          mode,
			Role:          role,
			Address:       address,
			ReadDeadline:  readDeadline,
			WriteDeadline: writeDeadline,
			Connections:   []string{},
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
func (h *SocketHandler) Status() Status { // Changed return type to Status interface
	h.mu.RLock()
	defer h.mu.RUnlock()

	connections := make([]string, 0, len(h.connections))
	for conn := range h.connections {
		connections = append(connections, conn.RemoteAddr().String())
	}

	h.status.Connections = connections
	h.status.Address = h.address

	return h.status // The SocketStatus already implements the Status interface
}

// Open starts the SocketHandler, setting up a Unix socket connection for sending or receiving data.
func (h *SocketHandler) Open() error {
	if h.mode == Server {
		ln, err := net.Listen("unix", h.address)
		if err != nil {
			return fmt.Errorf("error creating socket: %w", err)
		}
		h.listener = ln
		go h.acceptConnections()
	} else if h.mode == Client {
		conn, err := net.Dial("unix", h.address)
		if err != nil {
			return fmt.Errorf("error connecting to socket: %w", err)
		}
		h.mu.Lock()
		h.connections[conn] = struct{}{}
		h.mu.Unlock()
		go h.manageStream(conn)
	}
	return nil
}

func (h *SocketHandler) acceptConnections() {
	for {
		conn, err := h.listener.Accept()
		if err != nil {
			h.SendError(err)
			continue
		}
		h.mu.Lock()
		h.connections[conn] = struct{}{}
		h.mu.Unlock()
		go h.manageStream(conn)
	}
}

// manageStream handles data transmission over the connection based on the socket's role.
func (h *SocketHandler) manageStream(conn net.Conn) {
	defer func() {
		conn.Close()
		h.mu.Lock()
		delete(h.connections, conn)
		h.mu.Unlock()
	}()

	if h.role == Writer {
		h.handleWrite(conn)
	} else if h.role == Reader {
		h.handleRead(conn)
	}
}

// handleWrite manages writing data to the connection.
func (h *SocketHandler) handleWrite(conn net.Conn) {
	if h.writeDeadline > 0 {
		conn.SetWriteDeadline(time.Now().Add(h.writeDeadline))
	}
	for {
		message, ok := <-h.dataChannel
		if !ok {
			break // Channel closed
		}
		_, err := conn.Write(message)
		if err != nil {
			h.SendError(err)
			break // Exit if there is an error writing
		}
	}
}

// handleRead manages reading data from the connection.
func (h *SocketHandler) handleRead(conn net.Conn) {
	for {
		buffer := socketBufferPool.Get().(*[]byte)
		*buffer = (*buffer)[:cap(*buffer)] // Ensure the buffer is fully utilized

		if h.readDeadline > 0 {
			conn.SetReadDeadline(time.Now().Add(h.readDeadline))
		}
		n, err := conn.Read(*buffer)
		if err != nil {
			socketBufferPool.Put(buffer)
			h.SendError(err)
			break // Exit on error or when EOF is reached
		}
		h.dataChannel <- (*buffer)[:n]
		socketBufferPool.Put(buffer)
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
	return nil
}

func (h *SocketHandler) SendError(err error) {
	if h.events != nil {
		h.events <- err
	}
}
