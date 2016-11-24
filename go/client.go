package loqui

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	// ErrBadHandshake is returned when the server response to opening handshake is invalid.
	ErrBadHandshake = errors.New("loqui: bad handshake")
)

func hostPortNoPort(u *url.URL) (hostPort, hostNoPort string) {
	hostPort = u.Host
	hostNoPort = u.Host
	if i := strings.LastIndex(u.Host, ":"); i > strings.LastIndex(u.Host, "]") {
		hostNoPort = hostNoPort[:i]
	} else {
		switch u.Scheme {
		case "wss":
			hostPort += ":443"
		case "https":
			hostPort += ":443"
		default:
			hostPort += ":80"
		}
	}
	return hostPort, hostNoPort
}

type Dialer struct {
	// HandshakeTimeout specifies the duration for the handshake to complete.
	SupportedEncodings []string
	HandshakeTimeout   time.Duration
}

func (d *Dialer) Dial(urlStr string) (*Conn, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, err
	}

	req := &http.Request{
		Method:     "GET",
		URL:        u,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Host:       u.Host,
	}

	req.Header["Upgrade"] = []string{"loqui"}
	req.Header["Connection"] = []string{"Upgrade"}

	hostPort, hostNoPort := hostPortNoPort(u)

	targetHostPort := hostPort

	var deadline time.Time
	if d.HandshakeTimeout != 0 {
		deadline = time.Now().Add(d.HandshakeTimeout)
	}

	netDialer := &net.Dialer{Deadline: deadline}
	netDial := netDialer.Dial

	netConn, err := netDial("tcp", targetHostPort)
	if err != nil {
		return nil, err
	}

	defer func() {
		if netConn != nil {
			netConn.Close()
		}
	}()

	if err := netConn.SetDeadline(deadline); err != nil {
		return nil, err
	}

	if u.Scheme == "https" {
		cfg := tls.Config{
			ServerName: hostNoPort,
		}
		tlsConn := tls.Client(netConn, &cfg)
		netConn = tlsConn
		if err := tlsConn.Handshake(); err != nil {
			return nil, err
		}
		if !cfg.InsecureSkipVerify {
			if err := tlsConn.VerifyHostname(cfg.ServerName); err != nil {
				return nil, err
			}
		}
	}

	br := bufio.NewReader(netConn)

	if err := req.Write(netConn); err != nil {
		return nil, err
	}

	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 101 ||
		!strings.EqualFold(resp.Header.Get("Upgrade"), "loqui") ||
		!strings.EqualFold(resp.Header.Get("Connection"), "upgrade") {
		resp.Body.Close()
		return nil, ErrBadHandshake
	}

	resp.Body = ioutil.NopCloser(bytes.NewReader([]byte{}))

	netConn.SetDeadline(time.Time{})

	conn := NewConn(br, netConn, netConn, true)
	conn.supportedEncodings = d.SupportedEncodings

	netConn = nil // to avoid close in defer.

	return conn, nil
}