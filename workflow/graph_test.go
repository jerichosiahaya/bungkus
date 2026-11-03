package workflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jerichosiahaya/bungkus"
)

type ticket struct {
	Input  string
	Route  string
	Answer string
	Review int
}

func mustAdd[S any](t *testing.T, g *Graph[S], name string, node Node[S]) {
	t.Helper()
	if err := g.Add(name, node); err != nil {
		t.Fatal(err)
	}
}

func TestGraphConditionalRoute(t *testing.T) {
	g := New[ticket]("support")
	mustAdd(t, g, "classify", func(_ context.Context, s *ticket) (string, error) {
		if strings.Contains(s.Input, "refund") {
			s.Route = "refund"
			return "refund", nil
		}
		return "answer", nil
	})
	mustAdd(t, g, "refund", func(_ context.Context, s *ticket) (string, error) {
		s.Answer = "refund processed"
		return "", nil
	})
	mustAdd(t, g, "answer", func(_ context.Context, s *ticket) (string, error) {
		s.Answer = "here is the information"
		return "", nil
	})
	g.StartAt("classify")

	for _, tc := range []struct {
		input string
		want  string
		trace []string
	}{
		{"Please issue a refund", "refund processed", []string{"classify", "refund"}},
		{"Where is my order?", "here is the information", []string{"classify", "answer"}},
	} {
		res, err := g.Run(context.Background(), ticket{Input: tc.input})
		if err != nil {
			t.Fatal(err)
		}
		if res.State.Answer != tc.want || !reflect.DeepEqual(res.Trace, tc.trace) {
			t.Errorf("input %q: state=%+v trace=%v", tc.input, res.State, res.Trace)
		}
	}
}

func TestGraphBoundedReviewLoop(t *testing.T) {
	g := New[ticket]("review")
	mustAdd(t, g, "draft", func(_ context.Context, s *ticket) (string, error) {
		s.Answer = fmt.Sprintf("draft %d", s.Review+1)
		return "review", nil
	})
	mustAdd(t, g, "review", func(_ context.Context, s *ticket) (string, error) {
		s.Review++
		if s.Review < 3 {
			return "draft", nil
		}
		return "", nil
	})
	g.StartAt("draft")
	if err := g.SetMaxExecutions(8); err != nil {
		t.Fatal(err)
	}
	res, err := g.Run(context.Background(), ticket{})
	if err != nil {
		t.Fatal(err)
	}
	if res.State.Review != 3 || res.Executions != 6 || res.State.Answer != "draft 3" {
		t.Errorf("got %+v", res)
	}
}

func TestGraphValidation(t *testing.T) {
	g := New[int]("")
	if err := g.Add("", func(context.Context, *int) (string, error) { return "", nil }); err == nil {
		t.Error("empty node name accepted")
	}
	if err := g.Add("nil", nil); err == nil {
		t.Error("nil node accepted")
	}
	mustAdd(t, g, "start", func(context.Context, *int) (string, error) { return "", nil })
	if err := g.Add("start", func(context.Context, *int) (string, error) { return "", nil }); err == nil {
		t.Error("duplicate node accepted")
	}
	if err := g.SetMaxExecutions(0); err == nil {
		t.Error("zero maximum accepted")
	}
	if _, err := g.Run(context.Background(), 1); err == nil {
		t.Error("missing start accepted")
	}
	g.StartAt("missing")
	if _, err := g.Run(context.Background(), 1); err == nil {
		t.Error("unknown start accepted")
	}
}

func TestGraphInvalidRoute(t *testing.T) {
	g := New[int]("bad-route")
	mustAdd(t, g, "start", func(context.Context, *int) (string, error) { return "missing", nil })
	g.StartAt("start")
	res, err := g.Run(context.Background(), 0)
	if err == nil || !strings.Contains(err.Error(), `routed to unknown node "missing"`) || !reflect.DeepEqual(res.Trace, []string{"start"}) {
		t.Fatalf("result=%+v err=%v", res, err)
	}
}

func TestGraphNodeErrorReturnsPartialState(t *testing.T) {
	wantErr := errors.New("classifier failed")
	g := New[int]("partial")
	mustAdd(t, g, "start", func(_ context.Context, state *int) (string, error) {
		*state = 7
		return "", wantErr
	})
	g.StartAt("start")
	res, err := g.Run(context.Background(), 1)
	if !errors.Is(err, wantErr) || res.State != 7 || res.Executions != 1 || !reflect.DeepEqual(res.Trace, []string{"start"}) {
		t.Fatalf("result=%+v err=%v", res, err)
	}
}

func TestGraphMaxExecutions(t *testing.T) {
	g := New[int]("loop")
	mustAdd(t, g, "again", func(_ context.Context, state *int) (string, error) {
		*state++
		return "again", nil
	})
	g.StartAt("again")
	if err := g.SetMaxExecutions(3); err != nil {
		t.Fatal(err)
	}
	res, err := g.Run(context.Background(), 0)
	if !errors.Is(err, ErrMaxExecutions) || res.State != 3 || res.Executions != 3 {
		t.Fatalf("result=%+v err=%v", res, err)
	}
}

func TestGraphCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	g := New[int]("cancel")
	mustAdd(t, g, "cancel", func(_ context.Context, state *int) (string, error) {
		calls++
		*state++
		cancel()
		return "cancel", nil
	})
	g.StartAt("cancel")
	res, err := g.Run(ctx, 0)
	if !errors.Is(err, context.Canceled) || calls != 1 || res.State != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", res, calls, err)
	}
}

type workflowObserver struct {
	starts []bungkus.RunInfo
	ends   []bungkus.RunInfo
	errs   []bungkus.RunInfo
}

type workflowContextKey struct{}

func (o *workflowObserver) OnStart(ctx context.Context, info bungkus.RunInfo, _ any) context.Context {
	o.starts = append(o.starts, info)
	return context.WithValue(ctx, workflowContextKey{}, true)
}
func (o *workflowObserver) OnEnd(ctx context.Context, info bungkus.RunInfo, _ any) {
	if ctx.Value(workflowContextKey{}) != true {
		panic("handler-derived context was not propagated")
	}
	o.ends = append(o.ends, info)
}
func (o *workflowObserver) OnError(ctx context.Context, info bungkus.RunInfo, _ error) {
	if ctx.Value(workflowContextKey{}) != true {
		panic("handler-derived context was not propagated")
	}
	o.errs = append(o.errs, info)
}

func TestGraphHandlerNesting(t *testing.T) {
	observer := &workflowObserver{}
	ctx := bungkus.WithHandlers(context.Background(), observer)
	g := New[string]("safe-name")
	mustAdd(t, g, "node", func(_ context.Context, state *string) (string, error) {
		*state = "sensitive output"
		return "", nil
	})
	g.StartAt("node")
	_, err := g.Run(ctx, "sensitive input")
	if err != nil {
		t.Fatal(err)
	}
	if len(observer.starts) != 2 || len(observer.ends) != 2 || len(observer.errs) != 0 {
		t.Fatalf("starts=%v ends=%v errors=%v", observer.starts, observer.ends, observer.errs)
	}
	if observer.starts[0].Component != bungkus.ComponentWorkflow || observer.starts[1].Component != bungkus.ComponentWorkflowNode || observer.starts[1].Name != "node" {
		t.Errorf("unexpected runs: %+v", observer.starts)
	}
}
