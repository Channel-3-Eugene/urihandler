// Package urihandler provides a set of tools for file handling operations that can interact with regular files and FIFOs.
package urihandler

import (
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
)

// FileStatus represents the current status of a FileHandler, including operational configuration and state.
type FileStatus struct {
	FilePath     string
	IsFIFO       bool
	Mode         Mode
	Role         Role
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IsOpen       bool
}

// GetMode returns the operation mode of the file handler.
func (f FileStatus) GetMode() Mode { return f.Mode }

// GetRole returns the operational role of the file handler.
func (f FileStatus) GetRole() Role { return f.Role }

// GetAddress returns the file path associated with the file handler.
func (f FileStatus) GetAddress() string { return f.FilePath }

// FileHandler manages the operations for a file, supporting both regular file operations and FIFO-based interactions.
type FileHandler struct {
	filePath     string
	file         *os.File
	channel      chan []byte
	events       chan error
	mode         Mode
	role         Role
	isFIFO       bool
	readTimeout  time.Duration
	writeTimeout time.Duration
	isOpen       bool         // Tracks the open or closed state of the file.
	mu           sync.RWMutex // Use RWMutex to allow concurrent reads
}

// NewFileHandler creates a new FileHandler with specified configurations.
func NewFileHandler(mode Mode, role Role, ch chan []byte, events chan error, filePath string, isFIFO bool, readTimeout, writeTimeout time.Duration) *FileHandler {
	return &FileHandler{
		mode:         mode,
		role:         role,
		channel:      ch,
		events:       events,
		filePath:     filePath,
		isFIFO:       isFIFO,
		readTimeout:  readTimeout,
		writeTimeout: writeTimeout,
	}
}

// Status provides the current status of the FileHandler.
func (h *FileHandler) Status() FileStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return FileStatus{
		FilePath:     h.filePath,
		IsFIFO:       h.isFIFO,
		Mode:         h.mode,
		Role:         h.role,
		ReadTimeout:  h.readTimeout,
		WriteTimeout: h.writeTimeout,
		IsOpen:       h.isOpen,
	}
}

// Open initializes the file handler by opening or creating the file and starting the appropriate data processing goroutines.
func (h *FileHandler) Open() error {
	var err error
	// Check if the file exists; if not, create it or initialize a FIFO.
	if _, err = os.Stat(h.filePath); os.IsNotExist(err) {
		if h.isFIFO {
			if err = syscall.Mkfifo(h.filePath, 0666); err != nil {
				return err
			}
		} else {
			h.mu.Lock()
			h.file, err = os.Create(h.filePath)
			h.mu.Unlock()
			if err != nil {
				return err
			}
		}
	} else {
		h.mu.Lock()
		h.file, err = os.OpenFile(h.filePath, os.O_APPEND|os.O_CREATE, 0666)
		h.mu.Unlock()
		if err != nil {
			return err
		}
	}

	h.mu.Lock()
	h.isOpen = true // Mark the file as open.
	h.mu.Unlock()

	if h.role == Reader {
		go h.readData()
	} else {
		go h.writeData()
	}
	return nil
}

// Close terminates the file handler's operations and closes the file.
func (h *FileHandler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.file != nil {
		err := h.file.Close()
		h.isOpen = false // Update the state to closed.
		if h.isFIFO {
			syscall.Unlink(h.filePath) // Remove the FIFO file.
		}
		h.channel = nil
		return err
	}
	return nil
}

var bufferPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 4096)
		return &b
	},
}

// readData handles the data reading operations from the file based on configured timeouts.
func (h *FileHandler) readData() {
	var err error
	h.mu.Lock()
	if h.file == nil {
		h.file, err = os.Open(h.filePath)
	}
	h.mu.Unlock()
	if err != nil {
		h.SendError(err)
		return
	}
	defer h.file.Close()

	for {
		buffer := bufferPool.Get().(*[]byte)
		*buffer = (*buffer)[:cap(*buffer)] // Ensure the buffer is fully utilized

		if h.readTimeout > 0 {
			select {
			case <-time.After(h.readTimeout):
				bufferPool.Put(buffer)
				h.SendError(errors.New("file read operation timed out"))
				return // Exit the goroutine after a timeout.
			default:
				n, err := h.file.Read(*buffer)
				if err != nil {
					if err == io.EOF || err == syscall.EINTR {
						bufferPool.Put(buffer)
						continue
					}
					bufferPool.Put(buffer)
					h.SendError(err)
					return
				}
				if n > 0 {
					h.channel <- (*buffer)[:n] // Only send the valid portion
				}
				bufferPool.Put(buffer)
			}
		} else {
			n, err := h.file.Read(*buffer)
			if err != nil {
				if err == io.EOF || err == syscall.EINTR {
					bufferPool.Put(buffer)
					continue
				}
				bufferPool.Put(buffer)
				h.SendError(err)
				return
			}
			if n > 0 {
				h.channel <- (*buffer)[:n] // Only send the valid portion
			}
			bufferPool.Put(buffer)
		}
	}
}

// writeData handles the data writing operations to the file based on configured timeouts.
func (h *FileHandler) writeData() {
	var err error
	h.mu.Lock()
	if h.file == nil {
		h.file, err = os.OpenFile(h.filePath, os.O_WRONLY|os.O_CREATE, 0666)
	}
	h.mu.Unlock()
	if err != nil {
		h.SendError(err)
		return
	}

	defer h.file.Close()

	for data := range h.channel {
		if h.writeTimeout > 0 {
			writeDone := make(chan struct{})
			go func() {
				_, err := h.file.Write(data)
				if err != nil {
					h.SendError(err)
				}
				close(writeDone)
			}()

			select {
			case <-writeDone:
				// Write completed
			case <-time.After(h.writeTimeout):
				h.SendError(errors.New("file write operation timed out"))
				return // Exit the goroutine after a timeout.
			}
		} else {
			_, err := h.file.Write(data)
			if err != nil {
				h.SendError(err)
			}
		}
	}
}

func (h *FileHandler) SendError(err error) {
	if h.events != nil {
		h.events <- err
	}
}
