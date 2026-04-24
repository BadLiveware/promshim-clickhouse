package native

// cloneInt64Pointer returns a fresh copy of the int64 pointed to by
// value, or nil if value itself is nil. It is used by selector and
// analysis code that needs to defensively copy timestamp pointers
// carried on selector shapes.
func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
