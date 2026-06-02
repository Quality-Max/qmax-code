package fastapply

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFinishFromAnthropic(t *testing.T) {
	cases := map[string]Finish{
		"end_turn":      FinishOK,
		"stop_sequence": FinishOK,
		"max_tokens":    FinishTruncated,
		"tool_use":      FinishOther,
		"":              FinishOther,
		"weird":         FinishOther,
	}
	for in, want := range cases {
		if got := FinishFromAnthropic(in); got != want {
			t.Errorf("FinishFromAnthropic(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestCheckSize(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		if err := CheckSize("small file", "tiny edit"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("file too large", func(t *testing.T) {
		err := CheckSize(strings.Repeat("a", MaxFileChars+1), "edit")
		if !errors.Is(err, ErrFileTooLarge) {
			t.Fatalf("got %v, want ErrFileTooLarge", err)
		}
	})
	t.Run("edit too large", func(t *testing.T) {
		// Small file (passes the file check) but an oversized edit pushes the
		// combined prompt past MaxInputChars.
		err := CheckSize("small", strings.Repeat("b", MaxInputChars))
		if !errors.Is(err, ErrEditTooLarge) {
			t.Fatalf("got %v, want ErrEditTooLarge", err)
		}
	})
}

func TestApply_Truncated(t *testing.T) {
	_, err := Apply("original\nfile\n", "partial out", FinishTruncated)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("got %v, want ErrTruncated", err)
	}
}

func TestApply_AbnormalFinish(t *testing.T) {
	_, err := Apply("original\nfile\n", "partial out", FinishOther)
	if !errors.Is(err, ErrAbnormalFinish) {
		t.Fatalf("got %v, want ErrAbnormalFinish", err)
	}
}

func TestApply_EmptyOutput(t *testing.T) {
	_, err := Apply("original", "   \n\t ", FinishOK)
	if !errors.Is(err, ErrEmptyOutput) {
		t.Fatalf("got %v, want ErrEmptyOutput", err)
	}
}

func TestApply_HappyPath(t *testing.T) {
	out, err := Apply("a = 1\n", "set a to 2\na = 2\n", FinishOK)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "set a to 2\na = 2\n" {
		t.Errorf("got %q", out)
	}
}

func TestApply_StripsOuterFence(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain fence", "```\ncode here\n```", "code here"},
		{"language tag", "```go\npackage main\n```", "package main"},
		{"surrounding whitespace", "\n  ```go\nx := 1\n```  \n", "x := 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Apply("orig\nfile\n", tc.in, FinishOK)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Original ends in \n, fenced body doesn't → trailing newline re-added.
			if out != tc.want+"\n" {
				t.Errorf("got %q, want %q", out, tc.want+"\n")
			}
		})
	}
}

func TestApply_DoesNotStripInternalFences(t *testing.T) {
	// A file whose body contains fenced blocks but isn't wholly wrapped must be
	// returned verbatim.
	doc := "# Readme\n\n```\nexample\n```\n\nmore text\n"
	out, err := Apply("# Readme\nold\n", doc, FinishOK)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != doc {
		t.Errorf("internal fences were altered:\n got %q\nwant %q", out, doc)
	}
}

func TestApply_PreservesTrailingNewline(t *testing.T) {
	t.Run("re-adds when original had one", func(t *testing.T) {
		out, err := Apply("a\nb\n", "a\nB", FinishOK)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != "a\nB\n" {
			t.Errorf("got %q, want %q", out, "a\nB\n")
		}
	})
	t.Run("does not add when original lacked one", func(t *testing.T) {
		out, err := Apply("a\nb", "a\nB", FinishOK)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != "a\nB" {
			t.Errorf("got %q, want %q", out, "a\nB")
		}
	})
}

func TestApply_DrasticShrinkGuard(t *testing.T) {
	orig := strings.Repeat("line\n", 40) // 40 newlines → 41 lines

	t.Run("fires on >50% drop of a large file", func(t *testing.T) {
		small := strings.Repeat("line\n", 5) // 6 lines, well under half
		_, err := Apply(orig, small, FinishOK)
		if !errors.Is(err, ErrDrasticShrink) {
			t.Fatalf("got %v, want ErrDrasticShrink", err)
		}
	})

	t.Run("allows a modest shrink", func(t *testing.T) {
		// 41 → 30 lines is well above the 50% floor; must pass.
		modest := strings.Repeat("line\n", 29) // 30 lines
		out, err := Apply(orig, modest, FinishOK)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != modest {
			t.Errorf("content altered unexpectedly")
		}
	})

	t.Run("ignored on small files (below line floor)", func(t *testing.T) {
		// A 10-line file dropping to 1 line is a normal edit, not a botched
		// rewrite — the guard must not fire below shrinkGuardMinLines.
		smallOrig := strings.Repeat("x\n", 10) // 11 lines, < 40
		_, err := Apply(smallOrig, "x\n", FinishOK)
		if err != nil {
			t.Fatalf("guard wrongly fired on a small file: %v", err)
		}
	})
}

func TestFastApply_FullFlowSuccess(t *testing.T) {
	gen := func(ctx context.Context, system, prompt string) (string, Finish, error) {
		if system != SystemPrompt {
			t.Errorf("generator got unexpected system prompt")
		}
		if !strings.Contains(prompt, "<original_file>") || !strings.Contains(prompt, "<edit>") {
			t.Errorf("prompt missing expected tags: %q", prompt)
		}
		return "updated = true\n", FinishOK, nil
	}
	out, err := FastApply(context.Background(), "updated = false\n", "flip it", gen)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "updated = true\n" {
		t.Errorf("got %q", out)
	}
}

func TestFastApply_PropagatesGeneratorError(t *testing.T) {
	boom := errors.New("model unavailable")
	gen := func(ctx context.Context, system, prompt string) (string, Finish, error) {
		return "", FinishOK, boom
	}
	_, err := FastApply(context.Background(), "x\n", "edit", gen)
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want propagated generator error", err)
	}
}

func TestFastApply_SizeCheckShortCircuitsBeforeModelCall(t *testing.T) {
	called := false
	gen := func(ctx context.Context, system, prompt string) (string, Finish, error) {
		called = true
		return "x", FinishOK, nil
	}
	_, err := FastApply(context.Background(), strings.Repeat("a", MaxFileChars+1), "edit", gen)
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("got %v, want ErrFileTooLarge", err)
	}
	if called {
		t.Error("generator was called despite a failed pre-flight size check (wasted/billed call)")
	}
}

func TestLineCount(t *testing.T) {
	cases := map[string]int{
		"":         1,
		"one line": 1,
		"a\nb":     2,
		"a\nb\n":   3,
		"\n":       2,
	}
	for in, want := range cases {
		if got := lineCount(in); got != want {
			t.Errorf("lineCount(%q) = %d, want %d", in, got, want)
		}
	}
}
