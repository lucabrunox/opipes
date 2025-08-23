package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/lucabrunox/opipes/opipes"
	"github.com/spf13/cobra"
)

type OWrap struct {
	args       []string
	actualArgs []string
}

func Execute() error {
	state := OWrap{}

	var cmd = &cobra.Command{
		Use:   "o [command...]",
		Short: "wrap a command with opipes",
		RunE: func(cmd *cobra.Command, args []string) error {
			return state.runCmd(context.Background())
		},
	}

	// by default all args are passed verbatim to the wrapper command and must not be interpreted by cobra, unless there's a "--"
	state.args = os.Args[1:]
	cmd.SetArgs([]string{})
	for i, arg := range os.Args {
		if arg == "--" {
			state.args = os.Args[i+1:]
			cmd.SetArgs(os.Args[:i-1])
			break
		}
	}

	return cmd.Execute()
}

func (state *OWrap) runWrappedCmd(ctx context.Context, reader io.ReadCloser, writer io.WriteCloser) error {
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()

	slog.Debug("starting command", "args", state.actualArgs)
	c := exec.Command(state.actualArgs[0], state.actualArgs[1:]...)
	c.Stderr = os.Stderr
	stdin, err := c.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		return err
	}

	err = c.Start()
	if err != nil {
		return err
	}

	// read from reader and write into stdin
	stdinCopyDone := make(chan error, 1)
	go func() {
		defer func() { _ = stdin.Close() }()
		_, err := io.Copy(stdin, reader)
		if err != nil {
			stdinCopyDone <- err
		}
		stdinCopyDone <- nil
	}()

	// read from stdout and write into writer
	stdoutCopyDone := make(chan error, 1)
	go func() {
		defer func() { _ = stdout.Close() }()
		_, err := io.Copy(writer, stdout)
		if err != nil {
			stdoutCopyDone <- err
		}
		stdoutCopyDone <- nil
	}()

	err = c.Wait()
	if err != nil {
		return nil
	}

	err = <-stdoutCopyDone
	if err != nil {
		return err
	}

	select {
	case err = <-stdinCopyDone:
		return err
	default:
		// unfortunately if the stdin is os.Stdin then the copy will leak forever until the user sends an EOF, even if it's closed
		return nil
	}
}

func (state *OWrap) runCmd(ctx context.Context) error {
	if len(state.args) == 0 {
		return fmt.Errorf("no command given")
	}

	o := opipes.Init(opipes.OConfig{
		ProgramName: state.args[0],
		Args:        state.args,
	})

	pipe, err := o.NewPipe()
	if err != nil {
		return err
	}
	if pipe == nil {
		return nil
	}

	state.actualArgs = state.replaceArgs(pipe.WriterInfo)
	return state.runWrappedCmd(ctx, pipe.Reader, pipe.Writer)
}

func (state *OWrap) replaceArgs(writerInfo *opipes.WriterPipeInfo) []string {
	var res []string
	for _, arg := range state.args {
		if arg == "{awsLogFilter}" {
			res = append(res, state.parseAwsLogFilter(writerInfo))
		} else {
			res = append(res, arg)
		}
	}
	return res
}

func (state *OWrap) parseAwsLogFilter(writerInfo *opipes.WriterPipeInfo) string {
	res := ""
	canPushdownGrep := true
	for writerInfo != nil {
		if writerInfo.Args[0] == "grep" && canPushdownGrep {
			// poor man parsing, but you get the idea, if only cloud providers could handle this for us...
			if len(writerInfo.Args) == 2 && !strings.HasPrefix(writerInfo.Args[1], "-") {
				res = res + " " + writerInfo.Args[1]
				slog.Debug("good :) pushed down grep filter to speed up query", "args", writerInfo.Args)
			} else {
				slog.Debug("bad :( cannot pushdown unknown grep filter", "args", writerInfo.Args)
			}
		} else {
			slog.Debug("cannot pushdown unknown command", "args", writerInfo.Args)
			// we can't say anything from the transformations done from grep here onwards, so we can't pushdown greps either, but keep going through the rest of the pipeline for logging purposes
			canPushdownGrep = false
		}

		writerInfo = writerInfo.Next
	}
	return res
}

func main() {
	err := Execute()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
