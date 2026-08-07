package assistantapi

import (
	"strings"
	"testing"
)

func TestRedactorHandlesSplitMixedCaseTokenAndPunctuation(t *testing.T) {
	redactor := NewRedactor()
	output := redactor.Write("before E")
	output += redactor.Write("vE, after ")
	output += redactor.Flush()
	if output != "before assistant runtime, after " {
		t.Fatalf("redacted output = %q", output)
	}
}

func TestRedactorHandlesKnownSignaturesAndLeavesNonMatch(t *testing.T) {
	token := string([]rune{'e', 'v', 'e'})
	input := strings.Join([]string{
		"event",
		"node_modules/" + token,
		"from \"" + token + "\"",
		"/" + token + "/v1",
		token + "_default",
		"@" + "ver" + "cel/connect/" + token,
		"sleeve_",
	}, " | ")
	output := RedactString(input)
	if !strings.HasPrefix(output, "event | ") {
		t.Fatalf("non-matching word was changed: %q", output)
	}
	if strings.Contains(output, redactionReplacement+"_") || !strings.Contains(output, "sleeve_") {
		t.Fatalf("normal word with signature suffix was changed: %q", output)
	}
	if strings.Count(output, redactionReplacement) != 5 {
		t.Fatalf("signature redactions = %q", output)
	}
}

func TestRedactorNormalizesUnicodeBeforeMatching(t *testing.T) {
	fullWidth := string([]rune{'Ｅ', 'ｖ', 'ｅ'})
	if got := RedactString("x " + fullWidth + " y"); got != "x assistant runtime y" {
		t.Fatalf("full-width output = %q", got)
	}
	if got := RedactString("e\u0301"); got != "é" {
		t.Fatalf("normalization output = %q", got)
	}
}

func TestRedactorChunkingMatchesWholeValue(t *testing.T) {
	token := string([]rune{'e', 'v', 'e'})
	whole := "prefix " + token + " suffix"
	chunked := RedactChunks([]string{"prefix ", string([]rune{'E'}), "v", "e suffix"})
	if chunked != RedactString(whole) {
		t.Fatalf("chunked = %q, whole = %q", chunked, RedactString(whole))
	}
}

func TestPublicErrorNormalizesAndRedactsMessage(t *testing.T) {
	token := string([]rune{'e', 'v', 'e'})
	publicError := NormalizeError(ErrorInternal, &testError{message: "failed " + token})
	if publicError.Message != "failed "+redactionReplacement {
		t.Fatalf("public error message = %q", publicError.Message)
	}
	if err := publicError.Validate(); err != nil {
		t.Fatal(err)
	}
}

type testError struct{ message string }

func (err *testError) Error() string { return err.message }
