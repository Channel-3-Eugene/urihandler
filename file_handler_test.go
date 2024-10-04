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

// TestFileHandler_New verifies that a new FileHandler is correctly initialized with specified parameters.
func TestFileHandler_New(t *testing.T) {
	filePath := randFileName()
	readTimeout := 5 * time.Millisecond
	writeTimeout := 5 * time.Millisecond
	channel := make(chan []byte)
	events := make(chan error)

	handler := NewFileHandler(Peer, Writer, channel, events, filePath, false, readTimeout, writeTimeout).(*FileHandler)

	// Assert that all properties are set as expected.
	assert.Equal(t, filePath, handler.filePath)
	assert.Equal(t, Writer, handler.role)
	assert.Equal(t, false, handler.isFIFO)
	assert.Equal(t, readTimeout, handler.readTimeout)
	assert.Equal(t, writeTimeout, handler.writeTimeout)
}

// TestFileHandler_OpenAndClose tests the Open and Close methods of the FileHandler to ensure files are correctly managed.
func TestFileHandler_OpenAndClose(t *testing.T) {
	filePath := randFileName()

	// Ensure any existing file with the same name is removed before starting the test.
	os.Remove(filePath)
	channel := make(chan []byte)
	events := make(chan error)

	// Open the handler and verify that the file exists after opening.
	handler := NewFileHandler(Peer, Writer, channel, events, filePath, false, 0, 0).(*FileHandler)
	err := handler.Open(context.Background())
	assert.Nil(t, err)
	assert.FileExists(t, filePath)

	// Close the handler and check if the file still accessible, indicating proper closure.
	err = handler.Close()
	assert.Nil(t, err)

	// Check if file can be re-opened, indicating it was properly closed.
	file, err := os.OpenFile(filePath, os.O_WRONLY, 0666)
	assert.Nil(t, err)
	file.Close()

	// Clean up the created file after the test.
	os.Remove(filePath)
}

func TestFileHandler_OpenAndCancel(t *testing.T) {
	filePath := randFileName()

	// Ensure any existing file with the same name is removed before starting the test.
	os.Remove(filePath)
	channel := make(chan []byte)
	events := make(chan error)

	// Open the handler and verify that the file exists after opening.
	handler := NewFileHandler(Peer, Writer, channel, events, filePath, false, 0, 0).(*FileHandler)
	ctx, cancel := context.WithCancel(context.Background())
	err := handler.Open(ctx)
	assert.Nil(t, err)
	assert.FileExists(t, filePath)

	// Evaluate the file handler status
	status := handler.Status()
	assert.Equal(t, Peer, status.GetMode())
	assert.Equal(t, Writer, status.GetRole())
	assert.Equal(t, filePath, status.GetAddress())

	// Cancel the context to simulate a shutdown and check if the file is removed.
	cancel()
	time.Sleep(100 * time.Millisecond) // Wait for the handler to close.

	// Reevaluate the file handler status
	status = handler.Status()
	assert.Equal(t, Peer, status.GetMode())
	assert.Equal(t, Writer, status.GetRole())
	assert.Equal(t, filePath, status.GetAddress())
}

// TestFileHandler_FIFO checks the functionality of the FileHandler with FIFO specific operations.
func TestFileHandler_FIFO(t *testing.T) {
	filePath := randFileName()
	channel := make(chan []byte)
	events := make(chan error)

	handler := NewFileHandler(Peer, Reader, channel, events, filePath, true, 1, 1).(*FileHandler)

	// Open the handler as a FIFO and ensure the FIFO file exists.
	err := handler.Open(context.Background())
	assert.Nil(t, err)
	_, err = os.Stat(filePath)
	assert.Nil(t, err)

	// Use a goroutine to open the FIFO as a writer to prevent blocking.
	go func() {
		writer, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE, 0666)
		assert.Nil(t, err)
		writer.Close()
	}()

	// Allow some time for the writer to open and complete.
	time.Sleep(100 * time.Millisecond)

	// Clean up the created FIFO after the test.
	handler.Close()
	os.Remove(filePath)
}

// TestFileHandler_DataFlow tests the complete cycle of writing to and reading from the file.
func TestFileHandler_DataFlow(t *testing.T) {
	filePath := randFileName()
	readChannel := make(chan []byte, 1)
	writeChannel := make(chan []byte, 1)
	events := make(chan error)

	// Initialize writer and reader handlers.
	writer := NewFileHandler(Peer, Writer, writeChannel, events, filePath, false, 0, 0).(*FileHandler)
	err := writer.Open(context.Background())
	assert.Nil(t, err)
	defer writer.Close()

	reader := NewFileHandler(Peer, Reader, readChannel, events, filePath, false, 0, 0).(*FileHandler)
	err = reader.Open(context.Background())
	assert.Nil(t, err)
	defer reader.Close()

	// Write data to the file.
	testData := []byte("hello, world")
	writeChannel <- testData

	// Attempt to read the data and check if matches what was written.
	select {
	case <-time.After(10 * time.Millisecond):
		assert.Fail(t, "Timeout waiting for data")
	case data := <-readChannel:
		assert.Equal(t, testData, data)
	}

	// Clean up resources and remove test file.
	os.Remove(filePath)
}

// randFileName generates a random filename for testing, reducing the chance of file conflicts.
func randFileName() string {
	randBytes := make([]byte, 8) // Generates a unique identifier of 16 hex characters.
	_, err := rand.Read(randBytes)
	if err != nil {
		return ""
	}
	return "/tmp/" + hex.EncodeToString(randBytes) + ".txt"
}
