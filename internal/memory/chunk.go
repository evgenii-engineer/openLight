package memory

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// tokenCounter accumulates a token estimate across a stream of strings.
//
// It exists because summing EstimateTokens over the individual words of
// a sentence undercounts badly: integer division floors every short word
// to one token and the separators are never charged for. Counting runes
// once, across the whole stream, gives the same answer as calling
// EstimateTokens on the concatenation — which is what the retrieval
// budget actually has to respect.
type tokenCounter struct {
	plain int
	wide  int
}

func (c *tokenCounter) add(text string) {
	for _, r := range text {
		switch {
		case r < utf8.RuneSelf:
			c.plain++
		case unicode.Is(unicode.Cyrillic, r), unicode.Is(unicode.Han, r),
			unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r):
			c.wide++
		default:
			c.plain++
		}
	}
}

func (c tokenCounter) tokens() int {
	return c.plain/4 + c.wide/2
}

// EstimateTokens approximates a token count without pulling in a
// tokenizer. The heuristic is deliberately conservative for Cyrillic:
// multilingual BPE models spend noticeably more tokens per Russian
// character than per English one, and the whole point of the retrieval
// token budget is to not blow a small context window. Under-filling the
// budget costs a little recall; over-filling it truncates the user's
// actual question.
//
// Latin-ish text lands near chars/4, Cyrillic near chars/2.
func EstimateTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	var (
		wide  int // Cyrillic / CJK / other non-Latin runes
		plain int
	)
	for _, r := range text {
		switch {
		case r < utf8.RuneSelf:
			plain++
		case unicode.Is(unicode.Cyrillic, r), unicode.Is(unicode.Han, r),
			unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r):
			wide++
		default:
			plain++
		}
	}
	tokens := plain/4 + wide/2
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

// ChunkOptions controls the chunker. Values are in estimated tokens.
type ChunkOptions struct {
	TargetTokens  int
	OverlapTokens int
}

func (o ChunkOptions) normalized() ChunkOptions {
	if o.TargetTokens <= 0 {
		o.TargetTokens = 350
	}
	if o.OverlapTokens < 0 {
		o.OverlapTokens = 0
	}
	// Overlap must stay well below target or the chunker makes no
	// forward progress and produces near-duplicate chunks forever.
	if o.OverlapTokens >= o.TargetTokens/2 {
		o.OverlapTokens = o.TargetTokens / 4
	}
	return o
}

// TextChunk is one chunker output: the text plus the Markdown heading
// path it was found under, so retrieval can show where a fragment came
// from inside a long document.
type TextChunk struct {
	Text    string
	Heading string
	Tokens  int
}

// ChunkText splits text on structure rather than on byte offsets:
// Markdown headings start a new chunk, paragraphs are the atomic unit,
// and only a paragraph that alone exceeds the target is split further
// (on sentence boundaries, then on words as a last resort).
//
// Consecutive chunks carry OverlapTokens of trailing context from the
// previous chunk so a fact split across a paragraph boundary is still
// retrievable from either side.
func ChunkText(text string, opts ChunkOptions) []TextChunk {
	opts = opts.normalized()

	text = normalizeWhitespace(text)
	if text == "" {
		return nil
	}

	var (
		chunks   []TextChunk
		current  []string
		curTok   int
		heading  string
		lastTail string
	)

	flush := func() {
		if len(current) == 0 {
			return
		}
		body := strings.TrimSpace(strings.Join(current, "\n\n"))
		if body == "" {
			current = nil
			curTok = 0
			return
		}
		chunks = append(chunks, TextChunk{
			Text:    body,
			Heading: heading,
			Tokens:  EstimateTokens(body),
		})
		lastTail = tailTokens(body, opts.OverlapTokens)
		current = nil
		curTok = 0
	}

	startWithOverlap := func() {
		if opts.OverlapTokens > 0 && lastTail != "" {
			current = append(current, lastTail)
			curTok = EstimateTokens(lastTail)
		}
	}

	for _, block := range splitBlocks(text) {
		if h, ok := markdownHeading(block); ok {
			// A heading opens a new section: close the current chunk so
			// the section boundary is also a retrieval boundary.
			flush()
			heading = joinHeading(heading, h, headingLevel(block))
			continue
		}

		blockTok := EstimateTokens(block)

		if blockTok > opts.TargetTokens {
			flush()
			for _, piece := range splitOversizedBlock(block, opts.TargetTokens) {
				startWithOverlap()
				current = append(current, piece)
				curTok = EstimateTokens(piece)
				flush()
			}
			continue
		}

		if curTok > 0 && curTok+blockTok > opts.TargetTokens {
			flush()
			startWithOverlap()
		}
		current = append(current, block)
		curTok += blockTok
	}
	flush()

	return chunks
}

// splitBlocks breaks text into paragraph-level blocks. Headings are
// returned as their own block so the caller can react to them.
func splitBlocks(text string) []string {
	var blocks []string
	for _, paragraph := range strings.Split(text, "\n\n") {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		// A paragraph may still contain headings when the source used
		// single newlines around them.
		var pending []string
		for _, line := range strings.Split(paragraph, "\n") {
			if _, ok := markdownHeading(line); ok {
				if joined := strings.TrimSpace(strings.Join(pending, "\n")); joined != "" {
					blocks = append(blocks, joined)
				}
				pending = nil
				blocks = append(blocks, strings.TrimSpace(line))
				continue
			}
			pending = append(pending, line)
		}
		if joined := strings.TrimSpace(strings.Join(pending, "\n")); joined != "" {
			blocks = append(blocks, joined)
		}
	}
	return blocks
}

// splitOversizedBlock breaks a single over-long paragraph on sentence
// boundaries, falling back to word boundaries when a "sentence" is still
// too big (log lines, minified JSON, tables).
func splitOversizedBlock(block string, targetTokens int) []string {
	sentences := splitSentences(block)

	var (
		pieces  []string
		current []string
		curTok  int
	)
	flush := func() {
		if len(current) == 0 {
			return
		}
		if joined := strings.TrimSpace(strings.Join(current, " ")); joined != "" {
			pieces = append(pieces, joined)
		}
		current = nil
		curTok = 0
	}

	for _, sentence := range sentences {
		tok := EstimateTokens(sentence)
		if tok > targetTokens {
			flush()
			pieces = append(pieces, splitWords(sentence, targetTokens)...)
			continue
		}
		if curTok > 0 && curTok+tok > targetTokens {
			flush()
		}
		current = append(current, sentence)
		curTok += tok
	}
	flush()
	return pieces
}

func splitSentences(text string) []string {
	var (
		sentences []string
		current   strings.Builder
	)
	runes := []rune(text)
	for i, r := range runes {
		current.WriteRune(r)
		if r != '.' && r != '!' && r != '?' && r != '\n' {
			continue
		}
		// Only break when the next rune looks like a gap, so "3.14" and
		// "e.g." don't each become their own sentence.
		if i+1 < len(runes) && !unicode.IsSpace(runes[i+1]) {
			continue
		}
		if trimmed := strings.TrimSpace(current.String()); trimmed != "" {
			sentences = append(sentences, trimmed)
		}
		current.Reset()
	}
	if trimmed := strings.TrimSpace(current.String()); trimmed != "" {
		sentences = append(sentences, trimmed)
	}
	return sentences
}

func splitWords(text string, targetTokens int) []string {
	fields := strings.Fields(text)
	var (
		pieces  []string
		current []string
		curTok  int
	)
	for _, field := range fields {
		tok := EstimateTokens(field)
		if curTok > 0 && curTok+tok > targetTokens {
			pieces = append(pieces, strings.Join(current, " "))
			current = nil
			curTok = 0
		}
		current = append(current, field)
		curTok += tok
	}
	if len(current) > 0 {
		pieces = append(pieces, strings.Join(current, " "))
	}
	return pieces
}

// tailTokens returns roughly the last n estimated tokens of text, cut on
// a word boundary. Used to build inter-chunk overlap.
func tailTokens(text string, n int) string {
	if n <= 0 {
		return ""
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	var (
		tail []string
		tok  int
	)
	for i := len(fields) - 1; i >= 0; i-- {
		tok += EstimateTokens(fields[i])
		tail = append([]string{fields[i]}, tail...)
		if tok >= n {
			break
		}
	}
	return strings.Join(tail, " ")
}

func markdownHeading(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
	if title == "" {
		return "", false
	}
	return title, true
}

func headingLevel(line string) int {
	trimmed := strings.TrimSpace(line)
	level := 0
	for _, r := range trimmed {
		if r != '#' {
			break
		}
		level++
	}
	if level == 0 {
		return 1
	}
	return level
}

// joinHeading maintains a breadcrumb path ("Setup > Raspberry Pi") so a
// chunk taken from deep inside a document still says where it sits.
func joinHeading(existing, next string, level int) string {
	if level <= 1 || existing == "" {
		return next
	}
	parts := strings.Split(existing, " > ")
	if len(parts) >= level {
		parts = parts[:level-1]
	}
	parts = append(parts, next)
	return strings.Join(parts, " > ")
}

func normalizeWhitespace(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	// Collapse runs of blank lines to a single paragraph break so the
	// paragraph splitter sees consistent input.
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
