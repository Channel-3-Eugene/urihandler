// Package urihandler provides a set of tools for file handling operations that can interact with regular files and FIFOs.
package urihandler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
)

// FileStatus represents the current status of a FileHandler, including operational configuration and state.
type FileStatus struct {
	filePath     string
	isFIFO       bool
	mode         Mode
	role         Role
	readTimeout  time.Duration
	writeTimeout time.Duration
	isOpen       bool
}

// Getter methods for FileStatus
func (f FileStatus) GetMode() Mode      { return f.mode }
func (f FileStatus) GetRole() Role      { return f.role }
func (f FileStatus) GetAddress() string { return f.filePath }
func (f FileStatus) IsOpen() bool       { return f.isOpen }

// FileHandler manages the operations for a file, supporting both regular file operations and FIFO-based interactions.
type FileHandler struct {
	filePath     string
	file         *os.File
	dataChannel  chan []byte
	events       chan error
	mode         Mode
	role         Role
	isFIFO       bool
	readTimeout  time.Duration
	writeTimeout time.Duration
	isOpen       bool
	mu           sync.RWMutex // Use RWMutex to allow concurrent reads
	status       FileStatus
}

// NewFileHandler creates a new FileHandler with specified configurations.
func NewFileHandler(
	mode Mode,
	role Role,
	dataChannel chan []byte,
	events chan error,
	filePath string,
	isFIFO bool,
	readTimeout,
	writeTimeout time.Duration,
) URIHandler {
	handler := &FileHandler{
		mode:         mode,
		role:         role,
		dataChannel:  dataChannel,
		events:       events,
		filePath:     filePath,
		isFIFO:       isFIFO,
		readTimeout:  readTimeout,
		writeTimeout: writeTimeout,
		status: FileStatus{
			filePath:     filePath,
			isFIFO:       isFIFO,
			mode:         mode,
			role:         role,
			readTimeout:  readTimeout,
			writeTimeout: writeTimeout,
			isOpen:       false,
		},
	}
	return handler
}

func (h *FileHandler) GetDataChannel() chan []byte {
	return h.dataChannel
}

func (h *FileHandler) SetDataChannel(dataChannel chan []byte) {
	h.dataChannel = dataChannel
}

func (h *FileHandler) GetEventsChannel() chan error {
	return h.events
}

// Status provides the current status of the FileHandler.
func (h *FileHandler) Status() Status {
	h.mu.RLock()
	defer h.mu.RUnlock()
	h.status.mode = h.mode
	h.status.role = h.role
	h.status.filePath = h.filePath
	return h.status
}

// Open initializes the file handler by opening or creating the file and starting the appropriate data processing goroutines.
func (h *FileHandler) Open(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	var err error
	if _, err = os.Stat(h.filePath); os.IsNotExist(err) {
		if h.isFIFO {
			if err = syscall.Mkfifo(h.filePath, 0666); err != nil {
				return err
			}
		} else {
			h.file, err = os.Create(h.filePath)
			if err != nil {
				return err
			}
		}
	} else {
		h.file, err = os.OpenFile(h.filePath, os.O_RDWR, 0666)
		if err != nil {
			return err
		}
	}

	h.isOpen = true

	if h.role == Reader {
		go h.readData(ctx)
	} else {
		go h.writeData(ctx)
	}
	return nil
}

// readData handles the data reading operations from the file based on configured timeouts.
func (h *FileHandler) readData(ctx context.Context) {
	for {
		buffer := make([]byte, 4096) // Allocate a new buffer for each read operation

		select {
		case <-ctx.Done():
			h.Close()
			return
		default:
			if h.readTimeout > 0 {
				select {
				case <-time.After(h.readTimeout):

					h.SendError(errors.New("file read operation timed out"))
					return
				default:
				}
			}

			n, err := h.file.Read(buffer)
			if err != nil {
				if err == io.EOF {
					// EOF is not an error, just means we're done
					fmt.Println("EOF")
					h.Close()
					return
				}
				h.SendError(err)
				return
			}
			if n > 0 {
				h.dataChannel <- buffer[:n]
			}

		}
	}
}

func (h *FileHandler) writeData(ctx context.Context) {
	for {
		select {
		case data, ok := <-h.dataChannel:
			if !ok {
				// Channel is closed, perform cleanup
				h.Close()
				return
			}

			// Process the data
			if h.writeTimeout > 0 {
				select {
				case <-time.After(h.writeTimeout):
					h.SendError(errors.New("file write operation timed out"))
					return
				default:
				}
			}

			_, err := h.file.Write(data)
			if err != nil {
				h.SendError(err)
			}

		case <-ctx.Done():
			// Context has been cancelled, perform cleanup
			h.Close()
			return
		}
	}
}

func (h *FileHandler) SendError(err error) {
	if h.events != nil {
		h.events <- err
	}
}

// Close terminates the file handler's operations and closes the file.
func (h *FileHandler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.file != nil {
		err := h.file.Close()
		if err != nil {
			return err
		}
		h.isOpen = false

		if h.isFIFO {
			err := syscall.Unlink(h.filePath)
			if err != nil {
				return err
			}
		}
		close(h.dataChannel)
	}

	h.isOpen = false

	return nil
}
