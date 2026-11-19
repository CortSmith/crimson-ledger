package window

import (
	"errors"
	"testing"
)

var _ Window = (*FakeWindow)(nil)

func TestFake_BindAndCall(t *testing.T) {
	f := NewFake()
	if err := f.Bind("add", func(a, b int) (int, error) { return a + b, nil }); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Call("add", 1, 2); err != nil || got.(int) != 3 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestFake_PropagatesError(t *testing.T) {
	f := NewFake()
	boom := errors.New("boom")
	_ = f.Bind("x", func() (string, error) { return "", boom })
	if _, err := f.Call("x"); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
}

func TestFake_RejectsBadBind(t *testing.T) {
	if NewFake().Bind("x", 5) == nil || NewFake().Bind("y", func() {}) == nil {
		t.Fatal("expected refusal")
	}
}

func TestStub_WithoutWebview(t *testing.T) {
	if _, err := New(Options{}); !errors.Is(err, ErrBuiltWithoutWebview) {
		t.Fatalf("got %v", err)
	}
}
