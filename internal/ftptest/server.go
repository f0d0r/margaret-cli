// Package ftptest provides a minimal read-only FTP server used by the test
// suite to exercise the FTP source without a real server on the network.
package ftptest

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Server is a minimal FTP server that serves a directory tree read-only. It
// supports the passive commands (PASV and EPSV) and the LIST, RETR, SIZE and
// MLSD commands the client needs. Only one passive data connection is kept
// per control connection.
type Server struct {
	ln   net.Listener
	root string
}

// NewServer starts a server on 127.0.0.1:0 serving the directory root.
func NewServer(root string) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{ln: ln, root: root}
	go s.serve()
	return s, nil
}

// Addr returns the host:port clients connect to.
func (s *Server) Addr() string {
	return s.ln.Addr().String()
}

// Close stops the server.
func (s *Server) Close() {
	_ = s.ln.Close()
}

func (s *Server) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	sess := &session{c: c, r: bufio.NewReader(c), root: s.root}
	defer func() {
		if sess.data != nil {
			_ = sess.data.Close()
		}
	}()
	_, _ = fmt.Fprintf(c, "220 margaret test FTP ready\r\n")
	for {
		line, err := sess.r.ReadString('\n')
		if err != nil {
			return
		}
		if !sess.reply(strings.TrimSpace(line)) {
			return
		}
	}
}

// session holds the state of one control connection.
type session struct {
	c    net.Conn
	r    *bufio.Reader
	root string
	data net.Listener // passive-mode data listener, if any
}

// reply handles one command and reports whether the connection stays open.
func (s *session) reply(cmd string) bool {
	fields := strings.Fields(cmd)
	verb := strings.ToUpper(fields[0])
	arg := ""
	if len(fields) > 1 {
		arg = strings.Join(fields[1:], " ")
	}
	switch verb {
	case "USER":
		_, _ = fmt.Fprintf(s.c, "331 password required\r\n")
	case "PASS":
		_, _ = fmt.Fprintf(s.c, "230 logged in\r\n")
	case "FEAT":
		_, _ = fmt.Fprintf(s.c, "500 not understood\r\n")
	case "SYST":
		_, _ = fmt.Fprintf(s.c, "215 UNIX Type: L8\r\n")
	case "TYPE":
		_, _ = fmt.Fprintf(s.c, "200 type set\r\n")
	case "OPTS":
		_, _ = fmt.Fprintf(s.c, "200 ok\r\n")
	case "PWD":
		_, _ = fmt.Fprintf(s.c, "257 \"/\"\r\n")
	case "PASV":
		return s.pasv(false)
	case "EPSV":
		return s.pasv(true)
	case "LIST", "MLSD":
		return s.list(arg)
	case "RETR":
		return s.retr(arg)
	case "SIZE":
		return s.size(arg)
	case "CWD":
		_, _ = fmt.Fprintf(s.c, "250 ok\r\n")
	case "QUIT":
		_, _ = fmt.Fprintf(s.c, "221 goodbye\r\n")
		return false
	default:
		_, _ = fmt.Fprintf(s.c, "502 not implemented\r\n")
	}
	return true
}

// pasv opens a passive data listener and announces it. For EPSV the host is
// elided; for PASV the full 227 line with the dotted IP is sent.
func (s *session) pasv(epsv bool) bool {
	if s.data != nil {
		_ = s.data.Close()
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_, _ = fmt.Fprintf(s.c, "425 can't open data connection\r\n")
		return true
	}
	s.data = ln
	tcp := ln.Addr().(*net.TCPAddr)
	if epsv {
		_, _ = fmt.Fprintf(s.c, "229 Entering Extended Passive Mode (|||%d|)\r\n", tcp.Port)
		return true
	}
	ip := tcp.IP.To4()
	_, _ = fmt.Fprintf(s.c, "227 Entering Passive Mode (%d,%d,%d,%d,%d,%d)\r\n",
		ip[0], ip[1], ip[2], ip[3], tcp.Port>>8, tcp.Port&0xff)
	return true
}

// resolve maps an FTP path argument to a local path under the served root.
func (s *session) resolve(arg string) string {
	if arg == "" {
		return s.root
	}
	return filepath.Join(s.root, filepath.FromSlash(strings.TrimPrefix(arg, "/")))
}

// acceptData waits for the client to connect to the passive listener and
// returns the data connection.
func (s *session) acceptData() (net.Conn, bool) {
	if s.data == nil {
		_, _ = fmt.Fprintf(s.c, "425 use PASV first\r\n")
		return nil, false
	}
	ln := s.data
	s.data = nil
	_ = ln.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second))
	dc, err := ln.Accept()
	_ = ln.Close()
	if err != nil {
		_, _ = fmt.Fprintf(s.c, "425 can't open data connection\r\n")
		return nil, false
	}
	return dc, true
}

func (s *session) list(arg string) bool {
	path := s.resolve(arg)
	entries, err := os.ReadDir(path)
	if err != nil {
		fi, err := os.Stat(path)
		if err != nil {
			_, _ = fmt.Fprintf(s.c, "550 no such file or directory\r\n")
			return true
		}
		return s.sendList([]os.FileInfo{fi})
	}
	infos := make([]os.FileInfo, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			_, _ = fmt.Fprintf(s.c, "550 %v\r\n", err)
			return true
		}
		infos = append(infos, info)
	}
	return s.sendList(infos)
}

func (s *session) sendList(infos []os.FileInfo) bool {
	dc, ok := s.acceptData()
	if !ok {
		return true
	}
	_, _ = fmt.Fprintf(s.c, "150 opening data connection\r\n")
	for _, info := range infos {
		s.writeListLine(dc, info)
	}
	_ = dc.Close()
	_, _ = fmt.Fprintf(s.c, "226 transfer complete\r\n")
	return true
}

// writeListLine writes a UNIX ls -l style line that the jlaffaye/ftp parser
// understands.
func (s *session) writeListLine(dc net.Conn, info os.FileInfo) {
	mode := "-rw-r--r--"
	if info.IsDir() {
		mode = "drwxr-xr-x"
	}
	_, _ = fmt.Fprintf(dc, "%s   1 owner    group        %12d %s %s\r\n",
		mode, info.Size(), info.ModTime().Format("Jan 2 15:04"), info.Name())
}

func (s *session) retr(arg string) bool {
	f, err := os.Open(s.resolve(arg))
	if err != nil {
		_, _ = fmt.Fprintf(s.c, "550 no such file or directory\r\n")
		return true
	}
	defer func() {
		_ = f.Close()
	}()
	dc, ok := s.acceptData()
	if !ok {
		return true
	}
	_, _ = fmt.Fprintf(s.c, "150 opening data connection\r\n")
	_, _ = io.Copy(dc, f)
	_ = dc.Close()
	_, _ = fmt.Fprintf(s.c, "226 transfer complete\r\n")
	return true
}

func (s *session) size(arg string) bool {
	fi, err := os.Stat(s.resolve(arg))
	if err != nil {
		_, _ = fmt.Fprintf(s.c, "550 no such file or directory\r\n")
		return true
	}
	_, _ = fmt.Fprintf(s.c, "213 %d\r\n", fi.Size())
	return true
}
