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
	Connections   map[string]string // Mapping of local to remote addresses for connections
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
			Connections:   make(map[string]string),
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

	h.status.Connections = connections
	h.status.Address = h.address

	return h.status
}

// Open starts the SocketHandler, setting up a Unix socket connection for sending or receiving data.
func (h *SocketHandler) Open() error {
	if h.mode == Server {
		ln, err := net.Listen("unix", h.address)
		if err != nil {
			return fmt.Errorf("error creating socket: %w", err)
		}
		h.listener = ln
		h.mu.Lock()
		h.status.Address = ln.Addr().String()
		h.mu.Unlock()
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
	for {
		message, ok := <-h.dataChannel
		if !ok {
			fmt.Println("Data channel closed, stopping handleWrite.")
			break // Channel closed
		}

		if h.writeDeadline > 0 {
			conn.SetWriteDeadline(time.Now().Add(h.writeDeadline))
		}

		_, err := conn.Write(message)
		if err != nil {
			h.SendError(fmt.Errorf("write error: %w", err))
			break // Exit if there is an error writing
		}
	}
}

// handleRead manages reading data from the connection without using a buffer pool.
func (h *SocketHandler) handleRead(conn net.Conn) {
	defer conn.Close()

	for {
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

// SendError sends an error message to the events channel if it is defined.
func (h *SocketHandler) SendError(err error) {
	if h.events != nil {
		h.events <- err
	}
}
