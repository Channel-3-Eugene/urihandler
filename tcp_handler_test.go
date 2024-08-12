package urihandler

import (
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestTCPHandler_New tests the creation of a new TCPHandler instance.
func TestTCPHandler_New(t *testing.T) {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		fmt.Printf("TestTCPHandler_New took %v\n", duration)
	}()

	dataChannel := make(chan []byte)
	events := make(chan error)
	handler := NewTCPHandler(Server, Reader, dataChannel, events, ":0", 0, 0).(*TCPHandler)
	assert.Equal(t, ":0", handler.address)
	assert.Equal(t, 0*time.Second, handler.readDeadline)
	assert.Equal(t, 0*time.Second, handler.writeDeadline)
	assert.Equal(t, Server, handler.mode)
	assert.Equal(t, Reader, handler.role)
	assert.NotNil(t, handler.dataChannel)
	assert.NotNil(t, handler.connections)
}

// TestTCPHandler_ServerWriterClientReader tests the TCPHandler functionality with a server in writer role and a client in reader role.
func TestTCPHandler_ServerWriterClientReader(t *testing.T) {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		fmt.Printf("TestTCPHandler_ServerWriterClientReader took %v\n", duration)
	}()

	writerChannel := make(chan []byte)
	readerChannel := make(chan []byte)
	events := make(chan error)

	serverWriter := NewTCPHandler(Server, Writer, writerChannel, events, ":0", 0, 0).(*TCPHandler)
	err := serverWriter.Open()
	assert.Nil(t, err)

	status := serverWriter.Status().(TCPStatus)
	serverWriterAddr := status.Address

	clientReader := NewTCPHandler(Client, Reader, readerChannel, events, serverWriterAddr, 0, 0).(*TCPHandler)
	err = clientReader.Open()
	assert.Nil(t, err)

	t.Run("TestTCPHandler_Status", func(t *testing.T) {
		status := serverWriter.Status().(TCPStatus)
		assert.Equal(t, serverWriterAddr, status.Address)
		assert.Equal(t, Server, status.Mode)
		assert.Equal(t, Writer, status.Role)
	})

	t.Run("TestTCPHandler_WriteData", func(t *testing.T) {
		randBytes := make([]byte, 188)
		_, err := rand.Read(randBytes)
		if err != nil {
			t.Fatal("Failed to generate random bytes:", err)
		}

		go func() {
			writerChannel <- randBytes
		}()

		select {
		case <-time.After(10 * time.Millisecond):
			assert.Fail(t, "Timeout waiting for data")
		case data := <-readerChannel:
			assert.Equal(t, randBytes, data)
		}
	})
}

// TestTCPHandler_ServerReaderClientWriter tests the TCPHandler functionality with a server in reader role and a client in writer role.
func TestTCPHandler_ServerReaderClientWriter(t *testing.T) {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		fmt.Printf("TestTCPHandler_ServerReaderClientWriter took %v\n", duration)
	}()

	writerChannel := make(chan []byte)
	readerChannel := make(chan []byte)
	events := make(chan error)

	serverReader := NewTCPHandler(Server, Reader, readerChannel, events, ":0", 0, 0).(*TCPHandler)
	err := serverReader.Open()
	assert.Nil(t, err)

	status := serverReader.Status().(TCPStatus)
	serverReaderAddr := status.Address

	clientWriter := NewTCPHandler(Client, Writer, writerChannel, events, serverReaderAddr, 0, 0).(*TCPHandler)
	err = clientWriter.Open()
	assert.Nil(t, err)

	t.Run("TestTCPHandler_Status", func(t *testing.T) {
		status := serverReader.Status().(TCPStatus)
		assert.Equal(t, Server, status.Mode)
		assert.Equal(t, Reader, status.Role)

		status = clientWriter.Status().(TCPStatus)
		assert.Equal(t, serverReaderAddr, status.Address)
		assert.Equal(t, Client, status.Mode)
		assert.Equal(t, Writer, status.Role)
	})

	t.Run("TestTCPHandler_WriteData", func(t *testing.T) {
		randBytes := make([]byte, 188)
		_, err := rand.Read(randBytes)
		if err != nil {
			t.Fatal("Failed to generate random bytes:", err)
		}

		go func() {
			writerChannel <- randBytes
		}()

		select {
		case <-time.After(10 * time.Millisecond):
			assert.Fail(t, "Timeout waiting for data")
		case data := <-readerChannel:
			assert.Equal(t, randBytes, data)
		}
	})
}
