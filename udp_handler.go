// Package urihandler provides utilities for handling different types of socket communications.
package urihandler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// UDPStatus represents the status of a UDPHandler, detailing its configuration and state.
type UDPStatus struct {
	mode           Mode
	role           Role
	address        string
	readDeadline   time.Duration
	writeDeadline  time.Duration
	allowedSources []string // List of source addresses allowed to send data
	destinations   []string // List of destination addresses to send data
	isOpen         bool
}

// Getter methods for UDPStatus
func (u UDPStatus) GetMode() Mode      { return u.mode }
func (u UDPStatus) GetRole() Role      { return u.role }
func (u UDPStatus) GetAddress() string { return u.address }
func (u UDPStatus) IsOpen() bool       { return u.isOpen }

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
func NewUDPHandler(
	mode Mode,
	role Role,
	dataChannel chan []byte,
	events chan error,
	address string,
	readDeadline,
	writeDeadline time.Duration,
	sources,
	destinations []string,
) URIHandler { // Changed return type to URIHandler
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
			mode:           mode,
			role:           role,
			address:        address,
			readDeadline:   readDeadline,
			writeDeadline:  writeDeadline,
			allowedSources: sources,
			destinations:   destinations,
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
		} else {
			fmt.Printf("Failed to resolve destination %s: %v\n", dst, err)
		}
	}

	return handler
}

func (h *UDPHandler) GetDataChannel() chan []byte {
	return h.dataChannel
}

func (h *UDPHandler) GetEventsChannel() chan error {
	return h.events
}

// udpBufferPool is a pool of byte slices used to reduce garbage collection overhead.
var udpBufferPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 2048)
		return &b
	},
}

// Status returns the current status of the UDPHandler.
func (h *UDPHandler) Status() Status { // Changed return type to Status interface
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

	h.status.allowedSources = sources
	h.status.destinations = destinations
	if h.conn != nil {
		h.status.address = h.conn.LocalAddr().String()
	} else {
		h.status.isOpen = false
	}

	return h.status
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

// Close terminates the handler's operations and closes the UDP connection.
func (h *UDPHandler) Close() error {
	fmt.Println("Closing UDPHandler")

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn != nil {
		h.conn.Close()
	}

	h.status.isOpen = false
	return nil
}

func (h *UDPHandler) SendError(err error) {
	if h.events != nil {
		h.events <- err
	}
}

// Open starts the UDPHandler, setting up a UDP connection for sending or receiving data.
func (h *UDPHandler) Open(ctx context.Context) error {
	if h.role == Reader {
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
		h.status.address = conn.LocalAddr().String()
		h.mu.Unlock()

		go h.receiveData(ctx)
	} else if h.role == Writer {
		h.mu.Lock()
		h.status.address = "write-only, no specific local binding"
		h.mu.Unlock()

		go h.sendData(ctx)
	} else {
		fmt.Print("Invalid role specified\n")
		return errors.New("invalid role specified")
	}

	h.status.isOpen = true
	return nil
}

// sendData handles sending data to the configured destinations.
func (h *UDPHandler) sendData(ctx context.Context) {
	var wg sync.WaitGroup
	defer h.Close()

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("UDPHandler sendData context canceled: %s\n", h.address)
			return
		case message, ok := <-h.dataChannel:
			if !ok {
				fmt.Println("Data channel closed, exiting sendData.")
				return // Channel closed
			}

			wg.Add(len(h.destinations))

			for _, addr := range h.destinations {
				go func(destAddr *net.UDPAddr) {
					defer wg.Done()

					const maxRetries = 10
					const retryDelay = 1 * time.Millisecond

					for i := 0; i < maxRetries; i++ {
						// Check if context is done before each retry
						select {
						case <-ctx.Done():
							fmt.Printf("UDPHandler sendData context canceled during retry %d\n", i+1)
							return
						default:
						}

						conn, err := net.DialUDP("udp", nil, destAddr)
						if err != nil {
							h.SendError(err)
							time.Sleep(retryDelay)
							continue
						}

						if h.writeDeadline > 0 {
							conn.SetWriteDeadline(time.Now().Add(h.writeDeadline))
						}

						_, err = conn.Write(message)
						conn.Close() // Close the connection after each send

						if err == nil {
							return
						}

						// Handle specific errors and retry
						if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "connection refused" {
							h.SendError(fmt.Errorf("retry %d/%d: connection refused, retrying", i+1, maxRetries))
							time.Sleep(retryDelay)
							continue
						}

						h.SendError(err)
						return
					}
				}(addr)
			}

			wg.Wait() // Wait for all destinations to be processed before fetching the next message
		}
	}
}

// receiveData handles receiving data from allowed sources.
func (h *UDPHandler) receiveData(ctx context.Context) {
	defer h.Close()

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("UDPHandler receiveData context canceled: %s\n", h.address)
			return
		default:
			buffer := make([]byte, 1024*1024)

			if h.readDeadline > 0 {
				h.conn.SetReadDeadline(time.Now().Add(h.readDeadline))
			}

			n, addr, err := h.conn.ReadFromUDP(buffer)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					fmt.Printf("UDPHandler read timeout: %v\n", err)
					h.SendError(err)
					continue
				}

				fmt.Printf("UDPHandler read error: %v\n", err)
				h.SendError(err)
				return
			}

			// Check if the source is allowed
			if len(h.allowedSources) > 0 {
				h.mu.RLock()
				_, ok := h.allowedSources[addr.String()]
				h.mu.RUnlock()

				if !ok {
					fmt.Printf("Received data from unauthorized source: %s\n", addr.String())
					continue
				}
			}

			h.dataChannel <- buffer[:n]
		}
	}
}
