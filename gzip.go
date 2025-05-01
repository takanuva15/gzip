package gzip

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"net"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

const (
	BestCompression    = gzip.BestCompression
	BestSpeed          = gzip.BestSpeed
	DefaultCompression = gzip.DefaultCompression
	NoCompression      = gzip.NoCompression
	HuffmanOnly        = gzip.HuffmanOnly
)

func Gzip(level int, options ...Option) gin.HandlerFunc {
	return newGzipHandler(level, options...).Handle
}

type gzipWriter struct {
	gin.ResponseWriter
	writer        *gzip.Writer
	statusWritten bool
	status        int
	// minLength is the minimum length of the response body (in bytes) to enable compression
	minLength int
	// shouldCompress indicates whether the minimum length for compression has been met
	shouldCompress bool
	// buffer to store response data in case compression limit not met
	buffer bytes.Buffer
}

func (g *gzipWriter) WriteString(s string) (int, error) {
	return g.Write([]byte(s))
}

// Write writes the given data to the appropriate underlying writer.
// Note that this method can be called multiple times within a single request.
func (g *gzipWriter) Write(data []byte) (int, error) {
	// Check status from ResponseWriter if not set via WriteHeader
	if !g.statusWritten {
		g.status = g.ResponseWriter.Status()
	}

	// For error responses (4xx, 5xx), don't compress
	if g.status >= 400 {
		g.removeGzipHeaders()
		return g.ResponseWriter.Write(data)
	}

	// If a Content-Length header is set, use that to decide whether to compress the response.
	if g.Header().Get("Content-Length") != "" {
		// invalid header treated the same as having no Content-Length
		contentLen, err := strconv.Atoi(g.Header().Get("Content-Length"))
		if err == nil {
			if contentLen < g.minLength {
				return g.ResponseWriter.Write(data)
			}
			g.shouldCompress = true
			g.Header().Del("Content-Length")
		}
	}

	// Check if the response body is large enough to be compressed. If so, skip this condition and proceed with the
	// normal write process. If not, store the data in the buffer in case more data is written later.
	// (At the end, if the response body is still too small, the caller should check shouldCompress and
	// use the data stored in the buffer to write the response instead.)
	if !g.shouldCompress && len(data) >= g.minLength {
		g.shouldCompress = true
	} else if !g.shouldCompress {
		lenWritten, err := g.buffer.Write(data)
		if err != nil || g.buffer.Len() < g.minLength {
			return lenWritten, err
		}
		g.shouldCompress = true
		data = g.buffer.Bytes()
	}

	return g.writer.Write(data)
}

// Status returns the HTTP response status code
func (g *gzipWriter) Status() int {
	if g.statusWritten {
		return g.status
	}
	return g.ResponseWriter.Status()
}

// Size returns the number of bytes already written into the response http body
func (g *gzipWriter) Size() int {
	return g.ResponseWriter.Size()
}

// Written returns true if the response body was already written
func (g *gzipWriter) Written() bool {
	return g.ResponseWriter.Written()
}

// WriteHeaderNow forces to write the http header
func (g *gzipWriter) WriteHeaderNow() {
	g.ResponseWriter.WriteHeaderNow()
}

// removeGzipHeaders removes compression-related headers for error responses
func (g *gzipWriter) removeGzipHeaders() {
	g.Header().Del("Content-Encoding")
	g.Header().Del("Vary")
	g.Header().Del("ETag")
}

func (g *gzipWriter) Flush() {
	_ = g.writer.Flush()
	g.ResponseWriter.Flush()
}

// Fix: https://github.com/mholt/caddy/issues/38
func (g *gzipWriter) WriteHeader(code int) {
	g.status = code
	g.statusWritten = true

	// Remove gzip headers for error responses
	if code >= 400 {
		g.removeGzipHeaders()
	}

	g.ResponseWriter.WriteHeader(code)
}

// Ensure gzipWriter implements the http.Hijacker interface.
// This will cause a compile-time error if gzipWriter does not implement all methods of the http.Hijacker interface.
var _ http.Hijacker = (*gzipWriter)(nil)

// Hijack allows the caller to take over the connection from the HTTP server.
// After a call to Hijack, the HTTP server library will not do anything else with the connection.
// It becomes the caller's responsibility to manage and close the connection.
//
// It returns the underlying net.Conn, a buffered reader/writer for the connection, and an error
// if the ResponseWriter does not support the Hijacker interface.
func (g *gzipWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := g.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("the ResponseWriter doesn't support the Hijacker interface")
	}
	return hijacker.Hijack()
}
