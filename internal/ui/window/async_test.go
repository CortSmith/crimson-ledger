package window

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func asyncHarness(t *testing.T, name string, fn any) (stub func(int, ...json.RawMessage) (bool, error), evals chan string, inits *[]string) {
	t.Helper()
	evals = make(chan string, 4)
	inits = new([]string)
	l := lowLevel{
		bind: func(n string, f any) error {
			if n != "__go_"+name {
				t.Fatalf("bound %q", n)
			}
			stub = f.(func(int, ...json.RawMessage) (bool, error))
			return nil
		},
		init: func(js string) { *inits = append(*inits, js) },
		eval: func(js string) { evals <- js },
	}
	if err := bindAsync(l, name, fn); err != nil {
		t.Fatal(err)
	}
	return stub, evals, inits
}

func TestBindAsync_ReturnsImmediatelyThenResolves(t *testing.T) {
	release := make(chan struct{})
	stub, evals, inits := asyncHarness(t, "slow", func(s string) (map[string]string, error) {
		<-release
		return map[string]string{"got": s}, nil
	})
	if len(*inits) != 1 || !strings.Contains((*inits)[0], `"slow"`) {
		t.Fatalf("init shim missing: %v", *inits)
	}
	done := make(chan struct{})
	go func() {
		ok, err := stub(7, json.RawMessage(`"x"`))
		if !ok || err != nil {
			t.Errorf("stub = %v %v", ok, err)
		}
		close(done)
	}()
	select {
	case <-done: // the UI-thread stub returned while fn is still blocked
	case <-time.After(time.Second):
		t.Fatal("stub blocked on the slow function")
	}
	close(release)
	if got := <-evals; got != `window.__goDone(7,true,{"got":"x"})` {
		t.Fatalf("eval = %s", got)
	}
}

func TestBindAsync_Error(t *testing.T) {
	stub, evals, _ := asyncHarness(t, "bad", func() (int, error) { return 0, errors.New("boom") })
	if _, err := stub(1); err != nil {
		t.Fatal(err)
	}
	if got := <-evals; got != `window.__goDone(1,false,"boom")` {
		t.Fatalf("eval = %s", got)
	}
}

func TestBindAsync_ArgMismatchRejectsAtOnce(t *testing.T) {
	stub, _, _ := asyncHarness(t, "one", func(int) (int, error) { return 0, nil })
	if _, err := stub(1); err == nil {
		t.Fatal("expected an argument error")
	}
}
