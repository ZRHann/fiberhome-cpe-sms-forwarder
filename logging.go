package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

const maxLogFileSize = 1 << 20

type boundedLogWriter struct {
	mutex sync.Mutex
	path  string
	file  *os.File
	size  int64
}

func configureLogging(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return fmt.Errorf("protect log file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return fmt.Errorf("inspect log file: %w", err)
	}
	log.SetOutput(&boundedLogWriter{path: path, file: file, size: info.Size()})
	return nil
}

func (writer *boundedLogWriter) Write(data []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if writer.file == nil {
		return 0, fmt.Errorf("log file is closed")
	}
	originalSize := len(data)
	if len(data) > maxLogFileSize {
		data = data[len(data)-maxLogFileSize:]
	}
	if writer.size+int64(len(data)) > maxLogFileSize {
		if err := writer.rotate(); err != nil {
			return 0, err
		}
	}
	written, err := writer.file.Write(data)
	writer.size += int64(written)
	if err == nil && written == len(data) {
		return originalSize, nil
	}
	return written, err
}

func (writer *boundedLogWriter) rotate() error {
	if err := writer.file.Close(); err != nil {
		return fmt.Errorf("close log for rotation: %w", err)
	}
	writer.file = nil
	if err := os.Rename(writer.path, writer.path+".1"); err != nil {
		return fmt.Errorf("rotate log: %w", err)
	}
	file, err := os.OpenFile(writer.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("reopen log: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return fmt.Errorf("protect rotated log: %w", err)
	}
	writer.file = file
	writer.size = 0
	return nil
}
