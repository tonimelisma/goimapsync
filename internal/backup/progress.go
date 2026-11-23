package backup

// Progress receives how far a backup of one data type is: done of total
// units handled, successfully or not. Units are messages to download for
// mail and collections (calendars, address books, task lists) for the
// other data types (D77).
// A nil Progress reports nothing.
type Progress func(done, total int)

// Report passes progress on; it does nothing when p is nil.
func (p Progress) Report(done, total int) {
	if p != nil {
		p(done, total)
	}
}
