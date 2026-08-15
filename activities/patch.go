package activities

import (
	"fmt"
	"strconv"
	"strings"
)

// ParsePatch parses a unified diff string into structured hunks.
// Returns an error if the patch is malformed.
func ParsePatch(diff string) (*Patch, error) {
	lines := strings.Split(diff, "\n")
	patch := &Patch{}

	i := 0
	// Skip to first --- line
	for i < len(lines) && !strings.HasPrefix(lines[i], "--- ") {
		i++
	}
	if i >= len(lines) {
		return nil, fmt.Errorf("no --- header found")
	}
	patch.OrigFile = strings.TrimPrefix(lines[i], "--- ")
	i++

	if i >= len(lines) || !strings.HasPrefix(lines[i], "+++ ") {
		return nil, fmt.Errorf("missing +++ header")
	}
	patch.NewFile = strings.TrimPrefix(lines[i], "+++ ")
	i++

	// Parse hunks
	for i < len(lines) {
		if !strings.HasPrefix(lines[i], "@@") {
			i++
			continue
		}
		hunk, err := parseHunk(lines, &i)
		if err != nil {
			return nil, fmt.Errorf("parse hunk: %w", err)
		}
		patch.Hunks = append(patch.Hunks, hunk)
	}

	if len(patch.Hunks) == 0 {
		return nil, fmt.Errorf("no hunks found in patch")
	}

	return patch, nil
}

// Patch represents a parsed unified diff for a single file.
type Patch struct {
	OrigFile string
	NewFile  string
	Hunks    []*Hunk
}

// Hunk represents a single hunk in a unified diff.
type Hunk struct {
	OrigStart  int
	OrigCount  int
	NewStart   int
	NewCount   int
	Context    string
	Lines      []HunkLine
}

// HunkLine represents a single line in a hunk.
type HunkLine struct {
	Type    LineType // '+', '-', ' '
	Content string
}

type LineType byte

const (
	LineContext LineType = ' '
	LineAdd     LineType = '+'
	LineDel     LineType = '-'
)

func parseHunk(lines []string, idx *int) (*Hunk, error) {
	header := lines[*idx]
	*idx++

	origStart, origCount, newStart, newCount, err := parseHunkHeader(header)
	if err != nil {
		return nil, err
	}

	hunk := &Hunk{
		OrigStart: origStart,
		OrigCount: origCount,
		NewStart:  newStart,
		NewCount:  newCount,
	}

	// Parse hunk lines
	for *idx < len(lines) {
		line := lines[*idx]
		if strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			break
		}
		if len(line) == 0 {
			*idx++
			continue
		}
		switch line[0] {
		case '+':
			hunk.Lines = append(hunk.Lines, HunkLine{Type: LineAdd, Content: line[1:]})
		case '-':
			hunk.Lines = append(hunk.Lines, HunkLine{Type: LineDel, Content: line[1:]})
		case ' ':
			hunk.Lines = append(hunk.Lines, HunkLine{Type: LineContext, Content: line[1:]})
		default:
			// Unknown line type — skip
		}
		*idx++
	}

	return hunk, nil
}

func parseHunkHeader(header string) (origStart, origCount, newStart, newCount int, err error) {
	parts := strings.Fields(header)
	if len(parts) < 3 {
		return 0, 0, 0, 0, fmt.Errorf("invalid hunk header: %s", header)
	}

	origStart, origCount, err = parseRange(parts[1])
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("parse orig range: %w", err)
	}

	newStart, newCount, err = parseRange(parts[2])
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("parse new range: %w", err)
	}

	return origStart, origCount, newStart, newCount, nil
}

func parseRange(s string) (start, count int, err error) {
	s = strings.TrimPrefix(s, "+")
	s = strings.TrimPrefix(s, "-")

	parts := strings.SplitN(s, ",", 2)
	start, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("parse start: %w", err)
	}
	if len(parts) > 1 {
		count, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, fmt.Errorf("parse count: %w", err)
		}
	} else {
		count = 1
	}
	return start, count, nil
}

// ApplyPatch applies a parsed patch to the original file content.
// Returns an error if context lines do not match exactly.
func ApplyPatch(original string, patch *Patch) (string, error) {
	lines := strings.Split(original, "\n")

	for _, hunk := range patch.Hunks {
		var err error
		lines, err = applyHunk(lines, hunk)
		if err != nil {
			return "", fmt.Errorf("apply hunk at line %d: %w", hunk.OrigStart, err)
		}
	}

	return strings.Join(lines, "\n"), nil
}

func applyHunk(lines []string, hunk *Hunk) ([]string, error) {
	// hunk.OrigStart is 1-indexed
	pos := hunk.OrigStart - 1
	if pos < 0 {
		pos = 0
	}

	// Walk through the hunk lines and verify context
	for _, hl := range hunk.Lines {
		switch hl.Type {
		case LineContext:
			if pos >= len(lines) {
				return nil, fmt.Errorf("context line extends beyond file (pos=%d, len=%d)", pos, len(lines))
			}
			if lines[pos] != hl.Content {
				return nil, fmt.Errorf("context mismatch at line %d: expected %q, got %q", pos+1, hl.Content, lines[pos])
			}
			pos++
		case LineDel:
			if pos >= len(lines) {
				return nil, fmt.Errorf("deletion line extends beyond file (pos=%d, len=%d)", pos, len(lines))
			}
			if lines[pos] != hl.Content {
				return nil, fmt.Errorf("deletion mismatch at line %d: expected %q, got %q", pos+1, hl.Content, lines[pos])
			}
			// Remove the line
			lines = append(lines[:pos], lines[pos+1:]...)
		case LineAdd:
			// Insert the new line at pos
			lines = append(lines[:pos+1], lines[pos:]...)
			lines[pos] = hl.Content
			pos++
		}
	}

	return lines, nil
}

// ValidatePatch performs additional validation on a parsed patch:
// - Each hunk's context lines match the original file.
// - The patch modifies only the declared file.
// - Hunks are in order and non-overlapping.
func ValidatePatch(original string, patch *Patch, expectedFile string) error {
	if expectedFile != "" && patch.NewFile != expectedFile && patch.OrigFile != expectedFile {
		return fmt.Errorf("patch targets %q, expected %q", patch.NewFile, expectedFile)
	}

	lines := strings.Split(original, "\n")
	pos := 0
	for _, hunk := range patch.Hunks {
		hunkPos := hunk.OrigStart - 1
		if hunkPos < 0 {
			hunkPos = 0
		}
		if hunkPos < pos {
			return fmt.Errorf("hunk at line %d overlaps with previous hunk ending at %d", hunk.OrigStart, pos)
		}

		// Verify context lines
		origPos := hunkPos
		for _, hl := range hunk.Lines {
			if hl.Type == LineContext || hl.Type == LineDel {
				if origPos >= len(lines) {
					return fmt.Errorf("hunk context extends beyond file at line %d", origPos+1)
				}
				if lines[origPos] != hl.Content {
					return fmt.Errorf("context mismatch at line %d: expected %q, got %q", origPos+1, hl.Content, lines[origPos])
				}
				origPos++
			}
		}
		pos = origPos
	}

	return nil
}
