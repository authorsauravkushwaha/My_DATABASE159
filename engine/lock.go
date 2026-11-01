//go:build !windows

package engine

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

var errStoreLocked = errors.New("store directory is already locked by another process")

type dirLock struct {
	mu   sync.Mutex
	file *os.File
}

func acquireDirLock(dir string) (*dirLock, error) {
	f, err := os.OpenFile(filepath.Join(dir, "LOCK"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errStoreLocked
		}
		return nil, err
	}
	return &dirLock{file: f}, nil
}

func (l *dirLock) release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	err := l.file.Close()
	l.file = nil
	return err
}
