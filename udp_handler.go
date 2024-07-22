// Package urihandler provides utilities for handling different types of socket communications.
package urihandler

import (
	"net"
	"sync"
	"time"
)

// UDPStatus represents the status of a UDPHandler, detailing its configuration and state.
type UDPStatus struct {
	Mode           Mode
	Role           Role
	Address        string
	ReadDeadline   time.Duration
	WriteDeadline  time.Duration
	AllowedSources []string // List of source addresses allowed to send data
	Destinations   []string // List of destination addresses to send data
}

// Getter methods for UDPStatus
func (u UDPStatus) GetMode() Mode      { return u.Mode }
func (u UDPStatus) GetRole() Role      { return u.Role }
func (u UDPStatus) GetAddress() string { return u.Address }

// UDPHandler manages UDP network communication, supporting roles as sender (writer) or receiver (reader).
type UDPHandler struct {
	address        string
	conn           *net.UDPConn
	readDeadline   time.Duration
	writeDeadline  time.Duration
	mode           Mode
	role           Role
	dataChannel    chan []byte
	events         chan error
	allowedSources map[string]struct{}     // Set of source IPs allowed to send data to this handler
	destinations   map[string]*net.UDPAddr // UDP addresses for sending data
	mu             sync.RWMutex            // Mutex to protect concurrent access to handler state
	status         UDPStatus
}

// NewUDPHandler initializes a new UDPHandler with specified settings.
func NewUDPHandler(mode Mode, role Role, dataChannel chan []byte, events chan error, address string, readDeadline, writeDeadline time.Duration, sources, destinations []string) *UDPHandler {
	handler := &UDPHandler{
		mode:           mode,
		role:           role,
		dataChannel:    dataChannel,
		events:         events,
		address:        address,
		readDeadline:   readDeadline,
		writeDeadline:  writeDeadline,
		allowedSources: make(map[string]struct{}),
		destinations:   make(map[string]*net.UDPAddr),
		status: UDPStatus{
			Mode:           mode,
			Role:           role,
			Address:        address,
			ReadDeadline:   readDeadline,
			WriteDeadline:  writeDeadline,
			AllowedSources: sources,
			Destinations:   destinations,
		},
	}

	// Populate allowed sources.
	for _, src := range sources {
		if _, err := net.ResolveUDPAddr("udp", src); err == nil {
			handler.allowedSources[src] = struct{}{}
		}
	}

	// Populate destinations.
	for _, dst := range destinations {
		if addr, err := net.ResolveUDPAddr("udp", dst); err == nil {
			handler.destinations[dst] = addr
		}
	}

	return handler
}

// Status returns the current status of the UDPHandler.
func (h *UDPHandler) Status() UDPStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Convert internal maps to slices for easier external consumption.
	sources := make([]string, 0, len(h.allowedSources))
	for src := range h.allowedSources {
		sources = append(sources, src)
	}

	destinations := make([]string, 0, len(h.destinations))
	for dst := range h.destinations {
		destinations = append(destinations, dst)
	}

	h.status.AllowedSources = sources
	h.status.Destinations = destinations
	h.status.Address = h.conn.LocalAddr().String()

	return h.status
}

// Open starts the UDPHandler, setting up a UDP connection for sending or receiving data.
func (h *UDPHandler) Open() error {
	udpAddr, err := net.ResolveUDPAddr("udp", h.address)
	if err != nil {
		return err
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}
	h.conn = conn

	h.mu.Lock()
	h.status.Address = conn.LocalAddr().String()
	h.mu.Unlock()

	if h.role == Writer {
		go h.sendData()
	} else if h.role == Reader {
		go h.receiveData()
	}
	return nil
}

// AddSource adds a source address to the allowed sources list.
func (h *UDPHandler) AddSource(addr string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.allowedSources[addr] = struct{}{}
	return nil
}

// RemoveSource removes a source address from the allowed sources list.
func (h *UDPHandler) RemoveSource(addr string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.allowedSources, addr)
	return nil
}

// AddDestination adds a destination address to the destinations list.
func (h *UDPHandler) AddDestination(addr string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	h.destinations[addr] = udpAddr
	return nil
}

// RemoveDestination removes a destination address from the destinations list.
func (h *UDPHandler) RemoveDestination(addr string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.destinations, addr)
	return nil
}

// sendData handles sending data to the configured destinations.
func (h *UDPHandler) sendData() {
	defer h.conn.Close()

	if h.writeDeadline > 0 {
		h.conn.SetWriteDeadline(time.Now().Add(h.writeDeadline))
	}

	for {
		message, ok := <-h.dataChannel
		if !ok {
			break // Channel closed
		}

		for _, addr := range h.destinations {
			_, err := h.conn.WriteToUDP(message, addr)
			if err != nil {
				h.SendError(err)
				break
			}
		}
	}
}

// receiveData handles receiving data from allowed sources.
func (h *UDPHandler) receiveData() {
	defer h.conn.Close()

	if h.readDeadline > 0 {
		h.conn.SetReadDeadline(time.Now().Add(h.readDeadline))
	}

	bufferPool := sync.Pool{
		New: func() interface{} {
			buffer := make([]byte, 2048)
			return &buffer
		},
	}

	for {
		rawBuffer := bufferPool.Get().(*[]byte)
		n, addr, err := h.conn.ReadFromUDP(*rawBuffer)
		if err != nil {
			bufferPool.Put(rawBuffer)
			h.SendError(err)
			continue
		}

		h.mu.RLock()
		_, ok := h.allowedSources[addr.String()]
		h.mu.RUnlock()

		if !ok {
			bufferPool.Put(rawBuffer)
			continue
		}

		h.dataChannel <- (*rawBuffer)[:n]
		bufferPool.Put(rawBuffer)
	}
}

// Close terminates the handler's operations and closes the UDP connection.
func (h *UDPHandler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn != nil {
		h.conn.Close()
	}
	close(h.dataChannel)
	return nil
}

func (h *UDPHandler) SendError(err error) {
	if h.events != nil {
		h.events <- err
	}
}
