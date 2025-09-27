package task

// Action represents the type of work to perform for a task.
type Action int

const (
	// ActionCopy copies a file from Src to Dst.
	ActionCopy Action = iota
	// ActionDelete removes the path at Dst.
	ActionDelete
)

// Task represents work to be completed by the worker pool.
type Task struct {
	Action Action
	Src    string
	Dst    string
}
