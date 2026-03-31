package remoteexec

import (
	"bytes"
	"context"
	"testing"
)

type fakeExec struct {
	script string
	chunks [][]byte
}

func (f *fakeExec) Exec(_ context.Context, script string, output func([]byte) error) error {
	f.script = script
	for _, chunk := range f.chunks {
		if err := output(chunk); err != nil {
			return err
		}
	}
	return nil
}

func TestParserHandlesSplitSentinel(t *testing.T) {
	p := &Parser{}
	a, done, _ := p.Feed([]byte("hello\n\x01agent"))
	if string(a) != "hello\n" || done {
		t.Fatalf("unexpected first feed: %q done=%v", a, done)
	}
	a, done, status := p.Feed([]byte("ic9-exit fail\n"))
	if len(a) != 0 || !done || status != "fail" {
		t.Fatalf("unexpected second feed: %q done=%v status=%q", a, done, status)
	}
}

func TestParserIgnoresLookalikeSentinelText(t *testing.T) {
	p := &Parser{}
	a, done, status := p.Feed([]byte("hello\nagentic9-exit fail\n"))
	if string(a) != "hello\nagentic9-exit fail\n" || done || status != "" {
		t.Fatalf("unexpected feed: %q done=%v status=%q", a, done, status)
	}
}

func TestRunnerBuildsAndParses(t *testing.T) {
	exec := &fakeExec{chunks: [][]byte{
		[]byte("ok\n"),
		[]byte("\x01agentic9-exit \n"),
	}}
	var out bytes.Buffer
	r := NewRunner(exec, "/tmp/work")
	result, err := r.Run(context.Background(), []string{"echo", "ok"}, func(b []byte) error {
		_, err := out.Write(b)
		return err
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.String() != "ok\n" {
		t.Fatalf("unexpected output: %q", out.String())
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if got := BuildScript("/tmp/work", []string{"echo", "ok"}); got != exec.script {
		t.Fatalf("unexpected script:\n%s\nwant:\n%s", exec.script, got)
	}
}

func TestBuildScriptQuotesEachArgumentForRC(t *testing.T) {
	got := BuildScript("/tmp/with 'quote'", []string{
		"echo",
		"two words",
		"O'Brien",
		"$path",
		"",
		";|&<>",
		"paren(arg)",
	})
	want := "cd '/tmp/with ''quote''' || exit 'cd'\n" +
		"'echo' 'two words' 'O''Brien' '$path' '' ';|&<>' 'paren(arg)'\n" +
		"status=$status\n" +
		"echo '\x01agentic9-exit '$status\n" +
		"exit $status\n"
	if got != want {
		t.Fatalf("unexpected script:\n%s\nwant:\n%s", got, want)
	}
}
