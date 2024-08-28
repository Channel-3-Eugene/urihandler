package urihandler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestSocketHandler_New verifies that a new SocketHandler is correctly initialized with specified parameters.
func TestSocketHandler_New(t *testing.T) {
	socketPath := randSocketPath()
	readDeadline := 5 * time.Millisecond
	writeDeadline := 5 * time.Millisecond
	dataChannel := make(chan []byte)
	events := make(chan error)

	handler := NewSocketHandler(Server, Writer, dataChannel, events, socketPath, readDeadline, writeDeadline)

	// Assert that all properties are set as expected.
	assert.Equal(t, socketPath, handler.(*SocketHandler).address)
	assert.Equal(t, Writer, handler.(*SocketHandler).role)
	assert.Equal(t, readDeadline, handler.(*SocketHandler).readDeadline)
	assert.Equal(t, writeDeadline, handler.(*SocketHandler).writeDeadline)
	assert.NotNil(t, handler.(*SocketHandler).dataChannel)
	assert.NotNil(t, handler.(*SocketHandler).events)
}

// TestSocketHandler_OpenAndClose tests the Open and Close methods of the SocketHandler to ensure sockets are correctly managed.
func TestSocketHandler_OpenAndClose(t *testing.T) {
	socketPath := randSocketPath()
	dataChannel := make(chan []byte)
	events := make(chan error)

	// Ensure any existing socket with the same name is removed before starting the test.
	os.Remove(socketPath)

	// Open the handler and verify that the socket exists after opening.
	handler := NewSocketHandler(Server, Writer, dataChannel, events, socketPath, 0, 0)
	err := handler.(*SocketHandler).Open(context.Background())
	assert.Nil(t, err)

	// Give some time for the socket to be created
	time.Sleep(10 * time.Millisecond)

	// Check if the socket file exists
	_, err = os.Stat(socketPath)
	assert.Nil(t, err)

	// Close the handler and check if the socket is properly closed.
	err = handler.(*SocketHandler).Close()
	assert.Nil(t, err)
}

// TestSocketHandler_OpenAndCancel tests the Open method of the SocketHandler and verifies that it correctly handles context cancellation.
func TestSocketHandler_OpenAndCancel(t *testing.T) {
	socketPath := randSocketPath()
	dataChannel := make(chan []byte)
	events := make(chan error)

	// Ensure any existing socket with the same name is removed before starting the test.
	os.Remove(socketPath)

	// Create a context with cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Open the handler and verify that the socket exists after opening.
	handler := NewSocketHandler(Server, Writer, dataChannel, events, socketPath, 0, 0)
	err := handler.(*SocketHandler).Open(ctx)
	assert.Nil(t, err)

	// Check if the socket file exists
	_, err = os.Stat(socketPath)
	assert.Nil(t, err)

	// Cancel the context and check if the handler stops correctly
	cancel()

	// Give some time for the handler to stop
	time.Sleep(10 * time.Millisecond)

	// Check if the socket is properly closed after cancellation.
	_, err = os.Stat(socketPath)
	assert.True(t, os.IsNotExist(err), "Socket file should not exist after context cancellation")

	// Clean up the created socket after the test.
	os.Remove(socketPath)
}

// TestSocketHandler_DataFlow tests the complete cycle of writing to and reading from the socket.
func TestSocketHandler_DataFlow(t *testing.T) {
	socketPath := randSocketPath()
	readChannel := make(chan []byte)
	writeChannel := make(chan []byte)
	events := make(chan error)

	// Initialize writer and reader handlers.
	writer := NewSocketHandler(Client, Writer, writeChannel, events, socketPath, 0, 0)
	reader := NewSocketHandler(Server, Reader, readChannel, events, socketPath, 0, 0)

	err := reader.(*SocketHandler).Open(context.Background())
	assert.Nil(t, err)
	defer reader.(*SocketHandler).Close()

	time.Sleep(100 * time.Millisecond) // Give server time to start

	err = writer.(*SocketHandler).Open(context.Background())
	assert.Nil(t, err)
	defer writer.(*SocketHandler).Close()

	// Write data to the socket.
	testData := []byte("hello, world")
	go func() {
		writeChannel <- testData
	}()

	// Attempt to read the data and check if it matches what was written.
	select {
	case <-time.After(1 * time.Second):
		assert.Fail(t, "Timeout waiting for data")
	case data := <-readChannel:
		assert.Equal(t, testData, data)
	}

	// Clean up resources and remove test socket.
	os.Remove(socketPath)
}

// TestSocketHandler_SocketServerWriterClientReader tests the interaction between a server set to write and a client set to read.
func TestSocketHandler_SocketServerWriterClientReader(t *testing.T) {
	socketPath := randSocketPath()
	serverChannel := make(chan []byte)
	clientChannel := make(chan []byte)
	events := make(chan error)

	// Initialize server to write data.
	serverWriter := NewSocketHandler(Server, Writer, serverChannel, events, socketPath, 0, 0)
	err := serverWriter.(*SocketHandler).Open(context.Background())
	assert.Nil(t, err)
	defer serverWriter.(*SocketHandler).Close()

	time.Sleep(100 * time.Millisecond) // Short sleep to prevent busy waiting

	// Initialize client to read data.
	clientReader := NewSocketHandler(Client, Reader, clientChannel, events, socketPath, 10*time.Millisecond, 10*time.Millisecond)
	err = clientReader.(*SocketHandler).Open(context.Background())
	assert.Nil(t, err)
	defer clientReader.(*SocketHandler).Close()

	t.Run("TestSocketHandler_Status", func(t *testing.T) {
		status := serverWriter.(*SocketHandler).Status()
		assert.Equal(t, serverWriter.(*SocketHandler).address, status.GetAddress()) // Updated
		assert.Equal(t, Server, status.GetMode())                                   // Updated
		assert.Equal(t, Writer, status.GetRole())                                   // Updated
	})

	t.Run("TestSocketHandler_WriteData", func(t *testing.T) {
		randBytes := make([]byte, 188)
		_, err := rand.Read(randBytes)
		assert.Nil(t, err)

		go func() {
			serverChannel <- randBytes
		}()

		select {
		case <-time.After(10 * time.Millisecond):
			t.Error("Timeout waiting for data")
		case data := <-clientChannel:
			assert.Equal(t, randBytes, data)
		}
	})
}

// TestSocketHandler_SocketServerReaderClientWriter tests the interaction between a server set to read and a client set to write.
func TestSocketHandler_SocketServerReaderClientWriter(t *testing.T) {
	socketPath := randSocketPath()
	serverChannel := make(chan []byte)
	clientChannel := make(chan []byte)
	events := make(chan error)

	// Initialize server to read data.
	serverReader := NewSocketHandler(Server, Reader, serverChannel, events, socketPath, 0, 0)
	err := serverReader.(*SocketHandler).Open(context.Background())
	assert.Nil(t, err)
	defer serverReader.(*SocketHandler).Close()

	time.Sleep(100 * time.Millisecond) // Short sleep to prevent busy waiting

	// Initialize client to write data.
	clientWriter := NewSocketHandler(Client, Writer, clientChannel, events, socketPath, 10*time.Millisecond, 10*time.Millisecond)
	err = clientWriter.(*SocketHandler).Open(context.Background())
	assert.Nil(t, err)
	defer clientWriter.(*SocketHandler).Close()

	t.Run("TestSocketHandler_Status", func(t *testing.T) {
		status := serverReader.(*SocketHandler).Status()
		assert.Equal(t, Server, status.GetMode()) // Updated
		assert.Equal(t, Reader, status.GetRole()) // Updated

		status = clientWriter.(*SocketHandler).Status()
		assert.Equal(t, socketPath, status.GetAddress()) // Updated
		assert.Equal(t, Client, status.GetMode())        // Updated
		assert.Equal(t, Writer, status.GetRole())        // Updated
	})

	t.Run("TestSocketHandler_WriteData", func(t *testing.T) {
		randBytes := make([]byte, 188)
		_, err := rand.Read(randBytes)
		assert.Nil(t, err)

		go func() {
			clientChannel <- randBytes
		}()

		select {
		case <-time.After(10 * time.Millisecond):
			assert.Fail(t, "Timeout waiting for data")
		case data := <-serverChannel:
			assert.Equal(t, randBytes, data)
		}
	})
}

// randSocketPath generates a random path for a Unix socket used in testing.
func randSocketPath() string {
	randBytes := make([]byte, 8)
	_, err := rand.Read(randBytes)
	if err != nil {
		panic(err) // It's better to handle the error properly in real applications.
	}
	return "/tmp/" + hex.EncodeToString(randBytes) + ".sock"
}
