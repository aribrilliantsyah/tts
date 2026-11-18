package web

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"sync"
	"time"
)

// GenerateSelfSignedCert creates an in-memory self-signed TLS certificate valid for the given IPs and hostnames.
func GenerateSelfSignedCert(ips []net.IP, hostnames []string) (*tls.Config, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	allIPs := []net.IP{net.ParseIP("127.0.0.1"), net.IPv6loopback}
	for _, ip := range ips {
		if ip != nil && !ip.IsLoopback() {
			allIPs = append(allIPs, ip)
		}
	}

	allHosts := []string{"localhost"}
	for _, h := range hostnames {
		if h != "" && h != "localhost" {
			allHosts = append(allHosts, h)
		}
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"TTS Kuis"},
			CommonName:   "TTS Kuis",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           allIPs,
		DNSNames:              allHosts,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}

	cert := tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  priv,
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

type sniffConn struct {
	net.Conn
	r io.Reader
}

func (c *sniffConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

type channelListener struct {
	addr   net.Addr
	ch     chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newChannelListener(addr net.Addr) *channelListener {
	return &channelListener{
		addr:   addr,
		ch:     make(chan net.Conn, 128),
		closed: make(chan struct{}),
	}
}

func (l *channelListener) Accept() (net.Conn, error) {
	select {
	case c, ok := <-l.ch:
		if !ok {
			return nil, net.ErrClosed
		}
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *channelListener) Close() error {
	l.once.Do(func() {
		close(l.closed)
	})
	return nil
}

func (l *channelListener) Addr() net.Addr {
	return l.addr
}

// DualServer multiplexes HTTP and HTTPS connections on the same port/listener.
type DualServer struct {
	ln         net.Listener
	handler    http.Handler
	tlsConfig  *tls.Config
	httpServer *http.Server
	tlsServer  *http.Server
	httpLn     *channelListener
	tlsLn      *channelListener
	closed     chan struct{}
	once       sync.Once
}

// NewDualServer wraps an existing net.Listener to accept both HTTP and HTTPS on the same port.
func NewDualServer(ln net.Listener, handler http.Handler, tlsConfig *tls.Config) *DualServer {
	httpLn := newChannelListener(ln.Addr())
	tlsLn := newChannelListener(ln.Addr())

	return &DualServer{
		ln:        ln,
		handler:   handler,
		tlsConfig: tlsConfig,
		httpServer: &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
		},
		tlsServer: &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			TLSConfig:         tlsConfig,
		},
		httpLn: httpLn,
		tlsLn:  tlsLn,
		closed: make(chan struct{}),
	}
}

// Serve starts the connection dispatcher and the underlying HTTP & HTTPS servers.
func (d *DualServer) Serve() error {
	go func() {
		_ = d.httpServer.Serve(d.httpLn)
	}()

	tlsListener := tls.NewListener(d.tlsLn, d.tlsConfig)
	go func() {
		_ = d.tlsServer.Serve(tlsListener)
	}()

	for {
		conn, err := d.ln.Accept()
		if err != nil {
			select {
			case <-d.closed:
				return nil
			default:
				return err
			}
		}

		go func(c net.Conn) {
			_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
			buf := make([]byte, 1)
			_, err := io.ReadFull(c, buf)
			_ = c.SetReadDeadline(time.Time{})
			if err != nil {
				c.Close()
				return
			}

			wrapped := &sniffConn{
				Conn: c,
				r:    io.MultiReader(bytes.NewReader(buf), c),
			}

			if buf[0] == 0x16 { // TLS Record: Handshake (ClientHello)
				select {
				case d.tlsLn.ch <- wrapped:
				case <-d.closed:
					wrapped.Close()
				}
			} else {
				select {
				case d.httpLn.ch <- wrapped:
				case <-d.closed:
					wrapped.Close()
				}
			}
		}(conn)
	}
}

// Shutdown gracefully shuts down both HTTP and HTTPS servers.
func (d *DualServer) Shutdown(ctx context.Context) error {
	d.Close()
	err1 := d.httpServer.Shutdown(ctx)
	err2 := d.tlsServer.Shutdown(ctx)
	if err1 != nil && !errors.Is(err1, http.ErrServerClosed) {
		return err1
	}
	if err2 != nil && !errors.Is(err2, http.ErrServerClosed) {
		return err2
	}
	return nil
}

// Close closes the underlying listener and channel listeners.
func (d *DualServer) Close() error {
	d.once.Do(func() {
		close(d.closed)
		_ = d.ln.Close()
		_ = d.httpLn.Close()
		_ = d.tlsLn.Close()
	})
	return nil
}
