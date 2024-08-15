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

// Getter methods for FileStatus
func (f FileStatus) GetMode() Mode      { return f.Mode }
func (f FileStatus) GetRole() Role      { return f.Role }
func (f FileStatus) GetAddress() string { return f.FilePath }

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
			FilePath:     filePath,
			IsFIFO:       isFIFO,
			Mode:         mode,
			Role:         role,
			ReadTimeout:  readTimeout,
			WriteTimeout: writeTimeout,
			IsOpen:       false,
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
	h.status.IsOpen = h.isOpen
	return h.status
}

// Open initializes the file handler by opening or creating the file and starting the appropriate data processing goroutines.
func (h *FileHandler) Open() error {
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
		h.file, err = os.OpenFile(h.filePath, os.O_APPEND|os.O_CREATE, 0666)
		if err != nil {
			return err
		}
	}

	h.isOpen = true

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
		h.isOpen = false
		if h.isFIFO {
			err := syscall.Unlink(h.filePath)
			if err != nil {
				return err
			}
		}
		close(h.dataChannel)
		return err
	}
	return nil
}

// fileBufferPool is a pool of byte slices used to reduce garbage collection overhead.
var fileBufferPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 4096)
		return &b
	},
}

// readData handles the data reading operations from the file based on configured timeouts.
func (h *FileHandler) readData() {
	defer h.file.Close()

	for {
		buffer := fileBufferPool.Get().(*[]byte)
		*buffer = (*buffer)[:cap(*buffer)] // Ensure the buffer is fully utilized

		if h.readTimeout > 0 {
			select {
			case <-time.After(h.readTimeout):
				fileBufferPool.Put(buffer)
				h.SendError(errors.New("file read operation timed out"))
				return
			default:
				n, err := h.file.Read(*buffer)
				if err != nil {
					if err == io.EOF || err == syscall.EINTR {
						fileBufferPool.Put(buffer)
						continue
					}
					fileBufferPool.Put(buffer)
					h.SendError(err)
					return
				}
				if n > 0 {
					h.dataChannel <- (*buffer)[:n]
				}
				fileBufferPool.Put(buffer)
			}
		} else {
			n, err := h.file.Read(*buffer)
			if err != nil {
				if err == io.EOF || err == syscall.EINTR {
					fileBufferPool.Put(buffer)
					continue
				}
				fileBufferPool.Put(buffer)
				h.SendError(err)
				return
			}
			if n > 0 {
				h.dataChannel <- (*buffer)[:n]
			}
			fileBufferPool.Put(buffer)
		}
	}
}

// writeData handles the data writing operations to the file based on configured timeouts.
func (h *FileHandler) writeData() {
	defer h.file.Close()

	for data := range h.dataChannel {
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
			case <-time.After(h.writeTimeout):
				h.SendError(errors.New("file write operation timed out"))
				return
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
