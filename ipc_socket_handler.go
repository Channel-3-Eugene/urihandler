package urihandler

import (
	"fmt"
	"io"
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

// GetMode returns the mode of the socket.
func (s SocketStatus) GetMode() Mode { return s.Mode }

// GetRole returns the role of the socket.
func (s SocketStatus) GetRole() Role { return s.Role }

// GetAddress returns the address the socket is bound to.
func (s SocketStatus) GetAddress() string { return s.Address }

// SocketHandler manages socket connections, providing methods to open, close, and manage streams.
type SocketHandler struct {
	socketPath    string
	readDeadline  time.Duration
	writeDeadline time.Duration
	mode          Mode
	role          Role
	listener      net.Listener
	dataChannel   chan []byte
	events        chan error
	connections   map[net.Conn]struct{}
	mu            sync.RWMutex // Use RWMutex to allow concurrent reads
	status        SocketStatus
}

// NewSocketHandler creates and initializes a new SocketHandler with the specified parameters.
func NewSocketHandler(mode Mode, role Role, dataChannel chan []byte, events chan error, socketPath string, readDeadline, writeDeadline time.Duration) *SocketHandler {
	return &SocketHandler{
		mode:          mode,
		role:          role,
		dataChannel:   dataChannel,
		events:        events,
		socketPath:    socketPath,
		readDeadline:  readDeadline,
		writeDeadline: writeDeadline,
		connections:   make(map[net.Conn]struct{}),
		status: SocketStatus{
			Address:       socketPath,
			Mode:          mode,
			Role:          role,
			Connections:   []string{},
			ReadDeadline:  readDeadline,
			WriteDeadline: writeDeadline,
		},
	}
}

// Open initializes the socket's server or client based on its mode.
func (h *SocketHandler) Open() error {
	if h.mode == Client {
		go h.connectClient()
		return nil
	} else if h.mode == Server {
		ln, err := net.Listen("unix", h.socketPath)
		if err != nil {
			h.SendError(fmt.Errorf("error creating socket: %w", err))
			return err
		}
		h.mu.Lock()
		h.listener = ln
		h.status.Address = ln.Addr().String()
		h.mu.Unlock()
		go h.startServer()
		return nil
	}
	return fmt.Errorf("invalid mode: %v", h.mode)
}

// Status returns the current status of the socket.
func (h *SocketHandler) Status() SocketStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()

	connections := []string{} // Reset the list
	for conn := range h.connections {
		// Using remote address or local if remote not available
		connDesc := conn.RemoteAddr().String()
		if connDesc == "" {
			connDesc = conn.LocalAddr().String()
		}
		connections = append(connections, connDesc)
	}
	h.status.Connections = connections

	return h.status
}

// connectClient manages the client connection to the server.
func (h *SocketHandler) connectClient() {
	conn, err := net.Dial("unix", h.socketPath)
	if err != nil {
		h.SendError(fmt.Errorf("error connecting to socket: %w", err))
		return
	}
	h.mu.Lock()
	h.connections[conn] = struct{}{}
	h.mu.Unlock()

	h.manageStream(conn)
}

// startServer starts the socket server and listens for incoming connections.
func (h *SocketHandler) startServer() {
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
	readBuffer := make([]byte, 4096) // Buffer size can be adjusted as needed
	if h.readDeadline > 0 {
		conn.SetReadDeadline(time.Now().Add(h.readDeadline))
	}
	for {
		n, err := conn.Read(readBuffer)
		if err != nil {
			if err != io.EOF {
				h.SendError(err)
			}
			break // Exit on error or when EOF is reached
		}
		h.dataChannel <- readBuffer[:n]
	}
}

// Close shuts down the socket and cleans up resources.
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
