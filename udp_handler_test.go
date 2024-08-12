package urihandler

import (
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestUDPHandler_New verifies the creation and initialization of a new UDPHandler with specific configuration settings.
func TestUDPHandler_New(t *testing.T) {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		fmt.Printf("TestUDPHandler_New took %v\n", duration)
	}()

	sources := []string{}      // No allowed sources specified for this test.
	destinations := []string{} // No destinations specified for this test.
	dataChannel := make(chan []byte)
	events := make(chan error)

	// Create a new UDPHandler with a listening address, read and write timeouts, and roles specified.
	handler := NewUDPHandler(
		Peer,
		Reader,
		dataChannel,
		events,
		":0",
		10*time.Second,
		5*time.Second,
		sources,
		destinations,
	).(*UDPHandler)

	// Assert the handler is initialized with the correct configurations.
	assert.Equal(t, ":0", handler.address)
	assert.Equal(t, 10*time.Second, handler.readDeadline)
	assert.Equal(t, 5*time.Second, handler.writeDeadline)
	assert.Equal(t, Peer, handler.mode)
	assert.Equal(t, Reader, handler.role)
	assert.NotNil(t, handler.dataChannel)
	assert.Len(t, handler.allowedSources, 0)
	assert.Len(t, handler.destinations, 0)
}

// TestUDPHandler_DataFlow tests the UDP data flow from a writer to a reader to ensure data sent by the writer is correctly received by the reader.
func TestUDPHandler_DataFlow(t *testing.T) {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		fmt.Printf("TestUDPHandler_DataFlow took %v\n", duration)
	}()

	readChannel := make(chan []byte)
	writeChannel := make(chan []byte)
	events := make(chan error)

	// Setup writer and reader handlers, each on separate local UDP addresses.
	writer := NewUDPHandler(Peer, Writer, writeChannel, events, "[::1]:0", 0, 0, nil, nil).(*UDPHandler)
	err := writer.Open()
	assert.Nil(t, err)

	reader := NewUDPHandler(Peer, Reader, readChannel, events, "[::1]:0", 0, 0, nil, nil).(*UDPHandler)
	err = reader.Open()
	assert.Nil(t, err)

	// Ensure both the reader and the writer have unique, valid local addresses.
	assert.NotEmpty(t, reader.Status().Address)
	assert.NotEmpty(t, writer.Status().Address)
	assert.NotEqual(t, reader.Status().Address, writer.Status().Address)

	// Configure the reader to accept data from the writer's address.
	err = reader.AddSource(writer.Status().Address)
	assert.Nil(t, err)

	// Configure the writer to send data to the reader's address.
	err = writer.AddDestination(reader.Status().Address)
	assert.Nil(t, err)

	// Subtest to write data from the writer and verify it is received by the reader.
	t.Run("TestUDPHandler_WriteAndReceiveData", func(t *testing.T) {
		randBytes := make([]byte, 188) // Prepare a random byte slice for testing.
		_, _ = rand.Read(randBytes)    // Read random data into randBytes.

		go func() {
			writeChannel <- randBytes // Send data to the writer channel.
		}()

		// Wait to receive data on the reader channel or fail after a timeout.
		select {
		case <-time.After(10 * time.Millisecond):
			assert.Fail(t, "Timeout waiting for data") // Fail test if no data is received in time.
		case data := <-readChannel:
			assert.Equal(t, randBytes, data)
		}
	})

	// Clean up: Close both handlers.
	assert.Nil(t, writer.Close())
	assert.Nil(t, reader.Close())
}
