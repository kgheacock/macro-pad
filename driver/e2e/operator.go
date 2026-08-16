package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// defaultRunLogDir is where a scenario run's prompts and verdicts are
// recorded, relative to the package's own directory, so a reviewer can
// see exactly what a person was asked before ticking a human-verdict
// Definition-of-done item. See task 0020's Risks.
const defaultRunLogDir = "runs"

// Operator answers the two questions a scenario asks through Pad: Ask
// tells it to do something, and returns once that has been acknowledged
// or started; Confirm asks a yes/no question and waits for the answer.
// Close releases the operator's run log.
type Operator interface {
	Ask(prompt string)
	Confirm(prompt string) bool
	Close() error
}

// runLog appends one JSON line per Ask or Confirm call to a file under
// driver/e2e/runs/, so a scenario that ends in a person's verdict leaves
// a record of exactly what was asked and what they answered.
type runLog struct {
	f *os.File
}

type runLogEntry struct {
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"` // "ask" or "confirm"
	Prompt  string    `json:"prompt"`
	Verdict *bool     `json:"verdict,omitempty"`
}

var testNameSanitizer = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// newRunLog creates a new log file under dir, named after t's test name
// and the time it started, and returns a runLog appending to it. An empty
// dir defaults to defaultRunLogDir, relative to the current working
// directory — go test already runs with the package directory as its
// working directory, so this lands under driver/e2e/runs/.
func newRunLog(t testing.TB, dir string) (*runLog, error) {
	if dir == "" {
		dir = defaultRunLogDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("e2e: run log: %w", err)
	}
	name := fmt.Sprintf("%s-%s.jsonl",
		testNameSanitizer.ReplaceAllString(t.Name(), "_"),
		time.Now().UTC().Format("20060102T150405.000000000Z"))
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return nil, fmt.Errorf("e2e: run log: %w", err)
	}
	return &runLog{f: f}, nil
}

func (l *runLog) recordAsk(prompt string) {
	l.append(runLogEntry{Time: time.Now(), Kind: "ask", Prompt: prompt})
}

func (l *runLog) recordConfirm(prompt string, verdict bool) {
	l.append(runLogEntry{Time: time.Now(), Kind: "confirm", Prompt: prompt, Verdict: &verdict})
}

// append writes one entry as its own JSON line. A marshal or write
// failure is not fatal to the scenario the log is watching — the log is
// a record of the run, not part of its pass/fail verdict — so append
// drops the entry rather than propagating an error nothing calls it with
// a way to report.
func (l *runLog) append(e runLogEntry) {
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	l.f.Write(append(b, '\n'))
}

func (l *runLog) Close() error {
	return l.f.Close()
}

// HumanOperator drives Ask and Confirm through a person at a terminal:
// Ask prints the prompt and returns immediately, so a scenario's
// Key.ExpectPress calls are already watching by the time the person acts
// on it; Confirm prints the prompt and blocks for a yes/no answer.
// Attach wires one to standard input and output.
type HumanOperator struct {
	in  *bufio.Reader
	out io.Writer
	log *runLog
}

// NewHumanOperator returns an Operator that prompts through out and reads
// Confirm's answers from in, logging every prompt and verdict under
// driver/e2e/runs/.
func NewHumanOperator(t testing.TB, in io.Reader, out io.Writer) (*HumanOperator, error) {
	log, err := newRunLog(t, "")
	if err != nil {
		return nil, err
	}
	return &HumanOperator{in: bufio.NewReader(in), out: out, log: log}, nil
}

// Ask prints prompt and returns immediately — it does not wait for the
// person to finish; the scenario's own Key.ExpectPress or Pad.Confirm
// calls are what wait.
func (h *HumanOperator) Ask(prompt string) {
	h.log.recordAsk(prompt)
	fmt.Fprintf(h.out, "\n%s\n", prompt)
}

// Confirm prints prompt and blocks until the person answers y or n.
func (h *HumanOperator) Confirm(prompt string) bool {
	fmt.Fprintf(h.out, "\n%s [y/n] ", prompt)
	line, _ := h.in.ReadString('\n')
	verdict := strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y")
	h.log.recordConfirm(prompt, verdict)
	return verdict
}

func (h *HumanOperator) Close() error {
	return h.log.Close()
}

// ScriptedOperator drives Ask and Confirm with no person present, so the
// same scenario source that prompts a human, in a hardware run, also runs
// end to end against transport.Emulator. OnAsk queues an action to run,
// in its own goroutine, the next time Ask is called — a goroutine because
// Ask, like HumanOperator's, must return immediately for the scenario's
// own waiters to already be watching when the action presses a key.
// AnswerConfirm queues Confirm's next return value. An empty queue at
// call time just logs the prompt: Ask starts nothing further, and Confirm
// answers yes.
type ScriptedOperator struct {
	log     *runLog
	actions []func()
	answers []bool
}

// NewScriptedOperator returns an Operator with an empty script, logging
// every prompt and verdict under driver/e2e/runs/.
func NewScriptedOperator(t testing.TB) (*ScriptedOperator, error) {
	log, err := newRunLog(t, "")
	if err != nil {
		return nil, err
	}
	return &ScriptedOperator{log: log}, nil
}

// OnAsk queues action to run the next time Ask is called.
func (s *ScriptedOperator) OnAsk(action func()) {
	s.actions = append(s.actions, action)
}

// AnswerConfirm queues answer as Confirm's next return value.
func (s *ScriptedOperator) AnswerConfirm(answer bool) {
	s.answers = append(s.answers, answer)
}

func (s *ScriptedOperator) Ask(prompt string) {
	s.log.recordAsk(prompt)
	if len(s.actions) == 0 {
		return
	}
	action := s.actions[0]
	s.actions = s.actions[1:]
	go action()
}

func (s *ScriptedOperator) Confirm(prompt string) bool {
	verdict := true
	if len(s.answers) > 0 {
		verdict = s.answers[0]
		s.answers = s.answers[1:]
	}
	s.log.recordConfirm(prompt, verdict)
	return verdict
}

func (s *ScriptedOperator) Close() error {
	return s.log.Close()
}
