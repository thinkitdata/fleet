package agent

import (
	"errors"
	"fmt"

	"github.com/coreos/fleet/job"
	"github.com/coreos/fleet/pkg"
)

const (
	taskTypeLoadUnit   = "LoadUnit"
	taskTypeUnloadUnit = "UnloadUnit"
	taskTypeStartUnit  = "StartUnit"
	taskTypeStopUnit   = "StopUnit"

	taskReasonScheduledButNotRunnable    = "unit scheduled locally but unable to run"
	taskReasonScheduledButUnloaded       = "unit scheduled here but not loaded"
	taskReasonLoadedButNotScheduled      = "unit loaded but not scheduled here"
	taskReasonLoadedDesiredStateLaunched = "unit currently loaded but desired state is launched"
	taskReasonLaunchedDesiredStateLoaded = "unit currently launched but desired state is loaded"
	taskReasonPurgingAgent               = "purging agent"
)

type taskChain struct {
	unit  *job.Unit
	tasks []task
}

func newTaskChain(u *job.Unit, t ...task) taskChain {
	return taskChain{
		unit:  u,
		tasks: t,
	}
}

func (tc *taskChain) Add(t task) {
	tc.tasks = append(tc.tasks, t)
}

type task struct {
	typ    string
	reason string
}

type taskResult struct {
	task task
	err  error
}

type taskManager struct {
	processing pkg.Set
	mapper     taskMapperFunc
}

func newTaskManager() *taskManager {
	return &taskManager{
		processing: pkg.NewUnsafeSet(),
		mapper:     mapTaskToFunc,
	}
}

// Do attempts to complete a task against an Agent. If the task is unable
// to be attempted, an error is returned. A task is unable to be attempted
// if there exists in-flight any task with the same unit name. The returned
// error channel will be non-nil only if the task could be attempted. The
// channel will be closed when the task completes. If the task failed, an
// error will be sent to the channel. Do is not threadsafe.
func (tm *taskManager) Do(tc taskChain, a *Agent) (chan taskResult, error) {
	if tc.unit == nil {
		return nil, errors.New("unable to handle task with nil Job")
	}

	if tm.processing.Contains(tc.unit.Name) {
		return nil, errors.New("task already in flight")
	}

	// Do is not threadsafe due to the race between Contains and Add
	tm.processing.Add(tc.unit.Name)

	reschan := make(chan taskResult, len(tc.tasks))
	go func() {
		defer tm.processing.Remove(tc.unit.Name)
		for _, t := range tc.tasks {
			t := t
			res := taskResult{
				task: t,
			}

			taskFunc, err := tm.mapper(t, tc.unit, a)
			if err != nil {
				res.err = err
			} else {
				res.err = taskFunc()
			}

			reschan <- res
		}

		close(reschan)
	}()

	return reschan, nil
}

type taskMapperFunc func(t task, u *job.Unit, a *Agent) (func() error, error)

func mapTaskToFunc(t task, u *job.Unit, a *Agent) (fn func() error, err error) {
	switch t.typ {
	case taskTypeLoadUnit:
		fn = func() error { return a.loadUnit(u) }
	case taskTypeUnloadUnit:
		fn = func() error { a.unloadUnit(u.Name); return nil }
	case taskTypeStartUnit:
		fn = func() error { a.startUnit(u.Name); return nil }
	case taskTypeStopUnit:
		fn = func() error { a.stopUnit(u.Name); return nil }
	default:
		err = fmt.Errorf("unrecognized task type %q", t.typ)
	}

	return
}