package processors

import "testing"

// C7: strip the empty thought block that E2B/E4B emits when thinking
// is disabled. This is the most common case in practice.
func TestStripEmptyThoughtBlock(t *testing.T) {
	in := "<|channel>thought\n<channel|>Hello world"
	got := Strip(in)
	if got != "Hello world" {
		t.Errorf("got %q, want %q", got, "Hello world")
	}
}

// C7: strip a thought block containing reasoning.
func TestStripReasoningThoughtBlock(t *testing.T) {
	in := `<|channel>thought
Thinking Process:

1. Analyze the request.
2. Formulate the answer.<channel|>The answer is 42.`
	got := Strip(in)
	if got != "The answer is 42." {
		t.Errorf("got %q, want %q", got, "The answer is 42.")
	}
}

// C7: multiple thought blocks (e.g. multi-turn reasoning that leaked
// through) are all stripped.
func TestStripMultipleThoughtBlocks(t *testing.T) {
	in := "<|channel>thought\nfirst<channel|>answer A <|channel>thought\nsecond<channel|>answer B"
	got := Strip(in)
	want := "answer A answer B"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// C7: text with no thought block passes through unchanged.
func TestStripPassthrough(t *testing.T) {
	in := "Just an answer, no thinking tokens."
	got := Strip(in)
	if got != in {
		t.Errorf("got %q, want unchanged %q", got, in)
	}
}

// C7: idempotent.
func TestStripIdempotent(t *testing.T) {
	in := "<|channel>thought\nreasoning<channel|>final answer"
	once := Strip(in)
	twice := Strip(once)
	if once != twice {
		t.Errorf("Strip is not idempotent: once=%q twice=%q", once, twice)
	}
}

// C7: empty input is empty output.
func TestStripEmpty(t *testing.T) {
	if got := Strip(""); got != "" {
		t.Errorf("Strip(\"\") = %q", got)
	}
}

// C7: unclosed thought-open tag is treated as literal (no strip), so
// a truncated response from a cancelled stream doesn't silently eat
// all of the answer.
func TestStripDoesNotEatUnclosedOpen(t *testing.T) {
	in := "<|channel>thought\nthis never closed"
	got := Strip(in)
	if got != in {
		t.Errorf("unclosed thought should pass through; got %q", got)
	}
}
