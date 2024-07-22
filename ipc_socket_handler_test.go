package urihandler

import (
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
	assert.Equal(t, socketPath, handler.socketPath)
	assert.Equal(t, Writer, handler.role)
	assert.Equal(t, readDeadline, handler.readDeadline)
	assert.Equal(t, writeDeadline, handler.writeDeadline)
	assert.NotNil(t, handler.dataChannel)
	assert.NotNil(t, handler.events)
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
	err := handler.Open()
	assert.Nil(t, err)

	// Give some time for the socket to be created
	time.Sleep(100 * time.Millisecond)

	// Check if the socket file exists
	_, err = os.Stat(socketPath)
	assert.Nil(t, err)

	// Close the handler and check if the socket is properly closed.
	err = handler.Close()
	assert.Nil(t, err)

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

	err := reader.Open()
	assert.Nil(t, err)
	defer reader.Close()

	time.Sleep(100 * time.Millisecond) // Give server time to start

	err = writer.Open()
	assert.Nil(t, err)
	defer writer.Close()

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
	randomSocketPath := randSocketPath()
	serverChannel := make(chan []byte)
	clientChannel := make(chan []byte)
	events := make(chan error)

	// Initialize server to write data.
	serverWriter := NewSocketHandler(Server, Writer, serverChannel, events, randomSocketPath, 0, 0)
	err := serverWriter.Open()
	assert.Nil(t, err)
	defer serverWriter.Close()

	time.Sleep(100 * time.Millisecond) // Short sleep to prevent busy waiting

	// Initialize client to read data.
	clientReader := NewSocketHandler(Client, Reader, clientChannel, events, randomSocketPath, 10*time.Millisecond, 10*time.Millisecond)
	err = clientReader.Open()
	assert.Nil(t, err)
	defer clientReader.Close()

	t.Run("TestSocketHandler_Status", func(t *testing.T) {
		status := serverWriter.Status()
		assert.Equal(t, serverWriter.socketPath, status.Address)
		assert.Equal(t, Server, status.Mode)
		assert.Equal(t, Writer, status.Role)
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
	randomSocketPath := randSocketPath()
	serverChannel := make(chan []byte)
	clientChannel := make(chan []byte)
	events := make(chan error)

	// Initialize server to read data.
	serverReader := NewSocketHandler(Server, Reader, serverChannel, events, randomSocketPath, 0, 0)
	err := serverReader.Open()
	assert.Nil(t, err)
	defer serverReader.Close()

	time.Sleep(100 * time.Millisecond) // Short sleep to prevent busy waiting

	// Initialize client to write data.
	clientWriter := NewSocketHandler(Client, Writer, clientChannel, events, randomSocketPath, 10*time.Millisecond, 10*time.Millisecond)
	err = clientWriter.Open()
	assert.Nil(t, err)
	defer clientWriter.Close()

	t.Run("TestSocketHandler_Status", func(t *testing.T) {
		status := serverReader.Status()
		assert.Equal(t, Server, status.Mode)
		assert.Equal(t, Reader, status.Role)

		status = clientWriter.Status()
		assert.Equal(t, randomSocketPath, status.Address)
		assert.Equal(t, Client, status.Mode)
		assert.Equal(t, Writer, status.Role)
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
