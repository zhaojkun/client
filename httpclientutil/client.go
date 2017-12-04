package httpclientutil

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
)

var (
	ErrPersistEOF       = &http.ProtocolError{ErrorString: "persistent connection closed"}
	ErrClosed           = &http.ProtocolError{ErrorString: "connection closed by user"}
	ErrPipeline         = &http.ProtocolError{ErrorString: "pipeline error"}
	ErrBodyWaitingRead  = &http.ProtocolError{ErrorString: "body data waiting for read"}
	ErrBodyLeftData     = errors.New("http: some data left in the buffer")
	ErrServerClosedConn = errors.New("http: server closed connection")
)

var errClosed = errors.New("i/o operation on closed connection")

type ClientConn struct {
	mu          sync.Mutex // read-write protects the following fields
	conn        net.Conn
	r           *bufio.Reader
	bodyReading bool
	stoped      bool
	re, we      error // read/write errors
	reqch       chan *http.Request
	respch      chan *http.Response
	closech     chan struct{}
	writeReq    func(*http.Request, io.Writer) error
}

func NewClientConn(c net.Conn, r *bufio.Reader) *ClientConn {
	if r == nil {
		r = bufio.NewReader(c)
	}
	cc := &ClientConn{
		conn:     c,
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
func (cc *ClientConn) iswaiting() bool {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.bodyReading
}

func (cc *ClientConn) write(req *http.Request) error {
	var err error
	if err = cc.Ping(); err != nil {
		return err
	}
	if cc.iswaiting() {
		return ErrBodyWaitingRead
	}
	cc.mu.Lock()
	c := cc.conn
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
	cc.mu.Unlock()
	cc.reqch <- req
	return nil
}

func (cc *ClientConn) read(req *http.Request) (resp *http.Response, err error) {
	ctx := req.Context()
	select {
	case resp = <-cc.respch:
	case <-ctx.Done():
		err = ctx.Err()
		cc.setReadError(err)
	}
	return
}

func (cc *ClientConn) Hijack() (c net.Conn, r *bufio.Reader) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	c = cc.conn
	r = cc.r
	cc.conn = nil
	cc.r = nil
	return
}

func (cc *ClientConn) Close() error {
	c, _ := cc.Hijack()
	if c != nil {
		return c.Close()
	}
	close(cc.closech)
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
	if cc.conn == nil { // connection closed by user in the meantime
		return errClosed
	}
	if cc.stoped {
		return errClosed
	}
	return nil
}

func (cc *ClientConn) setReadError(err error) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.re = err
}

func (cc *ClientConn) readLoop() {
	alive := true
	for alive {
		r := cc.getReader()
		if r == nil {
			alive = false
			break
		}
		_, err := r.Peek(1)
		if err != nil {
			cc.setReadError(ErrServerClosedConn)
			break
		}
		rc := <-cc.reqch
		resp, err := http.ReadResponse(r, rc)
		if err != nil {
			cc.setReadError(err)
			break
		}
		hasBody := rc.Method != "HEAD" && resp.ContentLength != 0
		if resp.Close || rc.Close || resp.StatusCode <= 199 {
			alive = false
			cc.setReadError(ErrServerClosedConn)
		}
		if !hasBody {
			continue
		}
		waitForBodyRead := make(chan bool, 2)
		resp.Body = newBodyEOFSingle(resp.Body, waitForBodyRead, func(err error) {
			cc.mu.Lock()
			defer cc.mu.Unlock()
			cc.bodyReading = false
			if err != nil && err != io.EOF {
				cc.re = ErrBodyLeftData
			}
		})
		cc.respch <- resp
		cc.setBodyReading(true)
		select {
		case bodyEOF := <-waitForBodyRead:
			alive = alive && bodyEOF
		case <-rc.Cancel:
			alive = false
		case <-rc.Context().Done():
			alive = false
		case <-cc.closech:
			alive = false
		}
		cc.setBodyReading(false)
	}
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.stoped = true
}

func (cc *ClientConn) getReader() *bufio.Reader {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.r
}
func (cc *ClientConn) setBodyReading(flag bool) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.bodyReading = flag
}
