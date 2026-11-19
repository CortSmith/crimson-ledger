package window

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
)

// FakeWindow records calls and lets tests invoke bound functions the way the page would.
type FakeWindow struct {
	Title         string
	Width, Height int
	HTML          string
	Evals         []string
	Ran, Gone     bool
	Terminated    bool
	bound         map[string]reflect.Value
	mu            sync.Mutex
}

// NewFake returns an empty FakeWindow.
func NewFake() *FakeWindow { return &FakeWindow{bound: map[string]reflect.Value{}} }

// SetTitle records the title.
func (f *FakeWindow) SetTitle(t string) { f.Title = t }

// SetSize records the size.
func (f *FakeWindow) SetSize(w, h int) { f.Width, f.Height = w, h }

// SetHTML records the page.
func (f *FakeWindow) SetHTML(h string) error { f.HTML = h; return nil }

// Eval records the script.
func (f *FakeWindow) Eval(js string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Evals = append(f.Evals, js)
}

// EvalCalls returns a copy of the recorded scripts; safe while another goroutine calls Eval.
func (f *FakeWindow) EvalCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.Evals...)
}

// Run marks the window as run.
func (f *FakeWindow) Run() { f.Ran = true }

// Terminate marks the window as terminated.
func (f *FakeWindow) Terminate() { f.Terminated = true }

// Destroy marks the window as destroyed.
func (f *FakeWindow) Destroy() { f.Gone = true }

// Bind accepts a func; like the real libraries, it must return a value, or a value and an error.
func (f *FakeWindow) Bind(name string, fn any) error {
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func || v.Type().NumOut() == 0 || v.Type().NumOut() > 2 {
		return fmt.Errorf("bind %q: not a func returning a value and optionally an error", name)
	}
	f.bound[name] = v
	return nil
}

// BindAsync records the func like Bind; tests call it synchronously.
func (f *FakeWindow) BindAsync(name string, fn any) error { return f.Bind(name, fn) }

// Call invokes a bound func with args and returns its result, or its error.
func (f *FakeWindow) Call(name string, args ...any) (any, error) {
	v, ok := f.bound[name]
	if !ok {
		return nil, fmt.Errorf("nothing bound as %q", name)
	}
	in := make([]reflect.Value, len(args))
	for i, a := range args {
		in[i] = reflect.ValueOf(a)
	}
	out := v.Call(in)
	if len(out) == 2 && !out[1].IsNil() {
		err, ok := out[1].Interface().(error)
		if !ok {
			return nil, errors.New("second result is not an error")
		}
		return nil, err
	}
	return out[0].Interface(), nil
}

// Has reports whether name is bound.
func (f *FakeWindow) Has(name string) bool { _, ok := f.bound[name]; return ok }
