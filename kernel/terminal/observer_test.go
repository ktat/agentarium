package terminal

import "testing"

// recordObserver は terminal.ObserverHooks を 1 実装で満たせることを示す。
type recordObserver struct {
	inputs  int
	outputs int
	forgets int
}

func (o *recordObserver) OnInput(id string, data []byte)  { o.inputs++ }
func (o *recordObserver) OnOutput(id string, data []byte) { o.outputs++ }
func (o *recordObserver) Forget(id string)                { o.forgets++ }

func TestObserverHooks_SingleImplementation(t *testing.T) {
	// 1 つの ObserverHooks 実装で契約を満たせる（xterm/wrap で別型でない）。
	var o ObserverHooks = &recordObserver{}
	o.OnInput("t1", []byte("x"))
	o.OnOutput("t1", []byte("y"))
	o.Forget("t1")
	ro := o.(*recordObserver)
	if ro.inputs != 1 || ro.outputs != 1 || ro.forgets != 1 {
		t.Fatalf("observer not invoked: %+v", ro)
	}
}

func TestStateListener_Signature(t *testing.T) {
	var called bool
	var l StateListener = func(id string, prev, next SessionState, source string) {
		called = true
	}
	l("t1", StateIdle, StateRunning, "pty")
	if !called {
		t.Fatal("listener not called")
	}
}
