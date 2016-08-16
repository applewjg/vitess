package worker

// cloneType specifies whether it is a horizontal resharding or a vertical split.
// TODO(mberlin): Remove this once we merged both into one command.
type cloneType int

const (
	horizontalResharding cloneType = iota
	verticalSplit
)

// splitCloneInitializer is an interface which must be implemented for each
// "cloneType".
type splitCloneInitializer interface {
}
