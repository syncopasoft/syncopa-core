package distrib

import (
	"fmt"
	"strings"
	"time"

	"migratool/internal/task"
	"migratool/internal/worker"
)

const (
	actionCopy      = "copy"
	actionDelete    = "delete"
	actionCopyBatch = "copy_batch"
)

// TaskMessage represents the payload exchanged between the server and agents
// for an individual unit of work.
type TaskMessage struct {
	ID     string                 `json:"id"`
	Action string                 `json:"action"`
	Src    string                 `json:"src"`
	Dst    string                 `json:"dst"`
	Batch  *task.CopyBatchPayload `json:"batch,omitempty"`
}

// TaskResultMessage communicates the outcome of a task processed by an agent.
type TaskResultMessage struct {
	AgentID string             `json:"agent_id"`
	TaskID  string             `json:"task_id"`
	Success bool               `json:"success"`
	Error   string             `json:"error,omitempty"`
	Report  *TaskReportMessage `json:"report,omitempty"`
}

// TaskReportMessage is a JSON-friendly form of worker.TaskReport.
type TaskReportMessage struct {
	Action        string                `json:"action"`
	Source        string                `json:"source"`
	Destination   string                `json:"destination"`
	Bytes         int64                 `json:"bytes"`
	Hash          string                `json:"hash"`
	StartedAt     time.Time             `json:"started_at"`
	DurationMilli int64                 `json:"duration_ms"`
	BatchEntries  []task.CopyBatchEntry `json:"batch_entries,omitempty"`
}

// TaskToMessage converts a task and identifier to a transferable message.
func TaskToMessage(id string, t task.Task) (TaskMessage, error) {
	action, err := actionToString(t.Action)
	if err != nil {
		return TaskMessage{}, err
	}
	return TaskMessage{ID: id, Action: action, Src: t.Src, Dst: t.Dst, Batch: t.Batch}, nil
}

// ToTask converts a TaskMessage back into the internal task representation.
func (m TaskMessage) ToTask() (task.Task, error) {
	action, err := actionFromString(m.Action)
	if err != nil {
		return task.Task{}, err
	}
	return task.Task{Action: action, Src: m.Src, Dst: m.Dst, Batch: m.Batch}, nil
}

// ReportToMessage converts a worker.TaskReport into a TaskReportMessage.
func ReportToMessage(tr worker.TaskReport) TaskReportMessage {
	action, err := actionToString(tr.Action)
	if err != nil {
		// Action validation occurs before scheduling tasks, so reaching here
		// would indicate a programming error. Preserve the numeric value.
		action = fmt.Sprintf("unknown:%d", tr.Action)
	}
	return TaskReportMessage{
		Action:        action,
		Source:        tr.Source,
		Destination:   tr.Destination,
		Bytes:         tr.Bytes,
		Hash:          tr.Hash,
		StartedAt:     tr.StartedAt,
		DurationMilli: tr.Duration.Milliseconds(),
		BatchEntries:  append([]task.CopyBatchEntry(nil), tr.BatchEntries...),
	}
}

// ToTaskReport converts a TaskReportMessage into a worker.TaskReport.
func (m TaskReportMessage) ToTaskReport() (worker.TaskReport, error) {
	action, err := actionFromString(m.Action)
	if err != nil {
		return worker.TaskReport{}, err
	}
	return worker.TaskReport{
		Action:       action,
		Source:       m.Source,
		Destination:  m.Destination,
		Bytes:        m.Bytes,
		Hash:         m.Hash,
		StartedAt:    m.StartedAt,
		Duration:     time.Duration(m.DurationMilli) * time.Millisecond,
		BatchEntries: append([]task.CopyBatchEntry(nil), m.BatchEntries...),
	}, nil
}

func actionToString(a task.Action) (string, error) {
	switch a {
	case task.ActionCopy:
		return actionCopy, nil
	case task.ActionDelete:
		return actionDelete, nil
	case task.ActionCopyBatch:
		return actionCopyBatch, nil
	default:
		return "", fmt.Errorf("unsupported action %d", a)
	}
}

func actionFromString(s string) (task.Action, error) {
	switch strings.ToLower(s) {
	case actionCopy:
		return task.ActionCopy, nil
	case actionDelete:
		return task.ActionDelete, nil
	case actionCopyBatch:
		return task.ActionCopyBatch, nil
	default:
		return task.ActionCopy, fmt.Errorf("unknown action %q", s)
	}
}
