package oteltracer

import (
	"context"
	"testing"

	"gochen/contextx/propagation"
	"gochen/observe"
)

func TestTracer_NoPanic(t *testing.T) {
	tr := New("svc")
	ctx, span := tr.StartSpan(context.Background(), "op", observe.WithAttribute("k", "v"))
	span.AddEvent("e1")
	span.SetStatus("ok", "")
	span.End()

	carrier := propagation.MapCarrier{}
	tr.Inject(ctx, carrier)
	_ = tr.Extract(context.Background(), carrier)
}
