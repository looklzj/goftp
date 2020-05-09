package goftp

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The differents types of an Entry
const (
	EntryTypeFile EntryType = iota
	EntryTypeFolder
	EntryTypeLink
)

var dirTimeFormats = []string{
	"01-02-06  03:04PM",
	"2006-01-02  15:04",
}

// RePwdPath is the default expression for matching files in the current working directory
var RePwdPath = regexp.MustCompile(`\"(.*)\"`)
var errUnsupportedListLine = errors.New("unsupported LIST line")
var errUnknownListEntryType = errors.New("unknown entry type")
var errUnsupportedListDate = errors.New("unsupported LIST date")

type Response struct {
	conn   net.Conn
	closed bool
}

// FTP is a session for File Transfer Protocol
type FTP struct {
	conn net.Conn

	addr string

	debug     bool
	tlsconfig *tls.Config

	reader *bufio.Reader
	writer *bufio.Writer
}

// Close ends the FTP connection
func (ftp *FTP) Close() error {
	return ftp.conn.Close()
}

type (
	// WalkFunc is called on each path in a Walk. Errors are filtered through WalkFunc
	WalkFunc func(path string, info os.FileMode, err error) error

	// RetrFunc is passed to Retr and is the handler for the stream received for a given path
	RetrFunc func(r io.Reader) error
)

type EntryType int
type Entry struct {
	Name   string
	Target string // target of symbolic link
	Type   EntryType
	Size   uint64
	Time   time.Time
}

type parseFunc func(string, time.Time, *time.Location) (*Entry, error)

func parseLine(line string) (perm string, t string, filename string) {
	for _, v := range strings.Split(line, ";") {
		v2 := strings.Split(v, "=")

		switch v2[0] {
		case "perm":
			perm = v2[1]
		case "type":
			t = v2[1]
		default:
			filename = v[1 : len(v)-2]
		}
	}
	return
}

// Walk walks recursively through path and call walkfunc for each file
func (ftp *FTP) Walk(path string, walkFn WalkFunc) (err error) {
	/*
		if err = walkFn(path, os.ModeDir, nil); err != nil {
			if err == filepath.SkipDir {
				return nil
			}
		}
	*/
	if ftp.debug {
		log.Printf("Walking: '%s'\n", path)
	}

	var lines []string

	if lines, err = ftp.List2(path); err != nil {
		return
	}

	for _, line := range lines {
		_, t, subpath := parseLine(line)

		switch t {
		case "dir":
			if subpath == "." {
			} else if subpath == ".." {
			} else {
				if err = ftp.Walk(path+subpath+"/", walkFn); err != nil {
					return
				}
			}
		case "file":
			if err = walkFn(path+subpath, os.FileMode(0), nil); err != nil {
				return
			}
		}
	}

	return
}

// Quit sends quit to the server and close the connection. No need to Close after this.
func (ftp *FTP) Quit() (err error) {
	if _, err := ftp.cmd(StatusConnectionClosing, "QUIT"); err != nil {
		return err
	}

	ftp.conn.Close()
	ftp.conn = nil

	return nil
}

// Noop will send a NOOP (no operation) to the server
func (ftp *FTP) Noop() (err error) {
	_, err = ftp.cmd(StatusOK, "NOOP")
	return
}

// RawCmd sends raw commands to the remote server. Returns response code as int and response as string.
func (ftp *FTP) RawCmd(command string, args ...interface{}) (code int, line string) {
	if ftp.debug {
		log.Printf("Raw-> %s\n", fmt.Sprintf(command, args...))
	}

	code = -1
	var err error
	if err = ftp.send(command, args...); err != nil {
		return code, ""
	}
	if line, err = ftp.receive(); err != nil {
		return code, ""
	}
	code, err = strconv.Atoi(line[:3])
	if ftp.debug {
		log.Printf("Raw<-	<- %d \n", code)
	}
	return code, line
}

// private function to send command and compare return code with expects
func (ftp *FTP) cmd(expects string, command string, args ...interface{}) (line string, err error) {
	if err = ftp.send(command, args...); err != nil {
		return
	}

	if line, err = ftp.receive(); err != nil {
		return
	}

	if !strings.HasPrefix(line, expects) {
		err = errors.New(line)
		return
	}

	return
}

// Rename file on the remote host
func (ftp *FTP) Rename(from string, to string) (err error) {
	if _, err = ftp.cmd(StatusActionPending, "RNFR %s", from); err != nil {
		return
	}

	if _, err = ftp.cmd(StatusActionOK, "RNTO %s", to); err != nil {
		return
	}

	return
}

// Mkd makes a directory on the remote host
func (ftp *FTP) Mkd(path string) error {
	_, err := ftp.cmd(StatusPathCreated, "MKD %s", path)
	return err
}

// Rmd remove directory
func (ftp *FTP) Rmd(path string) (err error) {
	_, err = ftp.cmd(StatusActionOK, "RMD %s", path)
	return
}

// Pwd gets current path on the remote host
func (ftp *FTP) Pwd() (path string, err error) {
	var line string
	if line, err = ftp.cmd(StatusPathCreated, "PWD"); err != nil {
		return
	}

	res := RePwdPath.FindAllStringSubmatch(line[4:], -1)

	path = res[0][1]
	return
}

// Cwd changes current working directory on remote host to path
func (ftp *FTP) Cwd(path string) (err error) {
	_, err = ftp.cmd(StatusActionOK, "CWD %s", path)
	return
}

// Dele deletes path on remote host
func (ftp *FTP) Dele(path string) (err error) {
	if err = ftp.send("DELE %s", path); err != nil {
		return
	}

	var line string
	if line, err = ftp.receive(); err != nil {
		return
	}

	if !strings.HasPrefix(line, StatusActionOK) {
		return errors.New(line)
	}

	return
}

// AuthTLS secures the ftp connection by using TLS
func (ftp *FTP) AuthTLS(config *tls.Config) error {
	if _, err := ftp.cmd("234", "AUTH TLS"); err != nil {
		return err
	}

	// wrap tls on existing connection
	ftp.tlsconfig = config

	ftp.conn = tls.Client(ftp.conn, config)
	ftp.writer = bufio.NewWriter(ftp.conn)
	ftp.reader = bufio.NewReader(ftp.conn)

	if _, err := ftp.cmd(StatusOK, "PBSZ 0"); err != nil {
		return err
	}

	if _, err := ftp.cmd(StatusOK, "PROT P"); err != nil {
		return err
	}

	return nil
}

// ReadAndDiscard reads all the buffered bytes and returns the number of bytes
// that cleared from the buffer
func (ftp *FTP) ReadAndDiscard() (int, error) {
	var i int
	bufferSize := ftp.reader.Buffered()
	for i = 0; i < bufferSize; i++ {
		if _, err := ftp.reader.ReadByte(); err != nil {
			return i, err
		}
	}
	return i, nil
}

// Type changes transfer type.
func (ftp *FTP) Type(t TypeCode) error {
	_, err := ftp.cmd(StatusOK, "TYPE %s", t)
	return err
}

// TypeCode for the representation types
type TypeCode string

const (
	// TypeASCII for ASCII
	TypeASCII = "A"
	// TypeEBCDIC for EBCDIC
	TypeEBCDIC = "E"
	// TypeImage for an Image
	TypeImage = "I"
	// TypeLocal for local byte size
	TypeLocal = "L"
)

func (ftp *FTP) receiveLine() (string, error) {
	line, err := ftp.reader.ReadString('\n')

	if ftp.debug {
		log.Printf("< %s", line)
	}

	return line, err
}

func (ftp *FTP) receive() (string, error) {
	line, err := ftp.receiveLine()

	if err != nil {
		return line, err
	}

	if (len(line) >= 4) && (line[3] == '-') {
		//Multiline response
		closingCode := line[:3] + " "
		for {
			str, err := ftp.receiveLine()
			line = line + str
			if err != nil {
				return line, err
			}
			if len(str) < 4 {
				if ftp.debug {
					log.Println("Uncorrectly terminated response")
				}
				break
			} else {
				if str[:4] == closingCode {
					break
				}
			}
		}
	}
	ftp.ReadAndDiscard()
	//fmt.Println(line)
	return line, err
}

func (ftp *FTP) receiveNoDiscard() (string, error) {
	line, err := ftp.receiveLine()

	if err != nil {
		return line, err
	}

	if (len(line) >= 4) && (line[3] == '-') {
		//Multiline response
		closingCode := line[:3] + " "
		for {
			str, err := ftp.receiveLine()
			line = line + str
			if err != nil {
				return line, err
			}
			if len(str) < 4 {
				if ftp.debug {
					log.Println("Uncorrectly terminated response")
				}
				break
			} else {
				if str[:4] == closingCode {
					break
				}
			}
		}
	}
	//ftp.ReadAndDiscard()
	//fmt.Println(line)
	return line, err
}

func (ftp *FTP) send(command string, arguments ...interface{}) error {
	if ftp.debug {
		log.Printf("> %s", fmt.Sprintf(command, arguments...))
	}

	command = fmt.Sprintf(command, arguments...)
	command += "\r\n"

	if _, err := ftp.writer.WriteString(command); err != nil {
		return err
	}

	if err := ftp.writer.Flush(); err != nil {
		return err
	}

	return nil
}

// Pasv enables passive data connection and returns port number

func (ftp *FTP) Pasv() (port int, err error) {
	doneChan := make(chan int, 1)
	go func() {
		defer func() {
			doneChan <- 1
		}()
		var line string
		if line, err = ftp.cmd("227", "PASV"); err != nil {
			return
		}
		re := regexp.MustCompile(`\((.*)\)`)
		res := re.FindAllStringSubmatch(line, -1)
		if len(res) == 0 || len(res[0]) < 2 {
			err = errors.New("PasvBadAnswer")
			return
		}
		s := strings.Split(res[0][1], ",")
		if len(s) < 2 {
			err = errors.New("PasvBadAnswer")
			return
		}
		l1, _ := strconv.Atoi(s[len(s)-2])
		l2, _ := strconv.Atoi(s[len(s)-1])

		port = l1<<8 + l2

		return
	}()

	select {
	case _ = <-doneChan:

	case <-time.After(time.Second * 10):
		err = errors.New("PasvTimeout")
		ftp.Close()
	}

	return
}

// open new data connection
func (ftp *FTP) newConnection(port int) (conn net.Conn, err error) {
	addr := fmt.Sprintf("%s:%d", strings.Split(ftp.addr, ":")[0], port)

	if ftp.debug {
		log.Printf("Connecting to %s\n", addr)
	}

	if conn, err = net.Dial("tcp", addr); err != nil {
		return
	}

	if ftp.tlsconfig != nil {
		conn = tls.Client(conn, ftp.tlsconfig)
	}

	return
}

// Stor uploads file to remote host path, from r
func (ftp *FTP) Stor(path string, r io.Reader) error {
	if err := ftp.Type(TypeImage); err != nil {
		return err
	}

	port, err := ftp.Pasv()
	if err != nil {
		return err
	}

	if err := ftp.send("STOR %s", path); err != nil {
		return err
	}

	pconn, err := ftp.newConnection(port)
	if err != nil {
		return err
	}
	defer pconn.Close()

	line, err := ftp.receive()
	if err != nil {
		return err
	}

	if _, err := io.Copy(pconn, r); err != nil {
		fmt.Println(7)
		return err
	}
	pconn.Close()

	if line, err = ftp.receive(); err != nil {
		fmt.Println(8)
		return err
	}

	if !strings.HasPrefix(line, StatusClosingDataConnection) {
		err := errors.New(line)
		fmt.Println(9)
		return err
	}
	return nil
}

func (ftp *FTP) RetrFrom(path string, offset uint64, retrFn RetrFunc) error {
	if err := ftp.Type(TypeImage); err != nil {
		return err
	}

	port, err := ftp.Pasv()
	if err != nil {
		return err
	}

	if err := ftp.send("REST %d", offset); err != nil {
		return err
	}

	if err := ftp.send("RETR %s", path); err != nil {
		return err
	}

	var pconn net.Conn
	if pconn, err = ftp.newConnection(port); err != nil {
		return err
	}
	defer pconn.Close()

	var line string
	if line, err = ftp.receiveNoDiscard(); err != nil {
		return err
	}

	if err = retrFn(pconn); err != nil {
		return err
	}

	pconn.Close()

	if line, err = ftp.receive(); err != nil {
		return err
	}

	if !strings.HasPrefix(line, StatusClosingDataConnection) {
		err = errors.New(line)
		return err
	}

	return nil
}

func (ftp *FTP) StorFrom(path string, r io.Reader, offset uint64) error {
	if err := ftp.Type(TypeImage); err != nil {
		return err
	}

	port, err := ftp.Pasv()
	if err != nil {
		return err
	}

	if err := ftp.send("REST %d", offset); err != nil {
		return err
	}

	if err := ftp.send("STOR %s", path); err != nil {
		return err
	}
	pconn, err := ftp.newConnection(port)
	if err != nil {
		return err
	}
	defer pconn.Close()

	line, err := ftp.receive()
	if err != nil {
		return err
	}

	if _, err := io.Copy(pconn, r); err != nil {
		fmt.Println(7)
		return err
	}
	pconn.Close()

	if line, err = ftp.receive(); err != nil {
		fmt.Println(8)
		return err
	}

	if !strings.HasPrefix(line, StatusClosingDataConnection) {
		return nil
	}
	return nil
}

// Syst returns the system type of the remote host
func (ftp *FTP) Syst() (line string, err error) {
	if err := ftp.send("SYST"); err != nil {
		return "", err
	}
	if line, err = ftp.receive(); err != nil {
		return
	}
	if !strings.HasPrefix(line, StatusSystemType) {
		err = errors.New(line)
		return
	}

	return strings.SplitN(strings.TrimSpace(line), " ", 2)[1], nil
}

// System types from Syst
var (
	SystemTypeUnixL8    = "UNIX Type: L8"
	SystemTypeWindowsNT = "Windows_NT"
)

var reSystStatus = map[string]*regexp.Regexp{
	SystemTypeUnixL8:    regexp.MustCompile(""),
	SystemTypeWindowsNT: regexp.MustCompile(""),
}

// Stat gets the status of path from the remote host
func (ftp *FTP) Stat(path string) ([]string, error) {
	if err := ftp.send("STAT %s", path); err != nil {
		return nil, err
	}

	stat, err := ftp.receive()
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(stat, StatusFileStatus) &&
		!strings.HasPrefix(stat, StatusDirectoryStatus) &&
		!strings.HasPrefix(stat, StatusSystemStatus) {
		return nil, errors.New(stat)
	}
	if strings.HasPrefix(stat, StatusSystemStatus) {
		return strings.Split(stat, "\n"), nil
	}
	lines := []string{}
	for _, line := range strings.Split(stat, "\n") {
		if strings.HasPrefix(line, StatusFileStatus) {
			continue
		}
		//fmt.Printf("%v\n", re.FindAllStringSubmatch(line, -1))
		lines = append(lines, strings.TrimSpace(line))

	}
	// TODO(vbatts) parse this line for SystemTypeWindowsNT
	//"213-status of /remfdata/all.zip:\r\n    09-12-15  04:07AM             37192705 all.zip\r\n213 End of status.\r\n"

	// and this for SystemTypeUnixL8
	// "-rw-r--r--   22 4015     4015        17976 Jun 10  1994 COPYING"
	// "drwxr-xr-x    6 4015     4015         4096 Aug 21 17:25 kernels"
	return lines, nil
}

// Retr retrieves file from remote host at path, using retrFn to read from the remote file.
func (ftp *FTP) Retr(path string, retrFn RetrFunc) (s string, err error) {
	if err = ftp.Type(TypeImage); err != nil {
		return
	}

	var port int
	if port, err = ftp.Pasv(); err != nil {
		return
	}

	if err = ftp.send("RETR %s", path); err != nil {
		return
	}

	var pconn net.Conn
	if pconn, err = ftp.newConnection(port); err != nil {
		return
	}
	defer pconn.Close()

	var line string
	if line, err = ftp.receiveNoDiscard(); err != nil {
		return
	}

	if err = retrFn(pconn); err != nil {
		return
	}

	pconn.Close()

	if line, err = ftp.receive(); err != nil {
		return
	}

	if !strings.HasPrefix(line, StatusClosingDataConnection) {
		err = errors.New(line)
		return
	}

	return
}

/*func GetFilesList(path string) (files []string, err error) {

}*/

// List lists the path (or current directory)
func (ftp *FTP) List(path string) (entries []*Entry, err error) {
	if err = ftp.Type(TypeASCII); err != nil {
		return
	}

	var port int
	if port, err = ftp.Pasv(); err != nil {
		return
	}

	// check if MLSD works
	if err = ftp.send("MLSD %s", path); err != nil {
	}

	var pconn net.Conn
	if pconn, err = ftp.newConnection(port); err != nil {
		return
	}
	defer pconn.Close()

	var line string
	if line, err = ftp.receiveNoDiscard(); err != nil {
		return
	}

	var parser parseFunc
	parser = parseRFC3659ListLine

	if !strings.HasPrefix(line, StatusFileOK) {
		// MLSD failed, lets try LIST
		parser = parseListLine
		if err = ftp.send("LIST %s", path); err != nil {
			return
		}

		if line, err = ftp.receiveNoDiscard(); err != nil {
			return
		}
	}

	// reader := bufio.NewReader(pconn)

	// for {
	// 	line, err = reader.ReadString('\n')
	// 	if err == io.EOF {
	// 		break
	// 	} else if err != nil {
	// 		return
	// 	}

	// 	files = append(files, string(line))
	// }
	// Must close for vsftp tlsed conenction otherwise does not receive connection
	scanner := bufio.NewScanner(pconn)
	now := time.Now()
	for scanner.Scan() {
		entry, err := parser(scanner.Text(), now, time.UTC)
		if err == nil {
			entries = append(entries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return
	if line, err = ftp.receive(); err != nil {
		return
	}

	if !strings.HasPrefix(line, StatusClosingDataConnection) {
		err = errors.New(line)
		return
	}

	return
}

/*


// login on server with strange login behavior
func (ftp *FTP) SmartLogin(username string, password string) (err error) {
	var code int
	// Maybe the server has some useless words to say. Make him talk
	code, _ = ftp.RawCmd("NOOP")

	if code == 220 || code == 530 {
		// Maybe with another Noop the server will ask us to login?
		code, _ = ftp.RawCmd("NOOP")
		if code == 530 {
			// ok, let's login
			code, _ = ftp.RawCmd("USER %s", username)
			code, _ = ftp.RawCmd("NOOP")
			if code == 331 {
				// user accepted, password required
				code, _ = ftp.RawCmd("PASS %s", password)
				code, _ = ftp.RawCmd("PASS %s", password)
				if code == 230 {
					code, _ = ftp.RawCmd("NOOP")
					return
				}
			}
		}

	}
	// Nothing strange... let's try a normal login
	return ftp.Login(username, password)
}

*/

// Login to the server with provided username and password.
// Typical default may be ("anonymous","").
func (ftp *FTP) Login(username string, password string) (err error) {
	if _, err = ftp.cmd("331", "USER %s", username); err != nil {
		if strings.HasPrefix(err.Error(), "230") {
			// Ok, probably anonymous server
			// but login was fine, so return no error
			err = nil
		} else {
			return
		}
	}

	if _, err = ftp.cmd("230", "PASS %s", password); err != nil {
		return
	}

	return
}

// Connect to server at addr (format "host:port"). debug is OFF
func Connect(addr string) (*FTP, error) {
	var err error
	var conn net.Conn

	if conn, err = net.Dial("tcp", addr); err != nil {
		return nil, err
	}

	writer := bufio.NewWriter(conn)
	reader := bufio.NewReader(conn)

	//reader.ReadString('\n')
	object := &FTP{conn: conn, addr: addr, reader: reader, writer: writer, debug: false}
	object.receive()

	return object, nil
}

// ConnectDbg to server at addr (format "host:port"). debug is ON
func ConnectDbg(addr string) (*FTP, error) {
	var err error
	var conn net.Conn

	if conn, err = net.Dial("tcp", addr); err != nil {
		return nil, err
	}

	writer := bufio.NewWriter(conn)
	reader := bufio.NewReader(conn)

	var line string

	object := &FTP{conn: conn, addr: addr, reader: reader, writer: writer, debug: true}
	line, _ = object.receive()

	log.Print(line)

	return object, nil
}

// Size returns the size of a file.
func (ftp *FTP) Size(path string) (size int, err error) {
	line, err := ftp.cmd("213", "SIZE %s", path)

	if err != nil {
		return 0, err
	}

	return strconv.Atoi(line[4 : len(line)-2])
}

func parseRFC3659ListLine(line string, now time.Time, loc *time.Location) (*Entry, error) {
	iSemicolon := strings.Index(line, ";")
	iWhitespace := strings.Index(line, " ")

	if iSemicolon < 0 || iSemicolon > iWhitespace {
		return nil, errUnsupportedListLine
	}

	e := &Entry{
		Name: line[iWhitespace+1:],
	}

	for _, field := range strings.Split(line[:iWhitespace-1], ";") {
		i := strings.Index(field, "=")
		if i < 1 {
			return nil, errUnsupportedListLine
		}

		key := strings.ToLower(field[:i])
		value := field[i+1:]

		switch key {
		case "modify":
			var err error
			e.Time, err = time.ParseInLocation("20060102150405", value, loc)
			if err != nil {
				return nil, err
			}
		case "type":
			switch value {
			case "dir", "cdir", "pdir":
				e.Type = EntryTypeFolder
			case "file":
				e.Type = EntryTypeFile
			}
		case "size":
			e.setSize(value)
		}
	}
	return e, nil
}

// parseLsListLine parses a directory line in a format based on the output of
// the UNIX ls command.
func parseLsListLine(line string, now time.Time, loc *time.Location) (*Entry, error) {

	// Has the first field a length of exactly 10 bytes
	// - or 10 bytes with an additional '+' character for indicating ACLs?
	// If not, return.
	if i := strings.IndexByte(line, ' '); !(i == 10 || (i == 11 && line[10] == '+')) {
		return nil, errUnsupportedListLine
	}

	scanner := newScanner(line)
	fields := scanner.NextFields(6)

	if len(fields) < 6 {
		return nil, errUnsupportedListLine
	}

	if fields[1] == "folder" && fields[2] == "0" {
		e := &Entry{
			Type: EntryTypeFolder,
			Name: scanner.Remaining(),
		}
		if err := e.setTime(fields[3:6], now, loc); err != nil {
			return nil, err
		}

		return e, nil
	}

	if fields[1] == "0" {
		fields = append(fields, scanner.Next())
		e := &Entry{
			Type: EntryTypeFile,
			Name: scanner.Remaining(),
		}

		if err := e.setSize(fields[2]); err != nil {
			return nil, errUnsupportedListLine
		}
		if err := e.setTime(fields[4:7], now, loc); err != nil {
			return nil, err
		}

		return e, nil
	}

	// Read two more fields
	fields = append(fields, scanner.NextFields(2)...)
	if len(fields) < 8 {
		return nil, errUnsupportedListLine
	}

	e := &Entry{
		Name: scanner.Remaining(),
	}
	switch fields[0][0] {
	case '-':
		e.Type = EntryTypeFile
		if err := e.setSize(fields[4]); err != nil {
			return nil, err
		}
	case 'd':
		e.Type = EntryTypeFolder
	case 'l':
		e.Type = EntryTypeLink

		// Split link name and target
		if i := strings.Index(e.Name, " -> "); i > 0 {
			e.Target = e.Name[i+4:]
			e.Name = e.Name[:i]
		}
	default:
		return nil, errUnknownListEntryType
	}

	if err := e.setTime(fields[5:8], now, loc); err != nil {
		return nil, err
	}

	return e, nil
}

// parseDirListLine parses a directory line in a format based on the output of
// the MS-DOS DIR command.
func parseDirListLine(line string, now time.Time, loc *time.Location) (*Entry, error) {
	e := &Entry{}
	var err error

	// Try various time formats that DIR might use, and stop when one works.
	for _, format := range dirTimeFormats {
		if len(line) > len(format) {
			e.Time, err = time.ParseInLocation(format, line[:len(format)], loc)
			if err == nil {
				line = line[len(format):]
				break
			}
		}
	}
	if err != nil {
		// None of the time formats worked.
		return nil, errUnsupportedListLine
	}

	line = strings.TrimLeft(line, " ")
	if strings.HasPrefix(line, "<DIR>") {
		e.Type = EntryTypeFolder
		line = strings.TrimPrefix(line, "<DIR>")
	} else {
		space := strings.Index(line, " ")
		if space == -1 {
			return nil, errUnsupportedListLine
		}
		e.Size, err = strconv.ParseUint(line[:space], 10, 64)
		if err != nil {
			return nil, errUnsupportedListLine
		}
		e.Type = EntryTypeFile
		line = line[space:]
	}

	e.Name = strings.TrimLeft(line, " ")
	return e, nil
}

// parseHostedFTPLine parses a directory line in the non-standard format used
// by hostedftp.com
// -r--------   0 user group     65222236 Feb 24 00:39 UABlacklistingWeek8.csv
// (The link count is inexplicably 0)
func parseHostedFTPLine(line string, now time.Time, loc *time.Location) (*Entry, error) {
	// Has the first field a length of 10 bytes?
	if strings.IndexByte(line, ' ') != 10 {
		return nil, errUnsupportedListLine
	}

	scanner := newScanner(line)
	fields := scanner.NextFields(2)

	if len(fields) < 2 || fields[1] != "0" {
		return nil, errUnsupportedListLine
	}

	// Set link count to 1 and attempt to parse as Unix.
	return parseLsListLine(fields[0]+" 1 "+scanner.Remaining(), now, loc)
}

// parseListLine parses the various non-standard format returned by the LIST
// FTP command.
func parseListLine(line string, now time.Time, loc *time.Location) (*Entry, error) {
	for _, f := range listLineParsers {
		e, err := f(line, now, loc)
		if err != errUnsupportedListLine {
			return e, err
		}
	}
	return nil, errUnsupportedListLine
}

var listLineParsers = []parseFunc{
	parseRFC3659ListLine,
	parseLsListLine,
	parseDirListLine,
	parseHostedFTPLine,
}

type scanner struct {
	bytes    []byte
	position int
}

// newScanner creates a new scanner
func newScanner(str string) *scanner {
	return &scanner{
		bytes: []byte(str),
	}
}

func (ftp *FTP) List2(path string) (files []string, err error) {
	if err = ftp.Type(TypeASCII); err != nil {
		return
	}

	var port int
	if port, err = ftp.Pasv(); err != nil {
		return
	}

	// check if MLSD works
	if err = ftp.send("MLSD %s", path); err != nil {
	}

	var pconn net.Conn
	if pconn, err = ftp.newConnection(port); err != nil {
		return
	}
	defer pconn.Close()

	var line string
	if line, err = ftp.receiveNoDiscard(); err != nil {
		return
	}

	if !strings.HasPrefix(line, StatusFileOK) {
		// MLSD failed, lets try LIST
		if err = ftp.send("LIST %s", path); err != nil {
			return
		}

		if line, err = ftp.receiveNoDiscard(); err != nil {
			return
		}
	}

	reader := bufio.NewReader(pconn)

	for {
		line, err = reader.ReadString('\n')
		if err == io.EOF {
			break
		} else if err != nil {
			return
		}

		files = append(files, string(line))
	}
	// Must close for vsftp tlsed conenction otherwise does not receive connection
	pconn.Close()

	if line, err = ftp.receive(); err != nil {
		return
	}

	if !strings.HasPrefix(line, StatusClosingDataConnection) {
		err = errors.New(line)
		return
	}

	return
}

func (s *scanner) NextFields(count int) []string {
	fields := make([]string, 0, count)
	for i := 0; i < count; i++ {
		if field := s.Next(); field != "" {
			fields = append(fields, field)
		} else {
			break
		}
	}
	return fields
}

// Remaining returns the remaining string
func (s *scanner) Remaining() string {
	return string(s.bytes[s.position:len(s.bytes)])
}

func (s *scanner) Next() string {
	sLen := len(s.bytes)

	// skip trailing whitespace
	for s.position < sLen {
		if s.bytes[s.position] != ' ' {
			break
		}
		s.position++
	}

	start := s.position

	// skip non-whitespace
	for s.position < sLen {
		if s.bytes[s.position] == ' ' {
			s.position++
			return string(s.bytes[start : s.position-1])
		}
		s.position++
	}

	return string(s.bytes[start:s.position])
}

func (e *Entry) setSize(str string) (err error) {
	e.Size, err = strconv.ParseUint(str, 0, 64)
	return
}

func (e *Entry) setTime(fields []string, now time.Time, loc *time.Location) (err error) {
	if strings.Contains(fields[2], ":") { // contains time
		thisYear, _, _ := now.Date()
		timeStr := fmt.Sprintf("%s %s %d %s", fields[1], fields[0], thisYear, fields[2])
		e.Time, err = time.ParseInLocation("_2 Jan 2006 15:04", timeStr, loc)

		/*
			On unix, `info ls` shows:

			10.1.6 Formatting file timestamps
			---------------------------------

			A timestamp is considered to be “recent” if it is less than six
			months old, and is not dated in the future.  If a timestamp dated today
			is not listed in recent form, the timestamp is in the future, which
			means you probably have clock skew problems which may break programs
			like ‘make’ that rely on file timestamps.
		*/
		if !e.Time.Before(now.AddDate(0, 6, 0)) {
			e.Time = e.Time.AddDate(-1, 0, 0)
		}

	} else { // only the date
		if len(fields[2]) != 4 {
			return errUnsupportedListDate
		}
		timeStr := fmt.Sprintf("%s %s %s 00:00", fields[1], fields[0], fields[2])
		e.Time, err = time.ParseInLocation("_2 Jan 2006 15:04", timeStr, loc)
	}
	return
}
