package tts

import "regexp"

func normalizeTextForTTS(text string) string {
	// remove markdown formatting
	text = removeMarkdown(text)

	// remove emojis
	text = removeEmojis(text)

	// replace multiple spaces with a single space
	text = replaceMultipleSpaces(text)

	// trim leading and trailing whitespace
	text = trimWhitespace(text)

	return text
}

func removeMarkdown(text string) string {
	// This is a very basic implementation. You can expand this to cover more markdown features if needed.
	replacements := []struct {
		old string
		new string
	}{
		{"**", ""}, // bold
		{"*", ""},  // italic
		{"__", ""}, // underline
		{"~~", ""}, // strikethrough
		{"`", ""},  // inline code
	}
	for _, r := range replacements {
		text = replaceAll(text, r.old, r.new)
	}
	return text
}

func removeEmojis(text string) string {
	// This is a very basic implementation. You can use a more comprehensive regex or library for better emoji removal.
	return removeEmojiRegex.ReplaceAllString(text, "")
}

func replaceMultipleSpaces(text string) string {
	return multipleSpacesRegex.ReplaceAllString(text, " ")
}

func trimWhitespace(text string) string {
	return trimWhitespaceRegex.ReplaceAllString(text, "")
}

func replaceAll(text, old, new string) string {
	return replaceAllRegex(old).ReplaceAllString(text, new)
}

func replaceAllRegex(old string) *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(old))
}

var (
	removeEmojiRegex    = regexp.MustCompile(`[^\p{L}\p{N}\p{P}\p{Z}]`)
	multipleSpacesRegex = regexp.MustCompile(`\s+`)
	trimWhitespaceRegex = regexp.MustCompile(`^\s+|\s+$`)
)
