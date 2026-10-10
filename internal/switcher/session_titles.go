package switcher

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"golang.org/x/sys/unix"
)

// The optional local index supplies names without querying an account or reading
// rollout history. Retain names only for this page, within the scan's time budget.
const titleScanBytes = 64 << 20
const titleRecordBytes = 1 << 20

func readSessionTitles(ctx context.Context, home string, sessions []Session) (partial bool) {
	if len(sessions) == 0 || ctx.Err() != nil {
		return ctx.Err() != nil
	}
	fd, err := unix.Open(filepath.Join(home, "session_index.jsonl"), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return !os.IsNotExist(err)
	}
	f := os.NewFile(uintptr(fd), "session_index.jsonl")
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return true
	}
	positions := make(map[string]int, len(sessions))
	for i, session := range sessions {
		positions[session.ID] = i
	}
	updated := make(map[string]time.Time, len(sessions))
	// The index is append-only: scan a bounded tail, not an ever older prefix.
	start := max(int64(0), info.Size()-titleScanBytes)
	partial = start > 0
	skip := false
	if start > 0 {
		var previous [1]byte
		if _, err := f.ReadAt(previous[:], start-1); err != nil {
			return true
		}
		skip = previous[0] != '\n' // Never parse a record cut at the tail boundary.
	}
	reader := bufio.NewReaderSize(io.NewSectionReader(f, start, info.Size()-start), 64<<10)
	var line []byte
	for {
		if ctx.Err() != nil {
			return true
		}
		piece, readErr := reader.ReadSlice('\n')
		if !skip {
			if len(line)+len(piece) > titleRecordBytes {
				skip = true
				partial = true
				line = line[:0]
			} else {
				line = append(line, piece...)
			}
		}
		if errors.Is(readErr, bufio.ErrBufferFull) {
			continue // Discard oversized lines through their newline, then resume.
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return true
		}
		if !skip && len(bytes.TrimSpace(line)) > 0 {
			if !applySessionTitle(line, positions, updated, sessions) {
				partial = true
			}
		}
		line = line[:0]
		skip = false
		if errors.Is(readErr, io.EOF) {
			return partial
		}
	}
}

func applySessionTitle(line []byte, positions map[string]int, updated map[string]time.Time, sessions []Session) bool {
	var record struct {
		ID      string `json:"id"`
		Title   string `json:"thread_name"`
		Updated string `json:"updated_at"`
	}
	if json.Unmarshal(line, &record) != nil {
		return false // A partial append or damaged record does not hide the list.
	}
	position, wanted := positions[record.ID]
	if !wanted {
		return true
	}
	stamp, _ := time.Parse(time.RFC3339Nano, record.Updated)
	if previous, ok := updated[record.ID]; ok && !stamp.IsZero() && stamp.Before(previous) {
		return true
	}
	title := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, record.Title)
	title = strings.Join(strings.Fields(title), " ")
	if len([]rune(title)) > 240 {
		title = string([]rune(title)[:240]) + "…"
	}
	sessions[position].Title = title
	updated[record.ID] = stamp
	return true
}
