package remoteexec

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"agentic9/internal/transport/rcpu"
)

const sentinelPrefix = "\x01agentic9-exit "

type Result struct {
	ExitCode     int
	RemoteStatus string
}

type Runner struct {
	exec      rcpu.Executor
	workspace string
}

func NewRunner(exec rcpu.Executor, workspace string) *Runner {
	return &Runner{exec: exec, workspace: workspace}
}

func (r *Runner) Run(ctx context.Context, command []string, output func([]byte) error) (Result, error) {
	parser := &Parser{}
	script := BuildScript(r.workspace, command)
	err := r.exec.Exec(ctx, script, func(chunk []byte) error {
		visible, done, status := parser.Feed(chunk)
		if len(visible) > 0 && output != nil {
			if err := output(visible); err != nil {
				return err
			}
		}
		if done {
			parser.SetResult(status)
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	if !parser.Done() {
		return Result{}, fmt.Errorf("remote session ended before exit sentinel")
	}
	if parser.Status() == "" {
		return Result{ExitCode: 0}, nil
	}
	return Result{ExitCode: 1, RemoteStatus: parser.Status()}, nil
}

func BuildScript(workspace string, command []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cd %s || exit 'cd'\n", rcQuote(workspace))
	b.WriteString(strings.Join(command, " "))
	b.WriteByte('\n')
	b.WriteString("status=$status\n")
	fmt.Fprintf(&b, "echo '%s'$status\n", sentinelPrefix)
	b.WriteString("exit $status\n")
	return b.String()
}

func rcQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

type Parser struct {
	buf    bytes.Buffer
	done   bool
	status string
}

func (p *Parser) Feed(chunk []byte) (visible []byte, done bool, status string) {
	if p.done {
		return nil, true, p.status
	}
	_, _ = p.buf.Write(chunk)
	data := p.buf.Bytes()
	idx := bytes.Index(data, []byte(sentinelPrefix))
	if idx < 0 {
		keep := overlapSuffix(data, []byte(sentinelPrefix))
		if len(data) > keep {
			visible = append([]byte(nil), data[:len(data)-keep]...)
			p.buf.Reset()
			_, _ = p.buf.Write(data[len(data)-keep:])
		}
		return visible, false, ""
	}
	visible = append([]byte(nil), data[:idx]...)
	rest := data[idx+len(sentinelPrefix):]
	if nl := bytes.IndexByte(rest, '\n'); nl >= 0 {
		p.done = true
		p.status = strings.TrimSpace(string(rest[:nl]))
		return visible, true, p.status
	}
	return nil, false, ""
}

func (p *Parser) SetResult(status string) { p.done, p.status = true, status }
func (p *Parser) Done() bool              { return p.done }
func (p *Parser) Status() string          { return p.status }

func overlapSuffix(data, needle []byte) int {
	max := len(needle) - 1
	if max > len(data) {
		max = len(data)
	}
	for n := max; n > 0; n-- {
		if bytes.Equal(data[len(data)-n:], needle[:n]) {
			return n
		}
	}
	return 0
}
