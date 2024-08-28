package urihandler

import (
	"context"
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

// TestUDPHandler_OpenAndClose tests the Open and Close methods of the UDPHandler to ensure the handler can be properly opened and closed.
func TestUDPHandler_OpenAndClose(t *testing.T) {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		fmt.Printf("TestUDPHandler_OpenAndClose took %v\n", duration)
	}()

	readChannel := make(chan []byte)
	events := make(chan error)

	// Create a new UDPHandler with a listening address, read and write timeouts, and roles specified.
	handler := NewUDPHandler(
		Peer,
		Reader,
		readChannel,
		events,
		":0",
		10*time.Second,
		5*time.Second,
		nil,
		nil,
	).(*UDPHandler)

	// Open the handler and verify that it is successfully opened.
	err := handler.Open(context.Background())
	assert.Nil(t, err)
	assert.True(t, handler.Status().IsOpen())

	// Close the handler and verify that it is successfully closed.
	err = handler.Close()
	assert.Nil(t, err)
	assert.False(t, handler.Status().IsOpen())
}

// TestUDPHandler_OpenAndCancel tests the Open method of the UDPHandler with a context that is canceled before the handler is fully opened.
func TestUDPHandler_OpenAndCancel(t *testing.T) {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		fmt.Printf("TestUDPHandler_OpenAndCancel took %v\n", duration)
	}()

	readChannel := make(chan []byte)
	events := make(chan error)

	// Create a new UDPHandler with a listening address, read and write timeouts, and roles specified.
	handler := NewUDPHandler(
		Peer,
		Reader,
		readChannel,
		events,
		":0",
		10*time.Second,
		5*time.Second,
		nil,
		nil,
	).(*UDPHandler)

	// Open the handler with a context that is canceled immediately.
	ctx, cancel := context.WithCancel(context.Background())
	err := handler.Open(ctx)
	assert.Nil(t, err)
	assert.True(t, handler.Status().IsOpen())

	// Cancel the context and verify that the handler is closed.
	cancel()
	time.Sleep(10 * time.Millisecond) // Wait for the handler to close.
	assert.False(t, handler.Status().IsOpen())
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

	err := writer.Open(context.Background())
	assert.NoError(t, err)

	reader := NewUDPHandler(Peer, Reader, readChannel, events, "[::1]:0", 0, 0, nil, nil).(*UDPHandler)
	err = reader.Open(context.Background())
	assert.NoError(t, err)

	// Ensure both the reader and the writer have unique, valid local addresses.
	assert.NotEmpty(t, reader.Status().GetAddress())                               // Updated
	assert.NotEmpty(t, writer.Status().GetAddress())                               // Updated
	assert.NotEqual(t, reader.Status().GetAddress(), writer.Status().GetAddress()) // Updated

	// Configure the writer to send data to the reader's address.
	err = writer.AddDestination(reader.Status().GetAddress()) // Updated
	assert.NoError(t, err)

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

	assert.Nil(t, writer.Close())
	assert.Nil(t, reader.Close())
}
