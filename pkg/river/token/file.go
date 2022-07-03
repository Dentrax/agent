package token

import (
	"fmt"
	"sort"
)

// NoPos is the zero value for Pos. It has no file or line information
// associated with it, and NoPos.IsValid is false.
var NoPos = Pos{}

// Pos is a compact representation of a position within a file. It can be
// converted into a Position for a more convenient, but larger, representation.
type Pos struct {
	file *File
	off  int
}

// File returns the file used by the Pos.
func (p Pos) File() *File { return p.file }

// Position converts Pos into a Position.
func (p Pos) Position() Position { return p.file.PositionFor(p) }

// Add creates a new Pos relative to p.
func (p Pos) Add(n int) Pos {
	return Pos{
		file: p.file,
		off:  p.off + n,
	}
}

// Offset returns the offset information associated with Pos.
func (p Pos) Offset() int { return p.off }

// IsValid reports whether the Pos is valid.
func (p Pos) IsValid() bool { return p != NoPos }

// Position holds full position information for a location within an individual
// file.
type Position struct {
	Filename string // Filename (if any)
	Offset   int    // Byte offset (starting at 0)
	Line     int    // Line number (starting at 1)
	Column   int    // Offset from start of line (starting at 1)
}

// IsValid reports whether the position is valid. Valid positions must have a
// Line value of at least 1.
func (pos *Position) IsValid() bool {
	return pos.Line >= 1
}

// String returns a string in one of the following forms:
//
//     file:line:column   Valid position with file name
//     file:line          Valid position with file name but no column
//     line:column        Valid position with no file name
//     line               Valid position with no file name or column
//     file               Invalid position with file name
//     -                  Invalid position with no file name
func (pos Position) String() string {
	s := pos.Filename

	if pos.IsValid() {
		if s != "" {
			s += ":"
		}
		s += fmt.Sprintf("%d", pos.Line)
		if pos.Column != 0 {
			s += fmt.Sprintf(":%d", pos.Column)
		}
	}

	if s == "" {
		s = "-"
	}
	return s
}

// File holds position information for a specific file.
type File struct {
	filename string
	lines    []int // Byte offset of each line number (first element is always 0)
}

// NewFile creates a new File for storing position information.
func NewFile(filename string) *File {
	return &File{
		filename: filename,
		lines:    []int{0},
	}
}

// Pos returns a Pos from a byte offset. off must be >= 0.
func (f *File) Pos(off int) Pos {
	if off < 0 {
		panic("Pos: illegal offset")
	}
	return Pos{file: f, off: off}
}

// Name returns the name of the file.
func (f *File) Name() string { return f.filename }

// AddLine tracks a new line from a byte offset. The line offset must be larger
// than the offset for the previous line, otherwise the line offset is ignored.
func (f *File) AddLine(offset int) {
	lines := len(f.lines)
	if lines == 0 || f.lines[lines-1] < offset {
		f.lines = append(f.lines, offset)
	}
}

// PositionFor returns a Position from an offset.
func (f *File) PositionFor(p Pos) Position {
	if p == NoPos {
		return Position{}
	}

	var line, column int
	if i := searchInts(f.lines, p.off); i >= 0 {
		line, column = i+1, p.off-f.lines[i]+1
	}

	return Position{
		Filename: f.filename,
		Offset:   p.off,
		Line:     line,
		Column:   column,
	}
}

func searchInts(a []int, x int) int {
	return sort.Search(len(a), func(i int) bool { return a[i] > x }) - 1
}
