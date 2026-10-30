package store

// PublicationProblem locates one reason a spreadsheet publication was refused.
// Sheet names the spreadsheet, or is empty for a request field named by Column;
// Row counts the header as row 1. Reason is a stable code.
type PublicationProblem struct {
	Sheet  string `json:"sheet"`
	Row    int    `json:"row"`
	Column string `json:"column"`
	Reason string `json:"reason"`
}

// PublicationError lists every problem found in a publication, up to
// maxPublicationProblems. It is an ErrStaffInput.
type PublicationError struct {
	Problems []PublicationProblem
}

const maxPublicationProblems = 100

func (e *PublicationError) Error() string        { return "invalid publication" }
func (e *PublicationError) Is(target error) bool { return target == ErrStaffInput }

func (e *PublicationError) add(sheet string, row int, column, reason string) {
	if len(e.Problems) < maxPublicationProblems {
		e.Problems = append(e.Problems, PublicationProblem{Sheet: sheet, Row: row, Column: column, Reason: reason})
	}
}
