// Copyright (c) TapperVPN contributors
// SPDX-License-Identifier: BSD-3-Clause

package libtappervpn

import (
	"io"
	"log"
	"os"
	"sync"
)

// maxLog is the size at which the log is started over. The app's Logs page
// shows its end.
const maxLog = 2 << 20

// openLog sends the standard logger to path (and stderr, which iOS shows in
// Console.app), starting the file over once it gets big.
func openLog(path string) *os.File {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Printf("log file: %v", err)
		return nil
	}
	if fi, err := f.Stat(); err == nil && fi.Size() > maxLog {
		f.Truncate(0)
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetOutput(io.MultiWriter(os.Stderr, &cappedFile{f: f}))
	return f
}

type cappedFile struct {
	mu sync.Mutex
	f  *os.File
	n  int64
}

func (c *cappedFile) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == 0 {
		if fi, err := c.f.Stat(); err == nil {
			c.n = fi.Size()
		}
	}
	if c.n+int64(len(p)) > maxLog {
		c.f.Truncate(0)
		c.n = 0
	}
	n, err := c.f.Write(p)
	c.n += int64(n)
	return n, err
}
