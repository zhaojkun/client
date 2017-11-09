package http

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
)

var (
	ErrPersistEOF = &http.ProtocolError{ErrorString: "persistent connection closed"}

	ErrClosed = &http.ProtocolError{ErrorString: "connection closed by user"}

	ErrPipeline = &http.ProtocolError{ErrorString: "pipeline error"}

	ErrBodyWaitingRead = &http.ProtocolError{ErrorString: "body data waiting for read"}
)

var errClosed = errors.New("i/o operation on closed connection")

type ClientConn struct {
	mu              sync.Mutex // read-write protects the following fields
	c               net.Conn
	r               *bufio.Reader
	bodyReading     bool
	re, we          error // read/write errors
	nread, nwritten int
	reqch           chan *http.Request
	respch          chan *http.Response
	writeReq        func(*http.Request, io.Writer) error
}

func NewClientConn(c net.Conn, r *bufio.Reader) *ClientConn {
	if r == nil {
		r = bufio.NewReader(c)
	}
	cc := &ClientConn{
		c:        c,
		r:        r,
		reqch:    make(chan *http.Request, 1),
		respch:   make(chan *http.Response, 1),
		writeReq: (*http.Request).Write,
	}
	go cc.readLoop()
	return cc
}

func NewProxyClientConn(c net.Conn, r *bufio.Reader) *ClientConn {
	cc := NewClientConn(c, r)
	cc.writeReq = (*http.Request).WriteProxy
	return cc
}

func (cc *ClientConn) Do(req *http.Request) (*http.Response, error) {
	err := cc.write(req)
	if err != nil {
		return nil, err
	}
	return cc.read(req)
}

func (cc *ClientConn) waitForBody() bool {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.bodyReading
}
func (cc *ClientConn) write(req *http.Request) error {
	var err error
	if err = cc.Ping(); err != nil {
		return err
	}
	if cc.waitForBody() {
		return ErrBodyWaitingRead
	}
	cc.mu.Lock()
	c := cc.c
	if req.Close {
		cc.we = ErrPersistEOF
	}
	cc.mu.Unlock()
	err = cc.writeReq(req, c)
	cc.mu.Lock()
	if err != nil {
		cc.we = err
		cc.mu.Unlock()
		return err
	}
	cc.nwritten++
	cc.mu.Unlock()
	cc.reqch <- req
	return nil
}

func (cc *ClientConn) read(req *http.Request) (resp *http.Response, err error) {
	resp = <-cc.respch
	return
}

func (cc *ClientConn) Hijack() (c net.Conn, r *bufio.Reader) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	c = cc.c
	r = cc.r
	cc.c = nil
	cc.r = nil
	return
}

func (cc *ClientConn) Close() error {
	c, _ := cc.Hijack()
	if c != nil {
		return c.Close()
	}
	return nil
}

func (cc *ClientConn) Ping() error {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if cc.re != nil { // no point sending if read-side closed or broken
		return cc.re
	}
	if cc.we != nil {
		return cc.we
	}
	if cc.c == nil { // connection closed by user in the meantime
		return errClosed
	}
	return nil
}

func (cc *ClientConn) readLoop() {
	alive := true
	for alive {
		_, err := cc.r.Peek(1)
		if err != nil {
			cc.re = err
			break
		}
		rc := <-cc.reqch
		resp, err := http.ReadResponse(cc.r, rc)
		if err != nil {
			cc.mu.Lock()
			cc.re = err
			cc.mu.Unlock()
			break
		}
		hasBody := rc.Method != "HEAD" && resp.ContentLength != 0
		if resp.Close || rc.Close || resp.StatusCode <= 199 {
			alive = false
		}
		if !hasBody {
			continue
		}
		waitForBodyRead := make(chan bool, 2)
		body := &bodyEOFSignal{
			body: resp.Body,
			earlyCloseFn: func() error {
				waitForBodyRead <- false
				return nil

			},
			fn: func(err error) error {
				isEOF := err == io.EOF
				waitForBodyRead <- isEOF
				return err
			},
		}
		resp.Body = body
		cc.respch <- resp
		cc.mu.Lock()
		cc.bodyReading = true
		cc.mu.Unlock()
		select {
		case bodyEOF := <-waitForBodyRead:
			_ = bodyEOF
		}
		cc.mu.Lock()
		cc.bodyReading = false
		cc.mu.Unlock()
	}
}
