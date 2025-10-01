package task

// Action represents the type of work to perform for a task.
type Action int

const (
	// ActionCopy copies a file from Src to Dst.
	ActionCopy Action = iota
	// ActionDelete removes the path at Dst.
	ActionDelete
	// ActionCopyBatch copies multiple files described in Batch.
	ActionCopyBatch
)

// Task represents work to be completed by the worker pool.
type Task struct {
	Action Action
	Src    string
	Dst    string
	Batch  *CopyBatchPayload
}

// CopyBatchPayload contains the metadata and serialized content for a batch
// of files. The Archive field holds a TAR archive that includes all file
// contents. Entries is ordered to match the files encoded in the archive so a
// worker can reconstruct each target.
type CopyBatchPayload struct {
	Entries []CopyBatchEntry
	Archive []byte
}

// CopyBatchEntry describes a single file within a batched copy request.
type CopyBatchEntry struct {
	Source      string
	Destination string
	Size        int64
}
