//go:build linux

//nolint:gocognit,gocyclo,mnd // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package directruntime

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// runTerminalSession runs one CLI command on its own pseudo-terminal with this process acting
// as the terminal: input passes through unchanged, terminal capability queries and side-band
// sequences are removed from the output before forwarding. The session's exit status becomes
// this process's result.
func runTerminalSession(binary string, command []string) error {
	//nolint:gosec,noctx // Plan-scoped local session that lives for the interactive terminal.
	session := exec.Command(binary, command[1:]...)
	session.Args = command
	// `<runtime> exec -it` sessions always carry a real terminal identity, while the shim's own
	// process runs terminal-silent (TERM=dumb from the lifecycle boundary): replace, because a
	// child's getenv returns the first match.
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "TERM=") || strings.HasPrefix(entry, "NO_COLOR=") {
			continue
		}

		environment = append(environment, entry)
	}

	//nolint:gocritic // the result deliberately extends a different base slice.
	session.Env = append(
		environment,
		"TERM=xterm",
	) //nolint:gocritic // the child deliberately extends the filtered environment.

	terminal, err := pty.StartWithSize(session, &pty.Winsize{Rows: 255, Cols: 512})
	if err != nil {
		return fmt.Errorf("runtime CLI session terminal is unavailable: %w", err)
	}

	defer func() { _ = terminal.Close() }()
	// Nothing this process writes may be echoed back into the forwarded stream; interactive
	// CLIs perform their own application-level echo once running.
	state, termiosErr := unix.IoctlGetTermios(int(terminal.Fd()), unix.TCGETS)
	if termiosErr == nil {
		state.Lflag &^= unix.ECHO | unix.ECHONL
		_ = unix.IoctlSetTermios(int(terminal.Fd()), unix.TCSETS, state)
	}

	go func() {
		_, _ = io.Copy(terminal, os.Stdin)
	}()

	filter := &terminalQueryFilter{output: os.Stdout}
	copyErr := filter.consume(terminal)

	waitErr := session.Wait()
	if waitErr != nil {
		return fmt.Errorf("runtime CLI session ended: %w", waitErr)
	}

	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		return nil
	}

	return nil
}

// terminalQueryFilter relays a pseudo-terminal output stream with terminal-directed side-band
// data (capability queries and OSC sequences) removed, so screen-scraping readers see only
// application output.
type terminalQueryFilter struct {
	output  io.Writer
	pending []byte
}

func (f *terminalQueryFilter) consume(source io.Reader) error {
	buffer := make([]byte, 4096)
	for {
		count, err := source.Read(buffer)
		if count > 0 {
			forward := f.filter(buffer[:count])
			if len(forward) > 0 {
				if _, writeErr := f.output.Write(forward); writeErr != nil {
					return writeErr
				}
			}
		}

		if err != nil {
			return err
		}
	}
}

//nolint:gocognit,gocyclo // One pass classifies every escape sequence boundary explicitly.
func (f *terminalQueryFilter) filter(chunk []byte) []byte {
	data := append(f.pending, chunk...) //nolint:gocritic // pending is consumed here.
	f.pending = nil

	forward := make([]byte, 0, len(data))
	for index := 0; index < len(data); {
		value := data[index]
		if value != 0x1b {
			if value != 0x00 {
				forward = append(forward, value)
			}

			index++

			continue
		}

		sequence, kind, complete := scanEscapeSequence(data[index:])
		if !complete {
			f.pending = append([]byte{}, data[index:]...)

			break
		}

		switch kind {
		case escapeQueryDSR, escapeQueryCPR, escapeOSC:
			// Terminal-directed side-band sequences are removed, never answered: an answer
			// written into the session's input would interleave with caller-sent commands and
			// corrupt them, and interactive CLIs proceed without answers.
		default:
			forward = append(forward, sequence...)
		}

		index += len(sequence)
	}

	return forward
}

type escapeKind int

const (
	escapePassthrough escapeKind = iota
	escapeOSC
	escapeQueryDSR
	escapeQueryCPR
)

// scanEscapeSequence classifies one escape sequence at the start of data. It reports the
// sequence bytes, its kind, and whether the sequence is complete within data.
func scanEscapeSequence(data []byte) ([]byte, escapeKind, bool) {
	if len(data) < 2 {
		return nil, escapePassthrough, false
	}

	switch data[1] {
	case '[':
		for index := 2; index < len(data); index++ {
			value := data[index]
			if value >= 0x40 && value <= 0x7e {
				sequence := data[:index+1]
				if value == 'n' {
					body := string(data[2:index])
					if body == "5" {
						return sequence, escapeQueryDSR, true
					}
				}

				if value == 'n' && string(data[2:index]) == "6" {
					return sequence, escapeQueryCPR, true
				}

				return sequence, escapePassthrough, true
			}

			if index-2 > 64 {
				return data[:index+1], escapePassthrough, true
			}
		}

		return nil, escapePassthrough, false
	case ']':
		for index := 2; index < len(data); index++ {
			if data[index] == 0x07 {
				return data[:index+1], escapeOSC, true
			}

			if data[index] == 0x1b && index+1 < len(data) && data[index+1] == '\\' {
				return data[:index+2], escapeOSC, true
			}

			if index > 4096 {
				return data[:index+1], escapeOSC, true
			}
		}

		return nil, escapePassthrough, false
	default:
		return data[:2], escapePassthrough, true
	}
}
