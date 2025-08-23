package opipes

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"os"
)

type WriterPipeInfo struct {
	Args []string        `json:"args"`
	Next *WriterPipeInfo `json:"next"`
}

type ReaderPipeInfo struct {
	Address string `json:"address"`
}

type ReaderPipe struct {
	address        string
	writerPipeInfo WriterPipeInfo
	conn           net.Conn
}

func (r *ReaderPipe) Read(bytes []byte) (int, error) {
	if r.conn == nil {
		slog.Debug("connecting to Reader pipe", "address", r.address)

		var err error
		r.conn, err = net.Dial("unix", r.address)
		if err != nil {
			return 0, err
		}

		// send our info
		msgBytes, err := json.Marshal(r.writerPipeInfo)
		if err != nil {
			return 0, err
		}
		// TODO:
		_, err = r.conn.Write(msgBytes)
		if err != nil {
			return 0, err
		}
	}

	// TODO: implement reconnection
	return r.conn.Read(bytes)
}

func (r *ReaderPipe) Close() error {
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}

type WriterPipe struct {
	conn net.Conn
}

func (w *WriterPipe) Write(bytes []byte) (int, error) {
	// TODO: implement reconnection
	return w.conn.Write(bytes)
}

func (w *WriterPipe) Close() error {
	if w.conn != nil {
		return w.conn.Close()
	}
	return nil
}

type OConfig struct {
	ProgramName string
	Args        []string
}

type OPipes struct {
	OConfig
	cleanupFuncs        []func()
	origStdout          *os.File
	stdinIsPipe         bool
	stdoutIsPipe        bool
	updatedIncomingPipe bool
	returnedStdout      bool
	consumedStdin       bool
}

var initialized *OPipes

func Init(cfg OConfig) *OPipes {
	if initialized != nil {
		return initialized
	}
	initialized = &OPipes{
		OConfig: cfg,
	}
	o := initialized

	// setup logging
	level := slog.LevelInfo
	switch os.Getenv("OLOGLEVEL") {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
	case "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}).WithAttrs([]slog.Attr{
		slog.String("program", cfg.ProgramName),
		slog.Int("pid", os.Getpid()),
	})))

	// understand if left and right are pipes or not
	stdinStat, err := os.Stdin.Stat()
	if err != nil {
		panic(err)
	}
	o.stdinIsPipe = (stdinStat.Mode() & os.ModeNamedPipe) != 0

	stdoutStat, err := os.Stdout.Stat()
	if err != nil {
		panic(err)
	}
	o.stdoutIsPipe = (stdoutStat.Mode() & os.ModeNamedPipe) != 0

	// println goes to stderr to avoid messing up with the stdout protocol
	o.origStdout = os.Stdout
	os.Stdout = os.Stderr

	slog.Debug("initialized", "o", o)
	return o
}

func (o *OPipes) newWriterPipe() (io.WriteCloser, *WriterPipeInfo, error) {
	if !o.stdoutIsPipe {
		slog.Debug("returning stdout as Writer")
		return o.origStdout, nil, nil
	}

	runDir := os.Getenv("XDG_RUNTIME_DIR")
	randInt := rand.Int()
	address := fmt.Sprintf("%s/opipes-%d.sock", runDir, randInt)

	listener, err := net.Listen("unix", address)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = listener.Close() }()

	// send our address to the right-side of the pipeline via stdout
	ourPipe := ReaderPipeInfo{
		Address: address,
	}
	msgBytes, err := json.Marshal(ourPipe)
	if err != nil {
		return nil, nil, err
	}

	slog.Debug("sending Writer pipe", "Writer", asJson(ourPipe))
	_, err = o.origStdout.Write(msgBytes)
	if err != nil {
		return nil, nil, err
	}

	// wait for connection from the right-side of the pipeline
	conn, err := listener.Accept()
	if err != nil {
		return nil, nil, err
	}

	var writerPipeInfo WriterPipeInfo
	// read line from conn
	err = json.NewDecoder(conn).Decode(&writerPipeInfo)
	if err != nil {
		return nil, nil, err
	}
	slog.Debug("received Writer pipe", "Writer", asJson(writerPipeInfo))

	return &WriterPipe{conn: conn}, &writerPipeInfo, nil
}

type OPipe struct {
	Reader     io.ReadCloser
	Writer     io.WriteCloser
	WriterInfo *WriterPipeInfo
}

func (o *OPipes) NewPipe() (*OPipe, error) {
	// chicken-egg problem: before creating a Writer pipe we want a Reader pipe, however we also want to pass the Writer info the Reader pipe
	var reader io.ReadCloser
	var readerPipeInfo ReaderPipeInfo
	if !o.stdinIsPipe {
		if o.consumedStdin {
			slog.Debug("stdin already consumed, returning nil pipe")
			return nil, nil
		}
		slog.Debug("consuming stdin for Reader pipe")
		o.consumedStdin = true
		reader = os.Stdin
	} else {
		err := json.NewDecoder(os.Stdin).Decode(&readerPipeInfo)
		if err != nil {
			if err == io.EOF {
				return nil, nil
			}
			return nil, err
		}
		slog.Debug("received Reader pipe", "Reader", asJson(readerPipeInfo))
	}

	writer, writerInfo, err := o.newWriterPipe()
	if err != nil {
		if reader != nil {
			_ = reader.Close()
		}
		return nil, err
	}

	if reader == nil {
		reader = &ReaderPipe{
			address: readerPipeInfo.Address,
			writerPipeInfo: WriterPipeInfo{
				Args: o.Args,
				Next: writerInfo,
			},
		}
	}

	slog.Debug("created Reader+Writer pipe")
	return &OPipe{
		Reader:     reader,
		Writer:     writer,
		WriterInfo: writerInfo,
	}, nil
}

func asJson(v any) string {
	res, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("ERR(%v)", v)
	}
	return string(res)
}
