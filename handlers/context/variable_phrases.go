package context

import (
	"fmt"
	"math/rand"
	"strings"
)

// variableConfirmPhrases are spoken for autoconfirm variables. They combine
// an acknowledgement with a direct verification question so the caller can
// catch mishearings. Both %s are replaced: first with the variable label,
// second with the value.
var variableConfirmPhrases = []string{
	"Ok, so I've got your %s as %s — is that right?",
	"Alright, I have your %s down as %s — does that sound correct?",
	"Got it — just to confirm, your %s is %s — is that right?",
	"Let me read that back: %s, %s — is that accurate?",
	"Ok, so your %s is %s — did I get that right?",
	"I've noted %s as %s — just confirming that's correct?",
	"Just to double-check — %s, %s — does that sound right?",
	"I've got your %s as %s — just want to confirm that's right.",
	"Alright, so I'm putting your %s as %s — is that ok?",
	"Let me verify — %s: %s — is that what you said?",
	"So I've got %s, %s — is that correct?",
	"Ok, I have your %s as %s — does that sound right to you?",
	"Just confirming — %s is %s — is that accurate?",
	"I've got your %s down as %s — is that good?",
	"Alright, so %s — %s — is that right?",
	"Got that — your %s is %s — just want to make sure I got that right.",
	"So %s: %s — did I catch that correctly?",
	"Ok, I have %s as %s — just checking that's right.",
	"I've noted your %s as %s — is that correct?",
	"Alright, %s: %s — does that sound right?",
}

// randomVariableConfirmPhrase picks a random confirmation phrase for the captured value.
// If custom is non-empty those templates are used ({{value}} is substituted);
// otherwise a template from the built-in pool is chosen and filled with the
// speech label derived from description and the raw value.
func randomVariableConfirmPhrase(description, value string, custom []string) string {
	if len(custom) > 0 {
		t := custom[rand.Intn(len(custom))]
		return strings.ReplaceAll(t, "{{value}}", value)
	}
	label := descToSpeechLabel(description)
	t := variableConfirmPhrases[rand.Intn(len(variableConfirmPhrases))]
	return fmt.Sprintf(t, label, value)
}

// descToSpeechLabel strips parenthetical qualifiers and trailing punctuation
// from a TaskVariable description so it reads naturally in spoken phrases.
// e.g. "A callback phone number in any common format (7–20 characters)."
// becomes "A callback phone number in any common format".
func descToSpeechLabel(description string) string {
	if idx := strings.IndexByte(description, '('); idx > 0 {
		description = strings.TrimSpace(description[:idx])
	}
	return strings.TrimRight(description, ".,;:")
}
