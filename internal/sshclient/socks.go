package sshclient

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// socksDial connects to addr through a SOCKS5 proxy at proxyAddr.
// proxyAddr is "host:port". No authentication is supported (the common
// case for local SOCKS proxies like Shadowsocks/V2Ray).
func socksDial(ctx context.Context, proxyAddr, targetAddr string) (net.Conn, error) {
	d := net.Dialer{Timeout: 15 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("socks5 dial proxy %s: %w", proxyAddr, err)
	}

	finish := guardHandshake(ctx, conn, 15*time.Second)
	defer finish()
	// Greeting: version 5, no auth.
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 greeting: %w", err)
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 greeting response: %w", err)
	}
	if resp[0] != 0x05 {
		conn.Close()
		return nil, fmt.Errorf("socks5: bad version %d", resp[0])
	}
	if resp[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5: auth method %d not supported", resp[1])
	}

	// Connect request: version 5, connect, reserved, address type + addr + port.
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 bad target %s: %w", targetAddr, err)
	}
	port, _ := strconv.Atoi(portStr)

	req := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req = append(req, 0x01) // IPv4
			req = append(req, ip4...)
		} else {
			req = append(req, 0x04) // IPv6
			req = append(req, ip.To16()...)
		}
	} else {
		req = append(req, 0x03) // domain
		req = append(req, byte(len(host)))
		req = append(req, host...)
	}
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port))
	req = append(req, portBytes...)

	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect: %w", err)
	}

	// Read connect response: 4 bytes header + variable address + 2 port.
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect response: %w", err)
	}
	if header[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5: connect failed, code %d", header[1])
	}

	// Skip bound address based on address type.
	switch header[3] {
	case 0x01: // IPv4
		if _, err := io.ReadFull(conn, make([]byte, 4+2)); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 read bound addr: %w", err)
		}
	case 0x03: // domain
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 read domain len: %w", err)
		}
		if _, err := io.ReadFull(conn, make([]byte, int(lenBuf[0])+2)); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 read bound addr: %w", err)
		}
	case 0x04: // IPv6
		if _, err := io.ReadFull(conn, make([]byte, 16+2)); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 read bound addr: %w", err)
		}
	default:
		conn.Close()
		return nil, fmt.Errorf("socks5: unknown addr type %d", header[3])
	}

	return conn, nil
}
